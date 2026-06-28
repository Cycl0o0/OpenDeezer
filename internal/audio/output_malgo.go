//go:build !otosink

package audio

import (
	"fmt"
	"sync"

	"github.com/gen2brain/malgo"
)

// malgoOutput drives a miniaudio (CoreAudio/WASAPI/ALSA) playback device via a
// realtime data callback, and supports output-device enumeration/selection.
// Default backend for the TUI and the GNOME/KDE/Windows GUIs.
type malgoOutput struct {
	ctx        *malgo.AllocatedContext
	mu         sync.Mutex
	device     *malgo.Device
	selectedID *malgo.DeviceID
	read       func(out []byte) int
}

func newOutput() (output, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("audio init: %w", err)
	}
	return &malgoOutput{ctx: ctx}, nil
}

func (o *malgoOutput) start(read func(out []byte) int) error {
	o.read = read
	return o.initDevice(nil)
}

// initDevice (re)creates the playback device, optionally bound to deviceID.
func (o *malgoOutput) initDevice(deviceID *malgo.DeviceID) error {
	cfg := malgo.DefaultDeviceConfig(malgo.Playback)
	cfg.Playback.Format = malgo.FormatS16
	cfg.Playback.Channels = channels
	cfg.SampleRate = sampleRate
	// Large hardware buffer (~200ms × 4) with fewer, bigger periods: the realtime
	// callback re-enters Go, and a small period leaves no slack for scheduling/GC.
	cfg.Periods = 4
	cfg.PeriodSizeInMilliseconds = 200
	if deviceID != nil {
		cfg.Playback.DeviceID = deviceID.Pointer()
	}
	dev, err := malgo.InitDevice(o.ctx.Context, cfg, malgo.DeviceCallbacks{
		Data: func(out, _ []byte, _ uint32) { o.read(out) },
	})
	if err != nil {
		return fmt.Errorf("audio device: %w", err)
	}
	if err := dev.Start(); err != nil {
		dev.Uninit()
		return fmt.Errorf("audio device start: %w", err)
	}
	o.mu.Lock()
	old := o.device
	o.device = dev
	o.selectedID = deviceID
	o.mu.Unlock()
	if old != nil {
		old.Uninit()
	}
	return nil
}

func (o *malgoOutput) devices() ([]Device, error) {
	infos, err := o.ctx.Devices(malgo.Playback)
	if err != nil {
		return nil, err
	}
	o.mu.Lock()
	isDefault := o.selectedID == nil
	o.mu.Unlock()
	out := make([]Device, 0, len(infos)+1)
	out = append(out, Device{ID: "", Name: "System default", IsDefault: isDefault})
	for _, info := range infos {
		out = append(out, Device{ID: info.ID.String(), Name: info.Name(), IsDefault: info.IsDefault != 0})
	}
	return out, nil
}

func (o *malgoOutput) setDevice(id string) error {
	if id == "" {
		return o.initDevice(nil)
	}
	infos, err := o.ctx.Devices(malgo.Playback)
	if err != nil {
		return err
	}
	for i := range infos {
		if infos[i].ID.String() == id {
			devID := infos[i].ID
			return o.initDevice(&devID)
		}
	}
	return o.initDevice(nil) // unknown id: fall back to default rather than fail
}

func (o *malgoOutput) currentDevice() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.selectedID == nil {
		return ""
	}
	return o.selectedID.String()
}

func (o *malgoOutput) close() {
	o.mu.Lock()
	dev := o.device
	o.device = nil
	o.mu.Unlock()
	if dev != nil {
		dev.Uninit()
	}
	if o.ctx != nil {
		_ = o.ctx.Uninit()
		o.ctx.Free()
	}
}
