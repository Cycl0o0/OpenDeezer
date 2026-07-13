// Package audio is the playback engine: it streams, decrypts and decodes Deezer
// audio (MP3 + FLAC) into a PCM ring that an output device drains. Supports seek,
// per-track ReplayGain, gapless transitions and (experimental) crossfade.
// In-memory by default; SetStreamCache attaches an opt-in on-disk cache of the
// raw (still-encrypted) CDN bytes for full tracks — decrypted audio is never
// written to disk.
//
// The output device is abstracted behind the `output` interface so the backend
// is build-tag-selected: malgo/miniaudio by default (adds output-device
// selection), or oto under the `otosink` tag — used for the macOS GUI, where
// malgo's CoreAudio callback runs unreliably inside the c-archive.
package audio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/deezer"
	odlog "github.com/Cycl0o0/OpenDeezer/v2/internal/log"
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
	// suspend releases (on=true) / restores (on=false) the OS output without
	// tearing down the backend context; safe to call from any goroutine.
	suspend(on bool) error
	// setLostHandler registers a callback the backend invokes when the selected
	// device disappears and cannot be recovered (no-op on backends that reroute
	// themselves, e.g. oto).
	setLostHandler(func(string))
	close()
	deviceDown() bool
	latencyFrames() int
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

	fadeInFrames  = sampleRate * 12 / 1000 // ~12ms anti-click ramp after a discontinuity
	fadeOutFrames = sampleRate * 12 / 1000 // ~12ms anti-click ramp before Pause/Stop/skip/seek
	sleepFadeNS   = int64(8 * time.Second) // sleep-timer fade-out window before pausing

	// HTTP Range resume tuning for a dropped/torn CDN download.
	maxResumeAttempts  = 3                      // consecutive failed re-GETs before giving up
	maxRefreshAttempts = 2                      // media-URL re-resolutions on 403/410 expiry
	resumeBackoff      = 300 * time.Millisecond // base backoff, multiplied by the attempt count
	// minResumeProgress is how many new bytes an attempt must deliver before the
	// retry budget resets. Resetting on ANY progress would let a host that drips
	// a byte per connection (and streamHTTPClient deliberately has no overall
	// Client.Timeout) keep the download looping forever; genuine long streams
	// with sporadic drops still clear this easily between failures.
	minResumeProgress = 64 * 1024
)

// streamUserAgent is sent when fetching audio so third-party podcast hosts
// (Acast etc.) don't reject the default Go agent with 403.
const streamUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"

// pcmStream is a decoder yielding interleaved s16 PCM, seekable by PCM byte
// offset. *mp3Stream, *flacStream and *resampleStream all satisfy it.
type pcmStream interface {
	io.Reader
	Seek(offset int64, whence int) (int64, error)
}

// ErrOffline is returned when attempting to play a track offline (empty CDN URL)
// but the track is not present in the local StreamCache.
var ErrOffline = errors.New("offline: track not cached and no CDN URL available")

// streamHTTPClient fetches audio bodies. It sets connect/TLS/response-header
// timeouts so a stalled handshake or dead server fails fast, but deliberately
// has no Client.Timeout — that would abort long (multi-minute) track/podcast
// streams. A stalled mid-body read is unblocked instead by cancelling the
// per-source context in kill().
var streamHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       90 * time.Second,
	},
}

// source is one track's pipeline: download+decrypt -> streamBuffer -> decoder ->
// pcmRing. The download and decode each run on their own goroutine.
type source struct {
	plan   *deezer.StreamPlan
	durMS  int64
	format string
	rgFac  float64 // ReplayGain amplitude factor (immutable; read from RT callback)
	// cache is the optional raw-stream cache handed down from the Player
	// (nil = off). Immutable once download() starts.
	cache   StreamCache
	sb      *streamBuffer
	ring    *pcmRing
	eof     atomic.Bool  // decoder reached end and ring will not grow
	seekTo  atomic.Int64 // pending PCM-byte seek target, or -1
	dead    atomic.Bool
	errMsg  atomic.Value // string: download/decode error, if any
	decoded atomic.Int64 // PCM bytes successfully decoded and written to the ring

	// xfadeConsumed counts bytes of this source's ring drained by the crossfade
	// path while it is the incoming ("next") track, so the swap can resume it at
	// the position already played instead of restarting from 0. RT-incremented.
	xfadeConsumed atomic.Int64

	// mu/cond let the decode goroutine park after decoder EOF and wake on a seek
	// or kill (so seeking back into a finished-decoding track still works).
	mu   sync.Mutex
	cond *sync.Cond

	// ctx is cancelled by kill() to unblock a stalled Body.Read in download().
	ctx    context.Context
	cancel context.CancelFunc
}

type finishCallback struct {
	fn func()
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
	onFinish    atomic.Pointer[finishCallback]

	// streamCache is the optional raw-stream cache (see SetStreamCache) handed
	// to each new source. Set once at startup, before playback; read without
	// synchronization when sources are created.
	streamCache StreamCache

	// fadeLeft is the number of frames still to ramp up (anti-click fade-in) after
	// a playback discontinuity (start / resume / seek). Touched only from the RT
	// callback and set (store) from control calls; a stale read just fades a hair
	// longer, which is harmless.
	fadeLeft atomic.Int64
	// fadeOut is the frames still to ramp DOWN in an anti-click fade-out armed by
	// Pause/Stop/Play(skip)/SeekMS; the RT callback plays it out so those
	// operations cut on a near-zero crossing instead of popping. fadeOutArmed
	// stays set from arm until the control call performs its deferred state
	// flip/source swap, keeping the output muted in the gap between the ramp
	// finishing and that flip. Both are touched from the RT callback and control
	// calls via atomics only — no locks or allocations on the realtime path.
	fadeOut      atomic.Int64
	fadeOutArmed atomic.Bool
	// mix is a reusable scratch buffer for the crossfade path so the realtime
	// callback never allocates. Accessed only from readPCM (single goroutine).
	mix []byte

	// Sleep timer (core-owned so every client shares one implementation).
	sleepArmed atomic.Bool
	sleepEOT   atomic.Bool   // end-of-track mode (else duration mode)
	sleepAtNS  atomic.Int64  // wall-clock deadline (UnixNano) for duration mode
	sleepGain  atomic.Uint64 // float64 bits: fade-out envelope, 1.0 when not fading

	// Equalizer + mono downmix (core-owned DSP; see eq.go). eqCfg is rebuilt and
	// swapped whole by the setters; eqPrev/eqZ are filter memory touched only
	// from the RT callback.
	eqCfg       atomic.Pointer[eqConfig]
	eqPrev      *eqConfig
	eqZ         [channels][eqBands][2]float64
	eqSaveMu    sync.Mutex
	eqSaveTimer *time.Timer

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
	if on {
		// Apply the current track's gain immediately, so enabling ReplayGain
		// mid-playback takes effect now instead of only on the next Play().
		if cur := p.cur.Load(); cur != nil && cur.plan != nil {
			p.gainFac.Store(math.Float64bits(dbToFactor(cur.plan.GainDB)))
		}
	} else {
		p.gainFac.Store(math.Float64bits(1))
	}
}
func (p *Player) ReplayGain() bool { return p.rgOn.Load() }

