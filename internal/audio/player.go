package audio

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Cycl0o0/DeezerTUI/internal/deezer"
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

	mu       sync.Mutex
	cur      *oto.Player
	decoder  *mp3.Decoder

	state    atomic.Int32
	played   atomic.Int64 // decoded PCM bytes consumed by oto (position)
	totalMS  atomic.Int64
	lastErr  atomic.Value // string
	volume   atomic.Uint64 // float64 bits, 0..1
	onFinish func()
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
	p.mu.Lock()
	if p.cur != nil {
		p.cur.SetVolume(v)
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

// countReader counts decoded bytes pulled by oto (for position).
type countReader struct {
	r io.Reader
	p *Player
}

func (c countReader) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	c.p.played.Add(int64(n))
	return n, err
}

// Play downloads + decrypts the whole track, then decodes and plays it.
func (p *Player) Play(plan *deezer.StreamPlan, durationMS int64) error {
	p.Stop()
	p.state.Store(int32(Loading))
	p.lastErr.Store("")
	p.played.Store(0)
	p.totalMS.Store(durationMS)

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

	// Decrypt the whole BF_CBC_STRIPE buffer, then decode from a seekable reader.
	mp3bytes, err := deezer.DecryptTrack(plan.TrackID, enc)
	if err != nil {
		p.fail(err)
		return err
	}
	decoder, err := mp3.NewDecoder(bytes.NewReader(mp3bytes))
	if err != nil {
		p.fail(err)
		return err
	}

	player := p.ctx.NewPlayer(countReader{r: decoder, p: p})
	player.SetVolume(p.Volume())

	p.mu.Lock()
	p.cur = player
	p.decoder = decoder
	p.mu.Unlock()

	player.Play()
	p.state.Store(int32(Playing))

	go p.watch(player)
	return nil
}

// SeekMS jumps to an absolute position by seeking the decoded stream and
// rebuilding the oto player (to flush its buffered audio).
func (p *Player) SeekMS(ms int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.decoder == nil || p.cur == nil {
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
	if _, err := p.decoder.Seek(off, io.SeekStart); err != nil {
		return
	}
	p.played.Store(off)

	wasPlaying := p.State() == Playing
	old := p.cur
	np := p.ctx.NewPlayer(countReader{r: p.decoder, p: p})
	np.SetVolume(p.Volume())
	p.cur = np
	old.Pause()
	old.Close()
	np.Play()
	if !wasPlaying {
		np.Pause()
		p.state.Store(int32(Paused))
	}
	go p.watch(np)
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
		p.cur.Close()
		p.cur = nil
	}
	p.decoder = nil
}

func (p *Player) fail(err error) {
	p.lastErr.Store(err.Error())
	p.state.Store(int32(Errored))
}
