// Package audio is the oto-backed playback engine: it downloads, decrypts and
// decodes Deezer streams (MP3 + FLAC) and plays one track at a time with seek,
// volume and ReplayGain support.
package audio

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Cycl0o0/OpenDeezer/internal/deezer"
	"github.com/ebitengine/oto/v3"
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
	bytesPerSec = sampleRate * channels * 2 // s16 stereo
)

// Player owns a single oto context and plays one track at a time. The whole
// track is downloaded + Blowfish-decrypted up front into a seekable reader so
// that scrubbing works (the streaming CDN body is not seekable).
type Player struct {
	ctx *oto.Context

	mu  sync.Mutex
	cur *oto.Player
	src *seekSource

	state    atomic.Int32
	played   atomic.Int64 // decoded PCM bytes consumed by oto (position)
	totalMS  atomic.Int64
	lastErr  atomic.Value  // string
	format   atomic.Value  // string: resolved Deezer format of the current stream
	volume   atomic.Uint64 // float64 bits, user volume 0..1
	gainFac  atomic.Uint64 // float64 bits, ReplayGain factor for current track (≤1)
	rgOn     atomic.Bool   // apply ReplayGain loudness normalization
	onFinish func()
}

// dbToFactor converts a ReplayGain dB value to a linear amplitude factor,
// clamped to ≤1 so we only attenuate (oto can't amplify past the source without
// clipping). 0 dB (or unknown) yields 1.0 (no change).
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

// SetReplayGain enables/disables loudness normalization for subsequent tracks
// (and updates the current track immediately).
func (p *Player) SetReplayGain(on bool) {
	p.rgOn.Store(on)
	if !on {
		p.gainFac.Store(math.Float64bits(1))
	}
	p.setVolume(p.Volume()) // re-apply effective volume
}

// ReplayGain reports whether loudness normalization is enabled.
func (p *Player) ReplayGain() bool { return p.rgOn.Load() }

// effectiveVolume is the user volume scaled by the current ReplayGain factor.
func (p *Player) effectiveVolume() float64 {
	f := math.Float64frombits(p.gainFac.Load())
	if f == 0 {
		f = 1
	}
	return p.Volume() * f
}

// Format returns the resolved Deezer format of the current/last stream
// (e.g. "MP3_128", "MP3_320", "FLAC"), or "" if nothing has played.
func (p *Player) Format() string {
	s, _ := p.format.Load().(string)
	return s
}

// pcmStream is a decoder that yields interleaved s16 PCM and can seek by PCM
// byte offset. Both *mp3.Decoder and *flacStream satisfy it.
type pcmStream interface {
	io.Reader
	Seek(offset int64, whence int) (int64, error)
}

// seekSource is the io.Reader oto pulls from. It performs any requested seek on
// the audio-output goroutine, inside the same lock as Read, so the decoder is
// never read and seeked concurrently (which aborts the process).
type seekSource struct {
	p       *Player
	dec     pcmStream
	mu      sync.Mutex
	pending int64 // PCM byte offset to seek to, or -1 for none
}

func (s *seekSource) Read(b []byte) (int, error) {
	s.mu.Lock()
	if s.pending >= 0 {
		off := s.pending
		s.pending = -1
		// A bad seek must not take down the audio goroutine.
		func() {
			defer func() { _ = recover() }()
			if _, err := s.dec.Seek(off, io.SeekStart); err == nil {
				s.p.played.Store(off)
			}
		}()
	}
	s.mu.Unlock()
	n, err := s.dec.Read(b)
	s.p.played.Add(int64(n))
	return n, err
}

func (s *seekSource) requestSeek(off int64) {
	s.mu.Lock()
	s.pending = off
	s.mu.Unlock()
}

// NewPlayer creates the audio output context.
func NewPlayer() (*Player, error) {
	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:   sampleRate,
		ChannelCount: channels,
		Format:       oto.FormatSignedInt16LE,
	})
	if err != nil {
		return nil, fmt.Errorf("audio init: %w", err)
	}
	<-ready
	p := &Player{ctx: ctx}
	p.state.Store(int32(Stopped))
	p.lastErr.Store("")
	p.format.Store("")
	p.gainFac.Store(math.Float64bits(1))
	p.setVolume(1.0)
	return p, nil
}

// SetOnFinish registers a callback fired when a track ends naturally.
func (p *Player) SetOnFinish(fn func()) { p.onFinish = fn }

// State returns the current playback state.
func (p *Player) State() State { return State(p.state.Load()) }

// LastError returns the last playback error message ("" if none).
func (p *Player) LastError() string {
	s, _ := p.lastErr.Load().(string)
	return s
}

// PositionMS returns the approximate playback position.
func (p *Player) PositionMS() int64 { return p.played.Load() * 1000 / bytesPerSec }

// DurationMS returns the current track duration (from metadata).
func (p *Player) DurationMS() int64 { return p.totalMS.Load() }