// volumeTaper maps a 0..1 slider position to a perceptual amplitude gain. Human
// loudness perception is roughly logarithmic, so a linear slider crams almost
// all usable range into the bottom (50% already sounds nearly full). A cubic
// taper spreads the range so the control feels natural in every client. The
// public 0..1 volume API is unchanged; only the applied gain is tapered.
func volumeTaper(v float64) float64 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 1
	}
	return v * v * v
}

// sleepGainClamped reads the sleep-timer fade envelope (1.0 when not fading),
// treating an uninitialized/garbage value as full volume.
func (p *Player) sleepGainClamped() float64 {
	sg := math.Float64frombits(p.sleepGain.Load())
	if sg < 0 || sg > 1 { // uninitialized (0 bits) or garbage -> full
		return 1
	}
	return sg
}

func (p *Player) effectiveVolume() float64 {
	f := math.Float64frombits(p.gainFac.Load())
	if f == 0 {
		f = 1
	}
	return volumeTaper(p.Volume()) * f * p.sleepGainClamped()
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

// ---- sleep timer ----

// SetSleepTimer arms the sleep timer. When endOfTrack is true the player pauses
// once the current track finishes (d is ignored); otherwise it fades out over
// the last few seconds and pauses after d elapses. d <= 0 with endOfTrack false
// cancels any armed timer.
func (p *Player) SetSleepTimer(d time.Duration, endOfTrack bool) {
	if !endOfTrack && d <= 0 {
		p.CancelSleepTimer()
		return
	}
	// Disarm first so a manage() tick can't observe armed with a half-updated
	// mode/deadline (e.g. armed && !EOT while sleepAtNS is still the old 0 from a
	// previous EOT arm), which would fire the expiry branch and pause instantly.
	p.sleepArmed.Store(false)
	p.sleepGain.Store(math.Float64bits(1))
	p.sleepEOT.Store(endOfTrack)
	if !endOfTrack {
		p.sleepAtNS.Store(time.Now().Add(d).UnixNano())
	} else {
		p.sleepAtNS.Store(0)
	}
	p.sleepArmed.Store(true) // arm last, once mode + deadline are consistent
}

// CancelSleepTimer disarms the sleep timer and restores full volume.
func (p *Player) CancelSleepTimer() { p.clearSleep(); p.sleepGain.Store(math.Float64bits(1)) }

func (p *Player) clearSleep() {
	p.sleepArmed.Store(false)
	p.sleepEOT.Store(false)
	p.sleepAtNS.Store(0)
}

// SleepActive reports whether a sleep timer is armed.
func (p *Player) SleepActive() bool { return p.sleepArmed.Load() }

// SleepEndOfTrack reports whether the armed timer is in end-of-track mode.
func (p *Player) SleepEndOfTrack() bool { return p.sleepArmed.Load() && p.sleepEOT.Load() }

// SleepRemainingMS returns the milliseconds until the timer fires: for duration
// mode the wall-clock remainder, for end-of-track mode the current track's
// remaining time. Returns 0 when no timer is armed.
func (p *Player) SleepRemainingMS() int64 {
	if !p.sleepArmed.Load() {
		return 0
	}
	if p.sleepEOT.Load() {
		if r := p.DurationMS() - p.PositionMS(); r > 0 {
			return r
		}
		return 0
	}
	if r := (p.sleepAtNS.Load() - time.Now().UnixNano()) / int64(time.Millisecond); r > 0 {
		return r
	}
	return 0
}

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
	p.sleepGain.Store(math.Float64bits(1))
	p.gapless.Store(true)
	p.setVolume(1.0)
	p.loadEQ()
	p.out.setLostHandler(p.onDeviceLost)
	if err := p.out.start(p.readPCM); err != nil {
		p.out.close()
		return nil, err
	}
	go p.manage()
	return p, nil
}

// onDeviceLost is invoked by the output backend when the selected device
// disappears and it could not fall back to the system default. Surfaces the
// failure so clients stop showing a frozen 'Playing' state.
func (p *Player) onDeviceLost(msg string) {
	p.lastErr.Store(msg)
	p.state.Store(int32(Errored))
}

