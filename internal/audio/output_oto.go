//go:build darwin

package audio

import (
	"fmt"

	"github.com/ebitengine/oto/v3"
)

// otoOutput drives playback through oto (AudioToolbox on macOS via purego). Used
// on ALL macOS builds (TUI + GUIs): malgo's CoreAudio data callback is unreliable
// here (choppy playback, especially in the c-archive GUI) — oto's pull model is
// smooth. The cost is no output-device selection (oto = system default only);
// Linux/Windows use malgo and keep selection.
type otoOutput struct {
	ctx    *oto.Context
	player *oto.Player
}

func newOutput() (output, error) {
	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:   sampleRate,
		ChannelCount: channels,
		Format:       oto.FormatSignedInt16LE,
	})
	if err != nil {
		return nil, fmt.Errorf("audio init: %w", err)
	}
	<-ready
	return &otoOutput{ctx: ctx}, nil
}

func (o *otoOutput) start(read func(out []byte) int) error {
	o.player = o.ctx.NewPlayer(&otoReader{read: read})
	o.player.Play()
	return nil
}

// otoReader adapts the player's pull function to oto's io.Reader. read fills the
// buffer (zeroing any tail), so this always returns a full buffer and never EOF —
// oto keeps pulling, playing silence when nothing is queued.
type otoReader struct{ read func(out []byte) int }

func (r *otoReader) Read(p []byte) (int, error) {
	r.read(p)
	return len(p), nil
}

// oto plays the system default device only.
func (o *otoOutput) devices() ([]Device, error) {
	return []Device{{ID: "", Name: "System default", IsDefault: true}}, nil
}
func (o *otoOutput) setDevice(string) error { return nil }
func (o *otoOutput) currentDevice() string  { return "" }
func (o *otoOutput) deviceDown() bool       { return false }

// suspend releases/restores the audio context (used on audio-focus loss, e.g.
// iOS interruptions) without dropping the queued player.
func (o *otoOutput) suspend(on bool) error {
	if o.ctx == nil {
		return nil
	}
	if on {
		return o.ctx.Suspend()
	}
	return o.ctx.Resume()
}

// oto follows the system default device on a device change itself, so there is
// nothing for the player to recover from here.
func (o *otoOutput) setLostHandler(func(string)) {}

func (o *otoOutput) latencyFrames() int {
	return sampleRate / 2
}

func (o *otoOutput) close() {
	if o.player != nil {
		_ = o.player.Close()
	}
}