func (p *Player) setVolume(v float64) {
	if v < 0 {
		v = 0
	} else if v > 1 {
		v = 1
	}
	p.volume.Store(math.Float64bits(v))
	eff := p.effectiveVolume()
	p.mu.Lock()
	if p.cur != nil {
		p.cur.SetVolume(eff)
	}
	p.mu.Unlock()
}

// Volume returns the current volume (0..1).
func (p *Player) Volume() float64 { return math.Float64frombits(p.volume.Load()) }

// AddVolume nudges the volume by delta and returns the new value.
func (p *Player) AddVolume(delta float64) float64 {
	v := p.Volume() + delta
	p.setVolume(v)
	return p.Volume()
}

// Play downloads + decrypts the whole track, then decodes and plays it.
func (p *Player) Play(plan *deezer.StreamPlan, durationMS int64) error {
	p.Stop()
	p.state.Store(int32(Loading))
	p.lastErr.Store("")
	p.played.Store(0)
	p.totalMS.Store(durationMS)
	p.format.Store(plan.Format)
	// ReplayGain factor for this track (1.0 when disabled or unknown).
	if p.rgOn.Load() {
		p.gainFac.Store(math.Float64bits(dbToFactor(plan.GainDB)))
	} else {
		p.gainFac.Store(math.Float64bits(1))
	}

	resp, err := http.Get(plan.CDNURL)
	if err != nil {
		p.fail(err)
		return err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		err := fmt.Errorf("CDN returned %s", resp.Status)
		p.fail(err)
		return err
	}
	enc, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		p.fail(err)
		return err
	}

	// Encrypted Deezer streams need BF_CBC_STRIPE decryption first; plain
	// streams (e.g. podcast episodes) are already raw codec bytes.
	mp3bytes := enc
	if plan.Encrypted {
		mp3bytes, err = deezer.DecryptTrack(plan.TrackID, enc)
		if err != nil {
			p.fail(err)
			return err
		}
	}
	// Decrypt yields the raw codec bytes; pick the decoder by the resolved
	// format (FLAC for HiFi, else MP3). Both decode to s16 stereo PCM.
	var decoder pcmStream
	if strings.Contains(strings.ToUpper(plan.Format), "FLAC") {
		decoder, err = newFLACStream(mp3bytes)
	} else {
		decoder, err = mp3.NewDecoder(bytes.NewReader(mp3bytes))
	}
	if err != nil {
		p.fail(err)
		return err
	}

	src := &seekSource{p: p, dec: decoder, pending: -1}
	player := p.ctx.NewPlayer(src)
	player.SetVolume(p.effectiveVolume())

	p.mu.Lock()
	p.cur = player
	p.src = src
	p.mu.Unlock()

	player.Play()
	p.state.Store(int32(Playing))

	go p.watch(player)
	return nil
}

// SeekMS jumps to an absolute position. The actual decoder seek happens on the
// audio goroutine (see seekSource.Read), so no player is recreated and the
// decoder is never accessed concurrently — both of which previously aborted the
// in-process GUI.
func (p *Player) SeekMS(ms int64) {
	p.mu.Lock()
	src := p.src
	p.mu.Unlock()
	if src == nil {
		return
	}
	if ms < 0 {
		ms = 0
	}
	if total := p.totalMS.Load(); total > 0 && ms > total {
		ms = total
	}
	off := ms * bytesPerSec / 1000
	off -= off % (channels * 2) // align to a whole stereo frame
	p.played.Store(off)         // optimistic, for a snappy scrubber
	src.requestSeek(off)
}

func (p *Player) watch(player *oto.Player) {
	for {
		time.Sleep(200 * time.Millisecond)
		p.mu.Lock()
		cur := p.cur
		p.mu.Unlock()
		if cur != player {
			return // superseded by another Play/Seek/Stop
		}
		if !player.IsPlaying() && p.State() == Playing {
			p.mu.Lock()
			if p.cur == player {
				p.teardownLocked()
			}
			p.mu.Unlock()
			p.state.Store(int32(Stopped))
			if p.onFinish != nil {
				p.onFinish()
			}
			return
		}
	}
}

// Pause halts output, keeping position.
func (p *Player) Pause() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cur != nil && p.State() == Playing {
		p.cur.Pause()
		p.state.Store(int32(Paused))
	}
}

// Resume continues from a paused state.
func (p *Player) Resume() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cur != nil && p.State() == Paused {
		p.cur.Play()
		p.state.Store(int32(Playing))
	}
}

// TogglePause flips between playing and paused.
func (p *Player) TogglePause() {
	switch p.State() {
	case Playing:
		p.Pause()
	case Paused:
		p.Resume()
	}
}

// Stop ends playback and releases the current player.
func (p *Player) Stop() {
	p.mu.Lock()
	p.teardownLocked()
	p.mu.Unlock()
	p.state.Store(int32(Stopped))
}

// teardownLocked releases the current player + decoder. Caller holds p.mu.
func (p *Player) teardownLocked() {
	if p.cur != nil {
		p.cur.Pause()
		_ = p.cur.Close()
		p.cur = nil
	}
	p.src = nil
}

func (p *Player) fail(err error) {
	p.lastErr.Store(err.Error())
	p.state.Store(int32(Errored))
}