// SetOutputSuspended releases (on=true) or restores (on=false) the OS audio
// output without tearing down playback state (decoder, ring, position). Use it
// on audio-focus loss/interruption (e.g. iOS): playback resumes where it left
// off. Safe to call from any goroutine.
func (p *Player) SetOutputSuspended(on bool) error { return p.out.suspend(on) }

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
	gainApplied := false

	// Crossfade: within the crossfade window of the end, with a next source
	// ready, mix in next (fading in) while cur fades out.
	if xfadeMS > 0 && next != nil {
		total := cur.durMS
		pos := p.played.Load() * 1000 / bytesPerSec
		if total > 0 && pos >= total-xfadeMS {
			// Reuse a scratch buffer instead of allocating on every RT callback.
			if cap(p.mix) < len(out) {
				p.mix = make([]byte, len(out))
			}
			mix := p.mix[:len(out)]
			for i := range mix {
				mix[i] = 0
			}
			m := next.ring.read(mix)
			// Remember how much of next we've already played so the swap resumes
			// it here instead of restarting (which double-books crossfadeMS).
			next.xfadeConsumed.Add(int64(m))
			fade := float64(pos-(total-xfadeMS)) / float64(xfadeMS)
			if fade < 0 {
				fade = 0
			} else if fade > 1 {
				fade = 1
			}
			// Apply each track's own ReplayGain inside the window (the swap no
			// longer switches gains, which stepped the level between tracks whose
			// gains differ); the shared volume/sleep gain is folded in too.
			vs := volumeTaper(p.Volume()) * p.sleepGainClamped()
			curG, nextG := vs, vs
			if p.rgOn.Load() {
				curG *= cur.rgFac
				nextG *= next.rgFac
			}
			mixPCM(out[:n], out[:n], curG*(1-fade))
			mixPCM(mix[:m], mix[:m], nextG*fade)
			addPCM(out, mix)
			if m > n {
				n = m
			}
			gainApplied = true
		}
	}

	if !gainApplied {
		applyGain(out[:n], p.effectiveVolume())
	}
	// Equalizer + mono downmix (no-op when both are off). Linear DSP commutes
	// with the scalar gains above; runs before the fade-in ramp so the ramp
	// shapes the final signal.
	p.applyEQ(out[:n])
	// Anti-click: ramp the first few ms up after a discontinuity (start / resume /
	// seek) so the cut into a fresh waveform doesn't pop.
	if fl := p.fadeLeft.Load(); fl > 0 {
		p.fadeLeft.Store(applyFadeIn(out[:n], fl))
	}
	// Anti-click (out): a control op (Pause/Stop/skip/seek) armed a down-ramp and
	// is blocked waiting for us to play it out before it flips state/swaps the
	// source. Ramp toward zero; once the ramp is spent keep muting so no
	// full-volume PCM leaks in the gap before that flip.
	if p.fadeOutArmed.Load() {
		if fo := p.fadeOut.Load(); fo > 0 {
			p.fadeOut.Store(applyFadeOut(out[:n], fo))
		} else {
			for i := 0; i < n; i++ {
				out[i] = 0
			}
		}
	}
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

func (p *Player) Format() string { s, _ := p.format.Load().(string); return s }
func (p *Player) SetOnFinish(fn func()) {
	if fn == nil {
		p.onFinish.Store(nil)
	} else {
		p.onFinish.Store(&finishCallback{fn: fn})
	}
}
func (p *Player) State() State      { return State(p.state.Load()) }
func (p *Player) LastError() string { s, _ := p.lastErr.Load().(string); return s }
func (p *Player) PositionMS() int64 {
	played := p.played.Load()
	var latencyBytes int64
	if p.out != nil {
		latencyBytes = int64(p.out.latencyFrames() * frameBytes)
	}
	bytes := played - latencyBytes
	if bytes < 0 {
		bytes = 0
	}
	return bytes * 1000 / bytesPerSec
}
func (p *Player) DurationMS() int64 { return p.totalMS.Load() }

// IsPreview reports whether the current track is Deezer's 30-second preview
// (the free-account fallback) rather than the full, entitled stream.
func (p *Player) IsPreview() bool {
	if cur := p.cur.Load(); cur != nil && cur.plan != nil {
		return cur.plan.Preview
	}
	return false
}

// ---- playback ----

// fadeOutAndWait arms an anti-click down-ramp and blocks briefly (off the RT
// path) until the callback has played it out, so the caller's Pause/Stop/skip/
// seek cuts on a near-zero crossing instead of popping. It's a no-op when not
// actively Playing (no callback is draining the ring to ramp). The wait is
// bounded so a starved/silent output can't wedge the control call. The caller
// must clear the fade-out state (endFadeOut, or a Resume/Play that re-arms
// fade-in) after performing its deferred flip.
func (p *Player) fadeOutAndWait() {
	if State(p.state.Load()) != Playing {
		return
	}
	// Publish the frame count BEFORE arming. Go atomics are sequentially
	// consistent, so a callback that observes fadeOutArmed==true is guaranteed to
	// see fadeOut==fadeOutFrames and ramp. The reverse order let the callback
	// observe armed with fadeOut still 0 and hard-zero a full-volume buffer —
	// exactly the discontinuity this fade exists to prevent.
	p.fadeOut.Store(fadeOutFrames)
	p.fadeOutArmed.Store(true)
	deadline := time.Now().Add(250 * time.Millisecond)
	for p.fadeOut.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
}

// endFadeOut clears the fade-out state so normal full-volume playback resumes.
func (p *Player) endFadeOut() {
	p.fadeOut.Store(0)
	p.fadeOutArmed.Store(false)
}

// Play starts a track immediately, replacing anything current.
func (p *Player) Play(plan *deezer.StreamPlan, durationMS int64) error {
	if p.out.deviceDown() {
		if err := p.out.setDevice(p.out.currentDevice()); err != nil {
			p.state.Store(int32(Errored))
			p.lastErr.Store(err.Error())
			return err
		}
	}
	p.fadeOutAndWait() // ramp the outgoing track out before the swap (anti-click)
	p.stopSources()
	p.state.Store(int32(Loading))
	p.lastErr.Store("")
	p.played.Store(0)
	p.totalMS.Store(durationMS)
	p.format.Store(plan.Format)
	p.endFadeOut()                 // clear the fade-out; the fresh track fades in below
	p.fadeLeft.Store(fadeInFrames) // anti-click ramp on the fresh track
	if p.rgOn.Load() {
		p.gainFac.Store(math.Float64bits(dbToFactor(plan.GainDB)))
	} else {
		p.gainFac.Store(math.Float64bits(1))
	}

	src := newSource(plan, durationMS)
	src.cache = p.streamCache
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
	src.cache = p.streamCache
	go src.download()
	go src.decode()
	if old := p.next.Swap(src); old != nil {
		old.kill()
	}
}

