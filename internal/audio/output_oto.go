//go:build otosink

package audio

import (
	"fmt"

	"github.com/ebitengine/oto/v3"
)

// otoOutput drives playback through oto (AudioToolbox on macOS via purego). Used
// for the macOS GUI, where malgo's CoreAudio data callback runs unreliably inside
// the c-archive (choppy playback) — oto's pull model is smooth there. The cost is
// no output-device selection (oto plays the system default only).
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

func (o *otoOutput) close() {
	if o.player != nil {
		_ = o.player.Close()
	}
}
