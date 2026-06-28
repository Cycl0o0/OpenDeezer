// Package audio is the playback engine: it streams, decrypts and decodes Deezer
// audio (MP3 + FLAC) into a PCM ring that an output device drains. Supports seek,
// per-track ReplayGain, gapless transitions and (experimental) crossfade.
// In-memory only — nothing is written to disk.
//
// The output device is abstracted behind the `output` interface so the backend
// is build-tag-selected: malgo/miniaudio by default (adds output-device
// selection), or oto under the `otosink` tag — used for the macOS GUI, where
// malgo's CoreAudio callback runs unreliably inside the c-archive.
package audio

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Cycl0o0/OpenDeezer/internal/deezer"
	odlog "github.com/Cycl0o0/OpenDeezer/internal/log"
	"github.com/hajimehoshi/go-mp3"
)

// output is the platform audio sink. start() begins pulling PCM via read, which
// fills the given buffer (zeroing any tail it doesn't produce) and returns the
// number of bytes it actually wrote (for diagnostics). The backend is selected
// by build tag (output_malgo.go / output_oto.go).
type output interface {
	start(read func(out []byte) int) error
	devices() ([]Device, error)
	setDevice(id string) error
	currentDevice() string
	close()
}

// Device is an output device the user can pick (empty ID = system default).
type Device struct {
	ID        string
	Name      string
	IsDefault bool
}

// State is the player's lifecycle state.
type State int

const (
	Stopped State = iota
	Loading
	Playing
	Paused
	Errored
)

func (s State) String() string {
	switch s {
	case Loading:
		return "Loading"
	case Playing:
		return "Playing"
	case Paused:
		return "Paused"
	case Errored:
		return "Error"
	default:
		return "Stopped"
	}
}

const (
	sampleRate  = 44100
	channels    = 2
	frameBytes  = channels * 2 // s16 stereo
	bytesPerSec = sampleRate * frameBytes
	ringMax     = 4 * bytesPerSec // ~4s of decoded PCM buffered (headroom vs underrun)
	prebufferB  = 2 * bytesPerSec // fill ~2s before starting (clean intro, no underrun burst)
	decodeChunk = 16 * 1024
)

// streamUserAgent is sent when fetching audio so third-party podcast hosts
// (Acast etc.) don't reject the default Go agent with 403.
const streamUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"

// pcmStream is a decoder yielding interleaved s16 PCM, seekable by PCM byte
// offset. Both *mp3.Decoder and *flacStream satisfy it.
type pcmStream interface {
	io.Reader
	Seek(offset int64, whence int) (int64, error)
}

// source is one track's pipeline: download+decrypt -> streamBuffer -> decoder ->
// pcmRing. The download and decode each run on their own goroutine.
type source struct {
	plan   *deezer.StreamPlan
	durMS  int64
	format string
	sb     *streamBuffer
	ring   *pcmRing
	eof    atomic.Bool  // decoder reached end and ring will not grow
	seekTo atomic.Int64 // pending PCM-byte seek target, or -1
	dead   atomic.Bool
	errMsg atomic.Value // string: download/decode error, if any
}

func (s *source) setErr(err error) {
	if err != nil {
		s.errMsg.Store(err.Error())
	}
}

func (s *source) lastErr() string {
	v, _ := s.errMsg.Load().(string)
	return v
}

// Player owns the malgo context + one output device and plays a current source,
// optionally with a preloaded next source for gapless/crossfade.
type Player struct {
	out output

	// cur/next are accessed lock-free from the realtime audio callback.
	cur  atomic.Pointer[source]
	next atomic.Pointer[source]

	state       atomic.Int32
	played      atomic.Int64 // PCM bytes the callback has consumed from cur (position)
	totalMS     atomic.Int64
	lastErr     atomic.Value // string
	format      atomic.Value // string
	volume      atomic.Uint64
	gainFac     atomic.Uint64
	rgOn        atomic.Bool
	gapless     atomic.Bool
	crossfadeMS atomic.Int64
	cbCount     atomic.Int64 // audio callbacks served (diagnostics)
	cbUnderrun  atomic.Int64 // callbacks with a short read (ring starvation)
	onFinish    func()

	stopMgr chan struct{}
	mgrOnce sync.Once
}