// ClearPreload discards any preloaded next source. Call when the upcoming track
// is no longer determined (e.g. shuffle/repeat was toggled after a linear-next
// was preloaded) so a stale preload can't be gaplessly swapped in.
func (p *Player) ClearPreload() {
	if old := p.next.Swap(nil); old != nil {
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
			// Sleep timer (duration mode): fade out over the last few seconds, then
			// pause exactly at the deadline. End-of-track mode is handled in the
			// finish branch below.
			if p.sleepArmed.Load() && !p.sleepEOT.Load() {
				remain := p.sleepAtNS.Load() - time.Now().UnixNano()
				switch {
				case remain <= 0:
					if p.State() == Playing {
						p.state.Store(int32(Paused))
					}
					p.clearSleep()
					p.sleepGain.Store(math.Float64bits(1))
				case remain <= sleepFadeNS:
					p.sleepGain.Store(math.Float64bits(float64(remain) / float64(sleepFadeNS)))
				}
			}
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
			// Finished = decoder hit EOF, the ring has drained, AND no seek is
			// pending. The seekTo guard closes a race with tail seeks: SeekMS
			// flushes the ring synchronously (buffered()==0 at once) but the
			// parked decode goroutine only clears eof after it wakes — without
			// the guard a tick landing in that window would skip the track,
			// discarding the seek.
			if cur.eof.Load() && cur.ring.buffered() == 0 && cur.seekTo.Load() < 0 {
				if cur.lastErr() != "" && cur.decoded.Load() == 0 {
					p.lastErr.Store(cur.lastErr())
					if next != nil {
						if !p.next.CompareAndSwap(next, nil) {
							continue
						}
					}
					if !p.cur.CompareAndSwap(cur, nil) {
						if next != nil {
							next.kill()
						}
						continue
					}
					if next != nil {
						next.kill()
					}
					cur.kill()
					p.state.Store(int32(Errored))
					if cb := p.onFinish.Load(); cb != nil && cb.fn != nil {
						cb.fn()
					}
					continue
				}
				if e := cur.lastErr(); e != "" {
					p.lastErr.Store(e)
				}
				// End-of-track sleep timer: stop after this track finishes and do
				// not advance (don't fire onFinish, so the engine won't auto-next).
				if p.sleepArmed.Load() && p.sleepEOT.Load() {
					if next != nil {
						if !p.next.CompareAndSwap(next, nil) {
							continue
						}
					}
					if !p.cur.CompareAndSwap(cur, nil) {
						if next != nil {
							next.kill()
						}
						continue
					}
					if next != nil {
						next.kill()
					}
					cur.kill()
					p.state.Store(int32(Stopped))
					p.clearSleep()
					continue
				}
				if next != nil {
					// Seamless swap to the preloaded next track. Use CAS so a
					// concurrent Play/Stop/ClearPreload that swapped these pointers
					// (and killed the sources) can't be clobbered: claim next, then
					// install it as cur only if cur is unchanged.
					if !p.next.CompareAndSwap(next, nil) {
						continue // preload was cleared/replaced under us
					}
					if !p.cur.CompareAndSwap(cur, next) {
						next.kill() // we own next now; cur changed, so release it
						continue
					}
					cur.kill()
					// Re-verify p.cur == next before writing metadata and calling onFinish.
					if p.cur.Load() == next {
						// Crossfade already played next.xfadeConsumed bytes at real time;
						// resume from there (0 for a plain gapless transition).
						p.played.Store(next.xfadeConsumed.Load())
						p.totalMS.Store(next.durMS)
						p.format.Store(next.format)
						// Recompute the per-track ReplayGain for the swapped-in track;
						// otherwise it would keep playing at the previous track's gain.
						if p.rgOn.Load() && next.plan != nil {
							p.gainFac.Store(math.Float64bits(dbToFactor(next.plan.GainDB)))
						} else {
							p.gainFac.Store(math.Float64bits(1))
						}
						if cb := p.onFinish.Load(); cb != nil && cb.fn != nil {
							cb.fn()
						}
					}
				} else {
					// No preload: finish. Guard against a concurrent Play/Stop that
					// swapped in a new cur so we don't wedge it into Stopped.
					if !p.cur.CompareAndSwap(cur, nil) {
						continue
					}
					cur.kill() // release the decode goroutine parked after EOF
					p.state.Store(int32(Stopped))
					if cb := p.onFinish.Load(); cb != nil && cb.fn != nil {
						cb.fn()
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
	// Ramp the pre-seek audio out first (anti-click) so the jump doesn't pop, then
	// set the seek target and flush the ring synchronously so pre-seek PCM stops
	// playing immediately (the decode goroutine's own flush can lag by a device
	// period while it's blocked in ring.write). requestSeek before flush so the
	// decoder re-checks the target instead of refilling the flushed ring with
	// pre-seek PCM. The ring is now empty, so fadeLeft is spent on the first
	// post-seek PCM (the real discontinuity), not on stale audio.
	p.fadeOutAndWait()
	cur.requestSeek(off)
	cur.ring.flush()
	p.played.Store(off)
	p.endFadeOut()
	p.fadeLeft.Store(fadeInFrames) // anti-click ramp on the first post-seek PCM
}

func (p *Player) Pause() {
	if p.State() == Playing {
		p.fadeOutAndWait() // anti-click ramp before going silent
		p.state.Store(int32(Paused))
		// Leave fadeOut/fadeOutArmed set: state is now Paused so the callback
		// returns silence anyway; Resume clears them and re-arms the fade-in.
	}
}
func (p *Player) Resume() {
	state := p.State()
	if state == Paused || state == Errored {
		if p.out.deviceDown() {
			if err := p.out.setDevice(p.out.currentDevice()); err != nil {
				p.state.Store(int32(Errored))
				p.lastErr.Store(err.Error())
				return
			}
		}
		p.endFadeOut()
		p.fadeLeft.Store(fadeInFrames) // anti-click ramp on resume
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
	p.fadeOutAndWait() // anti-click ramp before the hard stop
	p.stopSources()
	p.state.Store(int32(Stopped))
	p.endFadeOut()
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
	// Flush a pending debounced EQ save so a change made just before quit
	// isn't lost.
	p.eqSaveMu.Lock()
	pending := p.eqSaveTimer != nil && p.eqSaveTimer.Stop()
	p.eqSaveMu.Unlock()
	if pending {
		p.saveEQNow()
	}
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
		rgFac:  dbToFactor(plan.GainDB),
		sb:     newStreamBuffer(),
		ring:   newPCMRing(ringMax),
	}
	if !plan.Encrypted {
		if err := s.sb.enableDiskSpill(); err != nil {
			odlog.Error("failed to enable disk spill for plain stream: %v", err)
		}
	}
	s.cond = sync.NewCond(&s.mu)
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.seekTo.Store(-1)
	return s
}

// download fetches the CDN body, decrypting BF_CBC_STRIPE chunks (encrypted
// streams) or passing through plain streams (podcasts), into the streamBuffer.
//
// It is resilient to a torn mid-body transfer: because the stripe cipher is a
// byte-for-byte, chunk-stateless transform, a dropped connection is recovered by
// re-issuing the GET with a Range header at the exact consumed offset and
// continuing to feed the SAME decryptor — the streamBuffer just keeps appending
// where it left off, so the decoded result is identical to an uninterrupted
// download. If the signed media URL has expired (403/410), it re-resolves a
// fresh one via plan.Refresh (when available) and resumes from the same offset.
//
// When a StreamCache is attached (SetStreamCache) and the plan is a full
// encrypted track, the raw ciphertext is served from / mirrored into the
// cache: a hit feeds the cached bytes through the same decrypt path with no
// HTTP (or Refresh) at all, and a miss tees only the clean offset-0 response
// body into the cache while it streams. Resumed (Range) and ignored-Range
// discard bodies are never teed — an interrupted tee discards itself, so that
// play simply isn't cached.
func (s *source) download() {
	var dec *deezer.StripeDecryptor
	if s.plan.Encrypted {
		var err error
		dec, err = deezer.NewStripeDecryptor(s.plan.TrackID)
		if err != nil {
			s.setErr(err)
			s.sb.finish(err)
			return
		}
	}

	url := s.plan.CDNURL
	buf := make([]byte, 64*1024)
	var out []byte     // reused decrypt output scratch
	var consumed int64 // ciphertext bytes appended/fed so far (== Range resume offset)
	attempts := 0      // consecutive failed re-GETs without progress
	refreshes := 0     // media-URL re-resolutions performed
	lenSet := false    // stream length recorded once
	// validator is the first response's entity validator (ETag, else
	// Last-Modified). Resume GETs send it as If-Range so a host whose entity
	// changed mid-download answers 200-full instead of a 206 we would blindly
	// splice; a 200-with-progress whose validator differs is fatal (below).
	// Mainly matters for plain passthrough/podcast hosts — encrypted Deezer CDN
	// tracks are immutable.
	validator := ""
	validatorSet := false

	// Raw-stream cache: only full encrypted tracks are cacheable, so the bytes
	// at rest stay ciphertext (previews and plain passthrough streams — e.g.
	// podcasts — are never cached).
	var cacheKey string
	if s.cache != nil && s.plan.Encrypted && !s.plan.Preview {
		cacheKey = s.plan.TrackID + "." + s.plan.Format
	}

	// Cache hit: feed the cached ciphertext through the normal decrypt path,
	// skipping HTTP entirely. A mid-read error on the cached file falls through
	// to the HTTP path below, which resumes from the ciphertext offset already
	// fed (consumed tracks dec.Consumed()) — exactly like a torn network
	// download.
	if cacheKey != "" {
		if rc, size, ok := s.cache.Get(cacheKey); ok {
			if size > 0 {
				s.sb.setContentLength(size) // preallocate from the cached size
				lenSet = true
			}
			completed, rerr := s.pump(rc, buf, &out, dec, &consumed, 0)
			rc.Close()
			if completed {
				s.finishDownload(dec, out)
				return
			}
			if s.dead.Load() {
				s.sb.finish(nil)
				return
			}
			if s.plan.CDNURL == "" {
				err := rerr
				if err == nil {
					err = io.ErrUnexpectedEOF
				}
				s.setErr(err)
				s.sb.finish(err)
				return
			}
			odlog.Debug("stream %s: cache read failed at %d (%v); falling back to HTTP",
				s.plan.TrackID, consumed, rerr)
		} else if s.plan.CDNURL == "" {
			s.setErr(ErrOffline)
			s.sb.finish(ErrOffline)
			return
		}
	} else if s.plan.CDNURL == "" {
		s.setErr(ErrOffline)
		s.sb.finish(ErrOffline)
		return
	}

	for {
		if s.dead.Load() {
			s.sb.finish(nil)
			return
		}
		startOff := consumed
		resp, err := s.openStream(url, consumed, validator)
		if err != nil {
			if s.dead.Load() {
				s.sb.finish(nil)
				return
			}
			attempts++
			if attempts > maxResumeAttempts {
				s.setErr(err)
				s.sb.finish(err)
				return
			}
			s.backoff(attempts)
			continue
		}
		switch {
		case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusGone:
			// Expired signed URL: re-resolve and retry from the same offset.
			resp.Body.Close()
			if u2 := s.refreshURL(&refreshes); u2 != "" {
				url = u2
				continue
			}
			e := fmt.Errorf("CDN returned %s", resp.Status)
			s.setErr(e)
			s.sb.finish(e)
			return
		case resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent:
			resp.Body.Close()
			e := fmt.Errorf("CDN returned %s", resp.Status)
			s.setErr(e)
			s.sb.finish(e)
			return
		}
		// Resume sanity: a 206 must continue exactly at the consumed offset —
		// splicing a body that starts anywhere else corrupts the stream. Treat a
		// mismatched (or unparsable) Content-Range start as a failed attempt.
		if resp.StatusCode == http.StatusPartialContent && consumed > 0 {
			if start := contentRangeStart(resp); start != consumed {
				resp.Body.Close()
				attempts++
				if attempts > maxResumeAttempts {
					e := fmt.Errorf("CDN resume at %d answered Content-Range start %d", consumed, start)
					s.setErr(e)
					s.sb.finish(e)
					return
				}
				s.backoff(attempts)
				continue
			}
		}
		if !lenSet {
			if total := totalLength(resp); total > 0 {
				s.sb.setContentLength(total)
			}
			lenSet = true
		}
		if !validatorSet {
			validator = responseValidator(resp)
			validatorSet = true
		}
		// If we asked for a Range but the server ignored it (answered 200 full),
		// skip the bytes already consumed so we don't double-feed the decryptor —
		// but only when the entity is provably the same one we started on. A
		// stored validator that no longer matches means the host swapped the
		// entity mid-download (the If-Range miss case): splicing would feed
		// mismatched bytes, so fail cleanly instead.
		var skip int64
		if consumed > 0 && resp.StatusCode == http.StatusOK {
			if validator != "" && responseValidator(resp) != validator {
				resp.Body.Close()
				e := fmt.Errorf("stream entity changed during resume (validator mismatch)")
				s.setErr(e)
				s.sb.finish(e)
				return
			}
			skip = consumed
		}
		// Cache fill: tee only a clean offset-0 body (nothing consumed yet, no
		// Range resume, no ignored-Range discard) so a committed entry is exactly
		// the full ciphertext the CDN served. The tee discards its partial entry
		// on any read error, so an interrupted first download leaves no cache
		// entry — that play just isn't cached.
		body := io.Reader(resp.Body)
		teeing := false
		if cacheKey != "" && consumed == 0 && resp.StatusCode == http.StatusOK {
			body = s.cache.TeeReader(cacheKey, resp.Body)
			teeing = body != io.Reader(resp.Body)
		}
		completed, rerr := s.pump(body, buf, &out, dec, &consumed, skip)
		resp.Body.Close()
		if teeing && !completed && rerr == nil {
			// pump bailed out on kill() without a read error, so the tee never
			// observed a failure and would leave its pending entry open. Poke it
			// once against the now-closed body so it errors and discards.
			_, _ = body.Read(buf[:1])
		}
		if completed {
			s.finishDownload(dec, out)
			return
		}
		if s.dead.Load() {
			s.sb.finish(nil)
			return
		}
		// Torn mid-body: resume. Meaningful progress refreshes the retry budget so
		// a long stream with sporadic drops isn't capped at a handful of total
		// tries — but only past minResumeProgress, so a host dripping a byte per
		// connection exhausts the budget instead of looping forever.
		if consumed-startOff >= minResumeProgress {
			attempts = 0
		}
		attempts++
		if attempts > maxResumeAttempts {
			if rerr != nil && !s.dead.Load() {
				s.setErr(rerr)
			}
			s.sb.finish(eofToNil(rerr))
			return
		}
		s.backoff(attempts)
	}
}

// finishDownload flushes the decryptor's trailing partial chunk (encrypted
// streams; always plaintext) and marks the streamBuffer complete. Shared by
// the cache-hit and HTTP completions of download(). out is the reusable
// decrypt scratch.
func (s *source) finishDownload(dec *deezer.StripeDecryptor, out []byte) {
	if dec != nil {
		out = dec.Finish(out[:0])
		if len(out) > 0 {
			s.sb.append(out)
		}
	}
	s.sb.finish(nil)
}

// openStream issues the CDN GET, resuming from offset via a Range header when
// offset > 0. validator (the first response's ETag/Last-Modified, may be "") is
// sent as If-Range on resumes so a host whose entity changed answers 200-full —
// which download() then validates — instead of a 206 of the wrong entity. The
// per-source context cancels a stalled read on kill().
func (s *source) openStream(url string, offset int64, validator string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// A browser User-Agent: Deezer's own CDN is permissive, but third-party
	// podcast hosts (e.g. Acast for direct-stream episodes) reject the default Go
	// agent with 403. streamHTTPClient follows the redirects those hosts use and
	// cancels (via s.ctx) when the source is killed, unblocking a stalled read.
	req.Header.Set("User-Agent", streamUserAgent)
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		if validator != "" {
			req.Header.Set("If-Range", validator)
		}
	}
	resp, err := streamHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	odlog.Debug("stream %s: HTTP %d %s (%s) off=%d", s.plan.TrackID, resp.StatusCode,
		resp.Header.Get("Content-Type"), s.format, offset)
	return resp, nil
}

// pump streams one response body into the streamBuffer, decrypting (dec != nil)
// or passing through (dec == nil). It advances *consumed to the ciphertext
// offset for a possible resume. skip discards leading bytes already consumed
// (when the server ignored a Range request and re-sent from the start). Returns
// completed=true on a clean io.EOF; on any other read error returns
// completed=false with the error so the caller can resume.
func (s *source) pump(body io.Reader, buf []byte, out *[]byte, dec *deezer.StripeDecryptor, consumed *int64, skip int64) (completed bool, err error) {
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			b := buf[:n]
			if skip > 0 {
				d := skip
				if d > int64(len(b)) {
					d = int64(len(b))
				}
				b = b[d:]
				skip -= d
			}
			if len(b) > 0 {
				if dec != nil {
					*out = dec.Feed(b, (*out)[:0])
					s.sb.append(*out)
					*consumed = dec.Consumed()
				} else {
					s.sb.append(b)
					*consumed += int64(len(b))
				}
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				if s.sb.Total() > 0 && *consumed < s.sb.Total() {
					return false, io.ErrUnexpectedEOF
				}
				return true, nil
			}
			return false, rerr
		}
		if s.dead.Load() {
			return false, nil
		}
	}
}

// refreshURL re-resolves a fresh CDN URL via plan.Refresh when the current one
// has expired (403/410). Returns "" when refresh is unavailable or exhausted,
// or when the refreshed plan is not the same stream: the caller resumes the
// download at the already-consumed ciphertext offset with the SAME decryptor,
// which is only valid if the refreshed plan has the identical format /
// encryption / preview identity. A plan that changed (quality switched at
// runtime, preview fallback) would splice mismatched bytes — corrupt audio,
// 416s, and a poisoned StreamCache entry — so it is rejected and download()
// falls through to its clean error path.
func (s *source) refreshURL(count *int) string {
	if s.plan.Refresh == nil || *count >= maxRefreshAttempts {
		return ""
	}
	*count++
	np, err := s.plan.Refresh()
	if err != nil || np == nil || np.CDNURL == "" ||
		np.Format != s.plan.Format || np.Encrypted != s.plan.Encrypted || np.Preview != s.plan.Preview {
		return ""
	}
	return np.CDNURL
}

// backoff sleeps a short, attempt-scaled interval before a resume, waking early
// if the source is killed.
func (s *source) backoff(attempt int) {
	t := time.NewTimer(resumeBackoff * time.Duration(attempt))
	defer t.Stop()
	select {
	case <-s.ctx.Done():
	case <-t.C:
	}
}

// totalLength extracts the full stream length from a response: the number after
// the slash in Content-Range for a 206, else Content-Length for a full 200.
func totalLength(resp *http.Response) int64 {
	if resp.StatusCode == http.StatusPartialContent {
		cr := resp.Header.Get("Content-Range")
		if i := strings.LastIndex(cr, "/"); i >= 0 {
			if v, err := strconv.ParseInt(strings.TrimSpace(cr[i+1:]), 10, 64); err == nil && v > 0 {
				return v
			}
		}
		return 0
	}
	if resp.ContentLength > 0 {
		return resp.ContentLength
	}
	return 0
}

// contentRangeStart parses the start offset from a 206's Content-Range header
// ("bytes <start>-<end>/<total>"). Returns -1 when absent or unparsable, so a
// resume can't silently splice a body whose position is unknown.
func contentRangeStart(resp *http.Response) int64 {
	cr := strings.TrimSpace(resp.Header.Get("Content-Range"))
	cr = strings.TrimSpace(strings.TrimPrefix(cr, "bytes"))
	dash := strings.IndexByte(cr, '-')
	if dash < 0 {
		return -1
	}
	v, err := strconv.ParseInt(strings.TrimSpace(cr[:dash]), 10, 64)
	if err != nil || v < 0 {
		return -1
	}
	return v
}

// responseValidator returns the response's entity validator for resume checks:
// ETag when present, else Last-Modified, else "".
func responseValidator(resp *http.Response) string {
	if et := resp.Header.Get("ETag"); et != "" {
		return et
	}
	return resp.Header.Get("Last-Modified")
}

func eofToNil(err error) error {
	if err == io.EOF {
		return nil
	}
	return err
}

// watermarkBytes estimates a safe head-start of encoded audio (~15s) to buffer
// before decoding, from the format's nominal bitrate. Starting on this margin
// instead of the whole file cuts time-to-first-audio; streamBuffer's blocking
// Read then paces the decoder as the rest streams in. Floored at 256KB so tiny
// or unknown-bitrate streams still have a cushion.
func watermarkBytes(format string) int {
	up := strings.ToUpper(format)
	kbps := 128
	switch {
	case strings.Contains(up, "FLAC"):
		kbps = 1024 // 16-bit/44.1k stereo FLAC compresses to roughly this
	case strings.Contains(up, "320"):
		kbps = 320
	case strings.Contains(up, "256"):
		kbps = 256
	case strings.Contains(up, "64"):
		kbps = 64
	}
	const seconds = 15
	n := kbps * 1000 / 8 * seconds
	if min := 256 * 1024; n < min {
		n = min
	}
	return n
}

// decode builds the decoder from the streamBuffer and pumps PCM into the ring,
// honoring seek requests.
func (s *source) decode() {
	// Start on a watermark of encoded audio rather than the whole track, so the
	// first sample plays sooner. The ring blocks the decoder when full (pacing it)
	// and the download runs in parallel and is usually far faster than realtime,
	// so the decoder rarely outruns the network; when it does, streamBuffer's
	// blocking Read simply parks it until more arrives.
	s.sb.waitWatermark(watermarkBytes(s.format))
	if s.dead.Load() {
		return
	}
	var dec pcmStream
	var err error
	if strings.Contains(strings.ToUpper(s.format), "FLAC") {
		// flac.NewSeek reads only the leading metadata (no full-download scan), so
		// FLAC decodes progressively from the streamBuffer. Seeking builds the seek
		// table lazily: if the FLAC has no SEEKTABLE block that scan reads to EOF,
		// which streamBuffer turns into a block-until-downloaded — a graceful
		// degradation, not a failure.
		dec, err = newFLACStream(s.sb)
	} else {
		var ms *mp3Stream
		ms, err = newMP3Stream(s.sb)
		if err == nil {
			// go-mp3 always emits 2ch s16 but at the FILE's native rate, while the
			// output device is fixed at 44100. Deezer tracks are 44.1k, but the
			// plain-stream path also serves third-party podcast MP3s (48k/24k/22.05k);
			// left unresampled they'd play at the wrong speed and pitch. Wrap the
			// decoder in a linear resampler when the rate differs.
			if r := ms.SampleRate(); r != sampleRate {
				odlog.Debug("mp3 decode: resampling %dHz -> %dHz format=%s", r, sampleRate, s.format)
				dec = newResampleStream(ms, r)
			} else {
				dec = ms
			}
		}
	}
	if err != nil {
		if s.lastErr() == "" {
			s.setErr(err)
		}
		s.eof.Store(true)
		// There is no decoder to service seeks, and manage()'s finish check
		// treats a pending seek as "not finished" — so park here swallowing any
		// seek requests until the source is killed. Returning immediately
		// instead would let a SeekMS on this errored source leave seekTo set
		// forever, wedging manage() out of ever finishing/advancing.
		for s.waitSeekOrDead() {
			s.seekTo.Store(-1)
		}
		return
	}
	buf := make([]byte, decodeChunk)
	seq := s.ring.seq()
	for {
		if s.dead.Load() {
			return
		}
		if s.seekTo.Load() >= 0 {
			// Clear eof BEFORE consuming the seek target: manage()'s finish check
			// is `eof && ring empty && no pending seek`, so the "not finished"
			// signal must hand off from seekTo to eof without a gap. Swapping
			// seekTo first would open a window (seekTo=-1, eof still true) in
			// which manage() could finish and skip the track.
			s.eof.Store(false) // seeking back into the track: PCM will grow again
			if to := s.seekTo.Swap(-1); to >= 0 {
				func() {
					defer func() { _ = recover() }()
					_, _ = dec.Seek(to, io.SeekStart)
				}()
				seq = s.ring.flush()
			}
		}
		n, rerr := dec.Read(buf)
		if n > 0 {
			if !s.ring.write(buf[:n], seq) {
				// The ring was flushed/closed under us (a seek, from here or from
				// SeekMS). Drop this chunk and loop so a pending seek is re-checked
				// at the top before we decode more — otherwise pre-seek PCM decoded
				// before the Seek executes could be written into the flushed ring.
				if s.dead.Load() {
					return
				}
				seq = s.ring.seq()
				continue
			}
			s.decoded.Add(int64(n))
		}
		if rerr != nil {
			// Decoder EOF, but the source is still alive: the ring may hold several
			// seconds of PCM and the user can still seek back into the track. Mark
			// eof (so manage() finishes once the ring drains) and park until a seek
			// or kill wakes us, then resume decoding.
			s.eof.Store(true)
			if !s.waitSeekOrDead() {
				return
			}
		}
	}
}

// waitSeekOrDead blocks until a seek is requested or the source is killed.
// Returns true if a seek is pending, false if the source is dead.
func (s *source) waitSeekOrDead() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.seekTo.Load() < 0 && !s.dead.Load() {
		s.cond.Wait()
	}
	return s.seekTo.Load() >= 0
}

