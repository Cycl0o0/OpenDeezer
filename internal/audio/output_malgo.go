//go:build !darwin

package audio

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/gen2brain/malgo"
)

// malgoOutput drives a miniaudio (CoreAudio/WASAPI/ALSA) playback device via a
// realtime data callback, and supports output-device enumeration/selection.
// Default backend for the TUI and the GNOME/KDE/Windows GUIs.
type malgoOutput struct {
	ctx        *malgo.AllocatedContext
	mu         sync.Mutex
	cur        *malgoDevice
	selectedID *malgo.DeviceID
	read       func(out []byte) int
	lost       func(string)
	suspended  bool
	closed     bool
}

// malgoDevice wraps one initialized device with a per-device "intentional stop"
// flag: miniaudio fires the Stop callback both on a stop we caused (suspend) and
// on an unexpected device loss (unplugged DAC, dropped Bluetooth). The flag lets
// the callback tell them apart. (Uninit deletes the callback before stopping, so
// a device we tear down never reaches the callback at all.)
type malgoDevice struct {
	dev         *malgo.Device
	intentional atomic.Bool
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

func (o *malgoOutput) setLostHandler(fn func(string)) {
	o.mu.Lock()
	o.lost = fn
	o.mu.Unlock()
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
	md := &malgoDevice{}
	dev, err := malgo.InitDevice(o.ctx.Context, cfg, malgo.DeviceCallbacks{
		Data: func(out, _ []byte, _ uint32) { o.read(out) },
		Stop: func() {
			if md.intentional.Load() {
				return // a suspend/teardown stop we caused, not a device loss
			}
			// Recover off the miniaudio thread: device functions must not be
			// called from within the notification callback.
			go o.deviceLost(md)
		},
	})
	if err != nil {
		return fmt.Errorf("audio device: %w", err)
	}
	md.dev = dev

	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		md.intentional.Store(true)
		dev.Uninit()
		return fmt.Errorf("audio output closed")
	}
	// Stop the old device BEFORE starting the new one: miniaudio pre-fills the new
	// device's periods by invoking the data callback from Start(), so starting
	// first would run two realtime callbacks into readPCM concurrently — a data
	// race on the mix scratch buffer plus double-draining of the ring.
	if o.cur != nil {
		o.cur.intentional.Store(true)
		o.cur.dev.Uninit()
		o.cur = nil
	}
	if err := dev.Start(); err != nil {
		md.intentional.Store(true)
		dev.Uninit()
		return fmt.Errorf("audio device start: %w", err)
	}
	o.cur = md
	o.selectedID = deviceID
	o.suspended = false
	return nil
}

// initDeviceRecover wraps initDevice for runtime device switches. A failure in
// dev.Start() has already torn down the previous device (it must be stopped
// before the new one starts; see initDevice), so returning the error alone
// would leave no device pulling readPCM — the player would sit silently frozen
// in "Playing". Recover instead: retry once on the system default, and if audio
// still cannot flow, surface it through the lost handler so the player moves to
// Errored. An InitDevice-stage failure leaves the old device intact (deviceDown
// is false), so it just returns the error like before.
func (o *malgoOutput) initDeviceRecover(deviceID *malgo.DeviceID) error {
	err := o.initDevice(deviceID)
	if err == nil {
		return nil
	}
	if deviceID != nil && o.deviceDown() {
		if o.initDevice(nil) == nil {
			return err // default is playing again; still report the failed switch
		}
	}
	if o.deviceDown() {
		o.notifyLost()
	}
	return err
}

// deviceDown reports that no playback device is running (and we are not simply
// shutting down) — the state after a failed dev.Start() destroyed the old device.
func (o *malgoOutput) deviceDown() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.cur == nil && !o.closed
}

func (o *malgoOutput) notifyLost() {
	o.mu.Lock()
	lost := o.lost
	o.mu.Unlock()
	if lost != nil {
		lost("output device lost")
	}
}

// deviceLost handles an unexpected stop of md: fall back to the system default so
// playback recovers, or surface the failure to the player if that fails. Runs on
// its own goroutine.
func (o *malgoOutput) deviceLost(md *malgoDevice) {
	o.mu.Lock()
	stale := o.closed || o.cur != md
	o.mu.Unlock()
	if stale {
		return // already replaced or shutting down
	}
	if err := o.initDevice(nil); err != nil {
		o.notifyLost()
	}
}

// suspend stops (on=true) / restarts (on=false) the current device without
// tearing it down, so playback resumes on the same device. Thread-safe.
func (o *malgoOutput) suspend(on bool) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cur == nil {
		return nil // no device (e.g. after a failed switch)
	}
	if on {
		if o.suspended {
			return nil
		}
		o.suspended = true
		o.cur.intentional.Store(true) // our Stop(), not a device loss
		return o.cur.dev.Stop()
	}
	if !o.suspended {
		return nil
	}
	o.suspended = false
	o.cur.intentional.Store(false) // re-arm loss detection
	return o.cur.dev.Start()
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
		return o.initDeviceRecover(nil)
	}
	infos, err := o.ctx.Devices(malgo.Playback)
	if err != nil {
		return err
	}
	for i := range infos {
		if infos[i].ID.String() == id {
			devID := infos[i].ID
			return o.initDeviceRecover(&devID)
		}
	}
	return o.initDeviceRecover(nil) // unknown id: fall back to default rather than fail
}

func (o *malgoOutput) currentDevice() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.selectedID == nil {
		return ""
	}
	return o.selectedID.String()
}

func (o *malgoOutput) latencyFrames() int {
	return 4 * (200 * sampleRate / 1000)
}

func (o *malgoOutput) close() {
	o.mu.Lock()
	o.closed = true
	cur := o.cur
	o.cur = nil
	if cur != nil {
		cur.intentional.Store(true)
	}
	o.mu.Unlock()
	if cur != nil {
		cur.dev.Uninit()
	}
	if o.ctx != nil {
		_ = o.ctx.Uninit()
		o.ctx.Free()
	}
}