// ---- ReplayGain / volume ----

func dbToFactor(db float64) float64 {
	if db == 0 {
		return 1
	}
	f := math.Pow(10, db/20)
	if f > 1 {
		f = 1
	}
	if f < 0 {
		f = 0
	}
	return f
}

func (p *Player) SetReplayGain(on bool) {
	p.rgOn.Store(on)
	if !on {
		p.gainFac.Store(math.Float64bits(1))
	}
}
func (p *Player) ReplayGain() bool { return p.rgOn.Load() }

func (p *Player) effectiveVolume() float64 {
	f := math.Float64frombits(p.gainFac.Load())
	if f == 0 {
		f = 1
	}
	return p.Volume() * f
}

// SetGapless enables/disables gapless transitions between tracks.
func (p *Player) SetGapless(on bool) { p.gapless.Store(on) }

// Gapless reports whether gapless transitions are enabled.
func (p *Player) Gapless() bool { return p.gapless.Load() }
func (p *Player) SetCrossfadeMS(ms int) {
	if ms < 0 {
		ms = 0
	}
	p.crossfadeMS.Store(int64(ms))
}
func (p *Player) CrossfadeMS() int { return int(p.crossfadeMS.Load()) }

// ---- construction ----

// NewPlayer initializes the audio output (backend chosen by build tag).
func NewPlayer() (*Player, error) {
	out, err := newOutput()
	if err != nil {
		return nil, err
	}
	p := &Player{out: out, stopMgr: make(chan struct{})}
	p.state.Store(int32(Stopped))
	p.lastErr.Store("")
	p.format.Store("")
	p.gainFac.Store(math.Float64bits(1))
	p.gapless.Store(true)
	p.setVolume(1.0)
	if err := p.out.start(p.readPCM); err != nil {
		p.out.close()
		return nil, err
	}
	go p.manage()
	return p, nil
}

// readPCM fills out with the next PCM for the device (zeroing any tail it can't
// produce) and returns the bytes actually written. Called from the backend's
// realtime pull (malgo callback / oto reader); must be fast + lock-free.
func (p *Player) readPCM(out []byte) int {
	for i := range out {
		out[i] = 0
	}
	if State(p.state.Load()) != Playing {
		return 0
	}
	cur := p.cur.Load()
	next := p.next.Load()
	xfadeMS := p.crossfadeMS.Load()
	if cur == nil {
		return 0
	}

	n := cur.ring.read(out)

	// Crossfade: within the crossfade window of the end, with a next source
	// ready, mix in next (fading in) while cur fades out.
	if xfadeMS > 0 && next != nil {
		total := cur.durMS
		pos := p.played.Load() * 1000 / bytesPerSec
		if total > 0 && pos >= total-xfadeMS {
			mix := make([]byte, len(out))
			m := next.ring.read(mix)
			fade := float64(pos-(total-xfadeMS)) / float64(xfadeMS)
			if fade < 0 {
				fade = 0
			} else if fade > 1 {
				fade = 1
			}
			mixPCM(out[:n], out[:n], 1-fade)
			mixPCM(mix[:m], mix[:m], fade)
			addPCM(out, mix)
			if m > n {
				n = m
			}
		}
	}

	applyGain(out[:n], p.effectiveVolume())
	p.played.Add(int64(n))

	// Diagnostics: a short read means the ring didn't have a full callback's
	// worth of PCM ready (decode/producer starvation). Counted so we can tell
	// ring-underrun glitches from device/callback-jitter glitches.
	p.cbCount.Add(1)
	if n < len(out) {
		p.cbUnderrun.Add(1)
	}
	return n
}

// ---- volume ----

func (p *Player) setVolume(v float64) {
	if math.IsNaN(v) {
		return // ignore NaN (would corrupt every sample)
	}
	if v < 0 {
		v = 0
	} else if v > 1 {
		v = 1
	}
	p.volume.Store(math.Float64bits(v))
}
func (p *Player) Volume() float64 { return math.Float64frombits(p.volume.Load()) }

