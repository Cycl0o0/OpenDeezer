package audio

import (
	"math"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/Cycl0o0/OpenDeezer/v3/internal/deezer"
)

func sampleAt(b []byte, frameIdx int) int16 {
	o := frameIdx * frameBytes // left channel of the frame
	return int16(uint16(b[o]) | uint16(b[o+1])<<8)
}

// TestApplyFadeOutRamp checks applyFadeOut produces a monotone down-ramp from
// near full scale to near zero across fadeOutFrames, not a hard cut.
func TestApplyFadeOutRamp(t *testing.T) {
	n := int(fadeOutFrames)
	b := make([]byte, n*frameBytes)
	for i := 0; i+1 < len(b); i += 2 {
		b[i] = 0xFF
		b[i+1] = 0x7F // 0x7FFF = 32767 full scale
	}
	rem := applyFadeOut(b, fadeOutFrames)
	if rem != 0 {
		t.Fatalf("ramp should be spent over a full-length buffer, %d frames left", rem)
	}
	first := sampleAt(b, 0)
	last := sampleAt(b, n-1)
	if first < 30000 {
		t.Fatalf("first frame should stay near full scale, got %d", first)
	}
	if last > 2000 {
		t.Fatalf("last frame should ramp near zero, got %d", last)
	}
	if last >= first {
		t.Fatalf("expected a downward ramp (first %d > last %d)", first, last)
	}
}

// newPlaybackTestPlayer builds a Player wired for driving readPCM directly (no
// output device): unity volume/gain/sleep so full-scale PCM passes through
// unattenuated, and a nil EQ config (applyEQ is then a no-op).
func newPlaybackTestPlayer() *Player {
	p := &Player{}
	p.state.Store(int32(Stopped))
	p.volume.Store(math.Float64bits(1))
	p.gainFac.Store(math.Float64bits(1))
	p.sleepGain.Store(math.Float64bits(1))
	return p
}

// filledSource returns a source whose ring is pre-loaded with nbytes of
// full-scale (0x7FFF) PCM, for driving readPCM without the download/decode
// pipeline.
func filledSource(t *testing.T, nbytes int) *source {
	t.Helper()
	s := newSource(&deezer.StreamPlan{Format: "MP3"}, 100000)
	data := make([]byte, nbytes)
	for i := 0; i+1 < len(data); i += 2 {
		data[i] = 0xFF
		data[i+1] = 0x7F
	}
	if !s.ring.write(data, s.ring.seq()) {
		t.Fatal("ring.write failed to preload PCM")
	}
	return s
}

// TestReadPCMFadeOut drives the realtime callback directly and confirms that
// arming a fade-out (as Pause/Stop/skip/seek do) makes the next callback ramp the
// PCM down instead of cutting it hard, and that once the ramp is spent the output
// is muted until the deferred state flip.
func TestReadPCMFadeOut(t *testing.T) {
	p := newPlaybackTestPlayer()
	frames := int(fadeOutFrames)
	src := filledSource(t, 4*frames*frameBytes)
	p.cur.Store(src)
	p.state.Store(int32(Playing))

	buf := make([]byte, frames*frameBytes)

	// Baseline: normal playback keeps full-scale samples (no fade armed).
	if n := p.readPCM(buf); n != len(buf) {
		t.Fatalf("baseline read short: %d of %d", n, len(buf))
	}
	if got := sampleAt(buf, 0); got < 30000 {
		t.Fatalf("baseline first frame should be full scale, got %d", got)
	}

	// Arm the fade-out (what Pause/Stop/skip/seek do before their deferred flip);
	// frame count first, then armed — the publication order fadeOutAndWait uses.
	p.fadeOut.Store(fadeOutFrames)
	p.fadeOutArmed.Store(true)
	p.readPCM(buf)
	first := sampleAt(buf, 0)
	last := sampleAt(buf, frames-1)
	if first < 30000 {
		t.Fatalf("fade-out first frame should start near full scale, got %d", first)
	}
	if last > 2000 {
		t.Fatalf("fade-out last frame should be near zero, got %d (looks like a hard cut/no ramp)", last)
	}
	if p.fadeOut.Load() != 0 {
		t.Fatalf("ramp should be spent after a full-length callback, %d frames left", p.fadeOut.Load())
	}

	// After the ramp completes, while still armed (awaiting the control op's flip),
	// output must be muted rather than resurging to full volume.
	p.readPCM(buf)
	for i := 0; i < frames; i++ {
		if v := sampleAt(buf, i); v != 0 {
			t.Fatalf("post-ramp output must be muted; frame %d = %d", i, v)
		}
	}
}

// TestFadeOutArmPublishesFramesFirst pins fadeOutAndWait's publication order:
// the RT callback reads fadeOutArmed first and, when set with fadeOut==0,
// hard-mutes the buffer (the intended post-ramp mute). At ARM time that
// combination must be unobservable — armed==true has to imply the frame count
// is already published, or the callback hard-zeroes a full-volume buffer
// before the ramp even starts (the exact discontinuity the fade prevents).
// Go atomics are sequentially consistent, so storing fadeOut before
// fadeOutArmed makes the bad interleaving impossible; an observer goroutine
// emulating the callback's read order asserts it never appears.
func TestFadeOutArmPublishesFramesFirst(t *testing.T) {
	for iter := 0; iter < 200; iter++ {
		p := newPlaybackTestPlayer()
		p.state.Store(int32(Playing))
		var violated atomic.Bool
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				if p.fadeOutArmed.Load() {
					// Callback's view at arm time: armed must come with frames.
					if p.fadeOut.Load() == 0 {
						violated.Store(true)
					}
					// Play the ramp out so fadeOutAndWait returns promptly.
					p.fadeOut.Store(0)
					return
				}
				runtime.Gosched()
			}
		}()
		p.fadeOutAndWait()
		<-done
		if violated.Load() {
			t.Fatalf("iteration %d: callback observed fadeOutArmed with fadeOut==0 at arm time (pre-ramp hard mute)", iter)
		}
	}
}