func (s *source) requestSeek(pcmOffset int64) {
	s.seekTo.Store(pcmOffset)
	s.mu.Lock()
	s.cond.Broadcast() // wake the decode goroutine if it parked after EOF
	s.mu.Unlock()
}

func (s *source) kill() {
	s.dead.Store(true)
	s.eof.Store(true) // a killed source is finished: never let manage() wait on it
	s.cancel()        // unblock a stalled Body.Read in download()
	s.mu.Lock()
	s.cond.Broadcast() // wake the decode goroutine if it parked after EOF
	s.mu.Unlock()
	s.sb.close()
	s.ring.close()
}

func init() {
	// Compile-time or init-time endianness check that panics on big-endian platforms.
	// Since all supported targets are little-endian and we cast []byte to []int16,
	// big-endian platforms are not supported.
	var x uint16 = 0x0001
	p := unsafe.Pointer(&x)
	if *(*byte)(p) == 0x00 {
		panic("big-endian platform not supported by internal/audio")
	}
}

// pcm16 casts a little-endian byte slice to an int16 slice in-place using unsafe.Slice.
// It assumes a little-endian host architecture (which is verified at init time).
// If the input slice length is less than 2, it returns nil.
// If the input slice length is odd, the trailing odd byte is ignored and left untouched.
func pcm16(b []byte) []int16 {
	if len(b) < 2 {
		return nil
	}
	return unsafe.Slice((*int16)(unsafe.Pointer(&b[0])), len(b)/2)
}