// SetVolume sets the absolute volume (clamped to 0..1).
func (p *Player) SetVolume(v float64) { p.setVolume(v) }
func (p *Player) AddVolume(delta float64) float64 {
	p.setVolume(p.Volume() + delta)
	return p.Volume()
}

// ---- accessors ----

func (p *Player) Format() string        { s, _ := p.format.Load().(string); return s }
func (p *Player) SetOnFinish(fn func()) { p.onFinish = fn }
func (p *Player) State() State          { return State(p.state.Load()) }
func (p *Player) LastError() string     { s, _ := p.lastErr.Load().(string); return s }
func (p *Player) PositionMS() int64     { return p.played.Load() * 1000 / bytesPerSec }
func (p *Player) DurationMS() int64     { return p.totalMS.Load() }

// ---- playback ----

// Play starts a track immediately, replacing anything current.
func (p *Player) Play(plan *deezer.StreamPlan, durationMS int64) error {
	p.stopSources()
	p.state.Store(int32(Loading))
	p.lastErr.Store("")
	p.played.Store(0)
	p.totalMS.Store(durationMS)
	p.format.Store(plan.Format)
	if p.rgOn.Load() {
		p.gainFac.Store(math.Float64bits(dbToFactor(plan.GainDB)))
	} else {
		p.gainFac.Store(math.Float64bits(1))
	}

	src := newSource(plan, durationMS)
	go src.download()
	go src.decode()

	old := p.cur.Swap(src)
	oldNext := p.next.Swap(nil)
	if old != nil {
		old.kill()
	}
	if oldNext != nil {
		oldNext.kill()
	}
	// Stay Loading; the manager flips to Playing once the ring is prebuffered, so
	// the callback never pulls from a half-filled ring (which caused a burst of
	// underruns — a choppy intro — at the start of every track).
	return nil
}

// Preload prepares the next track so the transition is gapless/crossfaded. It is
// a no-op if gapless is disabled.
func (p *Player) Preload(plan *deezer.StreamPlan, durationMS int64) {
	if !p.gapless.Load() && p.crossfadeMS.Load() == 0 {
		return
	}
	src := newSource(plan, durationMS)
	go src.download()
	go src.decode()
	if old := p.next.Swap(src); old != nil {
		old.kill()
	}
}

// manage advances to the preloaded next source when the current one is drained.
func (p *Player) manage() {
	ticker := time.NewTicker(40 * time.Millisecond)
	defer ticker.Stop()
	ticks := 0
	for {
		select {
		case <-p.stopMgr:
			return
		case <-ticker.C:
			// Diagnostics: log callback/underrun counts every ~5s while playing.
			if ticks++; ticks%125 == 0 {
				if c := p.cbCount.Load(); c > 0 {
					var rb int
					if cur := p.cur.Load(); cur != nil {
						rb = cur.ring.buffered()
					}
					odlog.Debug("audio: callbacks=%d underruns=%d ringBuf=%dKB state=%v",
						c, p.cbUnderrun.Load(), rb/1024, p.State())
				}
			}
			// Prebuffer: promote Loading -> Playing once the ring has filled (or
			// the track is short/decoded), so the callback starts from a healthy
			// ring instead of underrunning while it fills.
			if State(p.state.Load()) == Loading {
				if cur := p.cur.Load(); cur != nil &&
					(cur.ring.buffered() >= prebufferB || cur.eof.Load()) {
					p.state.Store(int32(Playing))
				}
			}
			if State(p.state.Load()) != Playing {
				continue
			}
			cur := p.cur.Load()
			next := p.next.Load()
			if cur == nil {
				continue
			}
			if cur.eof.Load() && cur.ring.buffered() == 0 {
				if e := cur.lastErr(); e != "" {
					p.lastErr.Store(e)
				}
				if next != nil {
					// Seamless swap to the preloaded next track.
					p.cur.Store(next)
					p.next.Store(nil)
					cur.kill()
					p.played.Store(0)
					p.totalMS.Store(next.durMS)
					p.format.Store(next.format)
					if p.onFinish != nil {
						p.onFinish()
					}
				} else {
					p.state.Store(int32(Stopped))
					if p.onFinish != nil {
						p.onFinish()
					}
				}
			}
		}
	}
}

