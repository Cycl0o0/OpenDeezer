// Package audio is the malgo (miniaudio) playback engine: it streams, decrypts
// and decodes Deezer audio (MP3 + FLAC) into a PCM ring that a single output
// device drains. Supports seek, per-track ReplayGain, output-device selection,
// gapless transitions and (experimental) crossfade. In-memory only — nothing is
// written to disk.
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
	"github.com/gen2brain/malgo"
	"github.com/hajimehoshi/go-mp3"
)

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
	decodeChunk = 16 * 1024
)

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
	ctx *malgo.AllocatedContext

	mu         sync.Mutex // guards device + selectedID only
	device     *malgo.Device
	selectedID *malgo.DeviceID
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

// NewPlayer initializes the audio context + default output device.
func NewPlayer() (*Player, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("audio init: %w", err)
	}
	p := &Player{ctx: ctx, stopMgr: make(chan struct{})}
	p.state.Store(int32(Stopped))
	p.lastErr.Store("")
	p.format.Store("")
	p.gainFac.Store(math.Float64bits(1))
	p.gapless.Store(true)
	p.setVolume(1.0)
	if err := p.initDevice(nil); err != nil {
		_ = ctx.Uninit()
		ctx.Free()
		return nil, err
	}
	go p.manage()
	return p, nil
}

// initDevice (re)creates the playback device, optionally bound to deviceID.
func (p *Player) initDevice(deviceID *malgo.DeviceID) error {
	cfg := malgo.DefaultDeviceConfig(malgo.Playback)
	cfg.Playback.Format = malgo.FormatS16
	cfg.Playback.Channels = channels
	cfg.SampleRate = sampleRate
	// Use a large hardware period (~100ms × 4 ≈ 400ms total). The default period
	// is tiny (~10ms on CoreAudio), so a Go GC pause longer than that delays the
	// realtime callback and underruns the device — audible as choppy playback,
	// especially in the GUI/c-archive process where GC pressure is higher (the
	// idle TUI doesn't show it). A larger buffer coasts through GC pauses. The
	// extra output latency is irrelevant for a music player.
	cfg.Periods = 4
	cfg.PeriodSizeInMilliseconds = 100
	if deviceID != nil {
		cfg.Playback.DeviceID = deviceID.Pointer()
	}
	dev, err := malgo.InitDevice(p.ctx.Context, cfg, malgo.DeviceCallbacks{Data: p.onSamples})
	if err != nil {
		return fmt.Errorf("audio device: %w", err)
	}
	if err := dev.Start(); err != nil {
		dev.Uninit()
		return fmt.Errorf("audio device start: %w", err)
	}
	p.mu.Lock()
	old := p.device
	p.device = dev
	p.selectedID = deviceID
	p.mu.Unlock()
	if old != nil {
		old.Uninit()
	}
	return nil
}

// ---- the audio callback (runs on miniaudio's thread; must be fast) ----

func (p *Player) onSamples(out, _ []byte, _ uint32) {
	for i := range out {
		out[i] = 0
	}
	if State(p.state.Load()) != Playing {
		return
	}
	cur := p.cur.Load()
	next := p.next.Load()
	xfadeMS := p.crossfadeMS.Load()
	if cur == nil {
		return
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
}

// ---- volume ----

func (p *Player) setVolume(v float64) {
	if v < 0 {
		v = 0
	} else if v > 1 {
		v = 1
	}
	p.volume.Store(math.Float64bits(v))
}
func (p *Player) Volume() float64 { return math.Float64frombits(p.volume.Load()) }
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
	p.state.Store(int32(Playing))
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
	for {
		select {
		case <-p.stopMgr:
			return
		case <-ticker.C:
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

// Close tears down the device + context.
func (p *Player) Close() {
	p.mgrOnce.Do(func() { close(p.stopMgr) })
	p.stopSources()
	p.mu.Lock()
	dev := p.device
	p.device = nil
	p.mu.Unlock()
	if dev != nil {
		dev.Uninit()
	}
	if p.ctx != nil {
		_ = p.ctx.Uninit()
		p.ctx.Free()
	}
}

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
	resp, err := http.Get(s.plan.CDNURL)
	if err != nil {
		s.setErr(err)
		s.sb.finish(err)
		return
	}
	defer resp.Body.Close()
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
	var dec pcmStream
	var err error
	if strings.Contains(strings.ToUpper(s.format), "FLAC") {
		dec, err = newFLACStream(s.sb)
	} else {
		dec, err = mp3.NewDecoder(s.sb)
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