// ---- PCM helpers ----

// applyGain scales interleaved s16 samples in place by g (0..1).
func applyGain(b []byte, g float64) {
	if g >= 0.999 {
		return
	}
	samples := pcm16(b)
	for i := range samples {
		samples[i] = int16(float64(samples[i]) * g)
	}
}

// applyFadeIn ramps interleaved s16 stereo frames up to unity over fadeInFrames,
// starting from `remaining` frames left, and returns the frames still to ramp.
// Runs in the RT callback; no allocation.
func applyFadeIn(b []byte, remaining int64) int64 {
	samples := pcm16(b)
	numFrames := len(samples) / channels
	for i := 0; i < numFrames && remaining > 0; i++ {
		g := float64(fadeInFrames-remaining) / float64(fadeInFrames)
		if g < 0 {
			g = 0
		} else if g > 1 {
			g = 1
		}
		off := i * channels
		for c := 0; c < channels; c++ {
			idx := off + c
			samples[idx] = int16(float64(samples[idx]) * g)
		}
		remaining--
	}
	return remaining
}

// applyFadeOut ramps interleaved s16 stereo frames DOWN from unity toward zero
// over fadeOutFrames, starting from `remaining` frames left, and returns the
// frames still to ramp. When the ramp completes partway through the buffer the
// tail is zeroed (the discontinuity has happened). Runs in the RT callback; no
// allocation.
func applyFadeOut(b []byte, remaining int64) int64 {
	samples := pcm16(b)
	numFrames := len(samples) / channels
	i := 0
	for ; i < numFrames && remaining > 0; i++ {
		g := float64(remaining) / float64(fadeOutFrames)
		if g < 0 {
			g = 0
		} else if g > 1 {
			g = 1
		}
		off := i * channels
		for c := 0; c < channels; c++ {
			idx := off + c
			samples[idx] = int16(float64(samples[idx]) * g)
		}
		remaining--
	}
	if remaining == 0 { // ramp spent: silence the rest of this buffer
		for j := i * channels; j < len(samples); j++ {
			samples[j] = 0
		}
		for j := len(samples) * 2; j < len(b); j++ {
			b[j] = 0
		}
	}
	return remaining
}

// mixPCM writes src*g into dst (same length); dst and src may alias.
func mixPCM(dst, src []byte, g float64) {
	dSamples := pcm16(dst)
	sSamples := pcm16(src)
	n := len(dSamples)
	if len(sSamples) < n {
		n = len(sSamples)
	}
	for i := 0; i < n; i++ {
		dSamples[i] = int16(float64(sSamples[i]) * g)
	}
}

// addPCM adds src into dst (saturating), in place.
func addPCM(dst, src []byte) {
	dSamples := pcm16(dst)
	sSamples := pcm16(src)
	n := len(dSamples)
	if len(sSamples) < n {
		n = len(sSamples)
	}
	for i := 0; i < n; i++ {
		s := int32(dSamples[i]) + int32(sSamples[i])
		if s > 32767 {
			s = 32767
		} else if s < -32768 {
			s = -32768
		}
		dSamples[i] = int16(s)
	}
}