// SeekMS jumps to an absolute position in the current track.
func (p *Player) SeekMS(ms int64) {
	cur := p.cur.Load()
	if cur == nil {
		return
	}
	if ms < 0 {
		ms = 0
	}
	if total := p.totalMS.Load(); total > 0 && ms > total {
		ms = total
	}
	off := ms * bytesPerSec / 1000
	off -= off % frameBytes
	p.played.Store(off)
	cur.requestSeek(off)
}

func (p *Player) Pause() {
	if p.State() == Playing {
		p.state.Store(int32(Paused))
	}
}
func (p *Player) Resume() {
	if p.State() == Paused {
		p.state.Store(int32(Playing))
	}
}
func (p *Player) TogglePause() {
	switch p.State() {
	case Playing:
		p.Pause()
	case Paused:
		p.Resume()
	}
}

// Stop halts playback and releases sources.
func (p *Player) Stop() {
	p.stopSources()
	p.state.Store(int32(Stopped))
}

func (p *Player) stopSources() {
	if cur := p.cur.Swap(nil); cur != nil {
		cur.kill()
	}
	if next := p.next.Swap(nil); next != nil {
		next.kill()
	}
}

// Close tears down sources and the output device.
func (p *Player) Close() {
	p.mgrOnce.Do(func() { close(p.stopMgr) })
	p.stopSources()
	if p.out != nil {
		p.out.close()
	}
}

// ---- output device selection (delegates to the backend) ----

// Devices lists available output devices.
func (p *Player) Devices() ([]Device, error) { return p.out.devices() }

// SetDevice switches output to the given device id ("" = system default).
func (p *Player) SetDevice(id string) error { return p.out.setDevice(id) }

// CurrentDevice returns the selected device id ("" = default).
func (p *Player) CurrentDevice() string { return p.out.currentDevice() }

// ---- source pipeline ----

func newSource(plan *deezer.StreamPlan, durMS int64) *source {
	s := &source{
		plan:   plan,
		durMS:  durMS,
		format: plan.Format,
		sb:     newStreamBuffer(),
		ring:   newPCMRing(ringMax),
	}
	s.seekTo.Store(-1)
	return s
}

// download fetches the CDN body, decrypting BF_CBC_STRIPE chunks (encrypted
// streams) or passing through plain streams (podcasts), into the streamBuffer.
func (s *source) download() {
	req, err := http.NewRequest(http.MethodGet, s.plan.CDNURL, nil)
	if err != nil {
		s.setErr(err)
		s.sb.finish(err)
		return
	}
	// A browser User-Agent: Deezer's own CDN is permissive, but third-party
	// podcast hosts (e.g. Acast for direct-stream episodes) reject the default Go
	// agent with 403. http.DefaultClient follows the redirects those hosts use.
	req.Header.Set("User-Agent", streamUserAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.setErr(err)
		s.sb.finish(err)
		return
	}
	defer resp.Body.Close()
	odlog.Debug("stream %s: HTTP %d %s (%s)", s.plan.TrackID, resp.StatusCode, resp.Header.Get("Content-Type"), s.format)
	if resp.StatusCode != http.StatusOK {
		e := fmt.Errorf("CDN returned %s", resp.Status)
		s.setErr(e)
		s.sb.finish(e)
		return
	}
	if !s.plan.Encrypted {
		buf := make([]byte, 64*1024)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				s.sb.append(buf[:n])
			}
			if err != nil {
				if err != io.EOF {
					s.setErr(err)
				}
				s.sb.finish(eofToNil(err))
				return
			}
			if s.dead.Load() {
				s.sb.finish(nil)
				return
			}
		}
	}
	dec, err := deezer.NewStripeDecryptor(s.plan.TrackID)
	if err != nil {
		s.setErr(err)
		s.sb.finish(err)
		return
	}
	buf := make([]byte, 64*1024)
	var out []byte
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			out = dec.Feed(buf[:n], out[:0])
			s.sb.append(out)
		}
		if rerr != nil {
			out = dec.Finish(out[:0])
			if len(out) > 0 {
				s.sb.append(out)
			}
			if rerr != io.EOF {
				s.setErr(rerr)
			}
			s.sb.finish(eofToNil(rerr))
			return
		}
		if s.dead.Load() {
			s.sb.finish(nil)
			return
		}
	}
}

func eofToNil(err error) error {
	if err == io.EOF {
		return nil
	}
	return err
}

// decode builds the decoder from the streamBuffer and pumps PCM into the ring,
// honoring seek requests.
func (s *source) decode() {
	// Buffer the whole track before decoding. Decoding while still downloading
	// made MP3 playback choppy (the decoder outran the network); this matches
	// what the pre-malgo player did and what FLAC already did implicitly. The
	// download runs in parallel and is far faster than realtime, so the startup
	// wait is small; gapless preload still downloads the next track in advance.
	s.sb.waitDone()
	if s.dead.Load() {
		return
	}
	var dec pcmStream
	var err error
	if strings.Contains(strings.ToUpper(s.format), "FLAC") {
		dec, err = newFLACStream(s.sb)
	} else {
		var md *mp3.Decoder
		md, err = mp3.NewDecoder(s.sb)
		if err == nil {
			// Diagnostics: the device runs at 44100 stereo and the pipeline
			// assumes the decoder matches. A mismatch here (rate != 44100, or a
			// mono source) would make MP3 playback sound choppy/wrong while FLAC
			// (always 44100, mono duplicated to stereo) stays fine.
			odlog.Debug("mp3 decode: sampleRate=%d deviceRate=%d format=%s", md.SampleRate(), sampleRate, s.format)
			dec = md
		}
	}
	if err != nil {
		if s.lastErr() == "" {
			s.setErr(err)
		}
		s.eof.Store(true)
		return
	}
	buf := make([]byte, decodeChunk)
	seq := s.ring.seq()
	for {
		if s.dead.Load() {
			return
		}
		if to := s.seekTo.Swap(-1); to >= 0 {
			func() {
				defer func() { _ = recover() }()
				_, _ = dec.Seek(to, io.SeekStart)
			}()
			seq = s.ring.flush()
		}
		n, rerr := dec.Read(buf)
		if n > 0 {
			if !s.ring.write(buf[:n], seq) {
				// flushed (seek) or closed; refresh seq and continue/stop.
				if s.dead.Load() {
					return
				}
				seq = s.ring.seq()
			}
		}
		if rerr != nil {
			s.eof.Store(true)
			return
		}
	}
}

func (s *source) requestSeek(pcmOffset int64) { s.seekTo.Store(pcmOffset) }

func (s *source) kill() {
	s.dead.Store(true)
	s.sb.close()
	s.ring.close()
}

// ---- PCM helpers ----

// applyGain scales interleaved s16 samples in place by g (0..1).
func applyGain(b []byte, g float64) {
	if g >= 0.999 {
		return
	}
	for i := 0; i+1 < len(b); i += 2 {
		v := int16(uint16(b[i]) | uint16(b[i+1])<<8)
		v = int16(float64(v) * g)
		b[i] = byte(v)
		b[i+1] = byte(uint16(v) >> 8)
	}
}

// mixPCM writes src*g into dst (same length); dst and src may alias.
func mixPCM(dst, src []byte, g float64) {
	for i := 0; i+1 < len(src) && i+1 < len(dst); i += 2 {
		v := int16(uint16(src[i]) | uint16(src[i+1])<<8)
		v = int16(float64(v) * g)
		dst[i] = byte(v)
		dst[i+1] = byte(uint16(v) >> 8)
	}
}

// addPCM adds src into dst (saturating), in place.
func addPCM(dst, src []byte) {
	for i := 0; i+1 < len(src) && i+1 < len(dst); i += 2 {
		a := int32(int16(uint16(dst[i]) | uint16(dst[i+1])<<8))
		b := int32(int16(uint16(src[i]) | uint16(src[i+1])<<8))
		s := a + b
		if s > 32767 {
			s = 32767
		} else if s < -32768 {
			s = -32768
		}
		dst[i] = byte(int16(s))
		dst[i+1] = byte(uint16(int16(s)) >> 8)
	}
}
