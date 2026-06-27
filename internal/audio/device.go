package audio

import "github.com/gen2brain/malgo"

// Device is an output device the user can pick.
type Device struct {
	ID        string // backend device id, hex-encoded; "" = system default
	Name      string
	IsDefault bool
}

// Devices lists available playback devices.
func (p *Player) Devices() ([]Device, error) {
	infos, err := p.ctx.Devices(malgo.Playback)
	if err != nil {
		return nil, err
	}
	out := make([]Device, 0, len(infos)+1)
	out = append(out, Device{ID: "", Name: "System default", IsDefault: p.selectedID == nil})
	for _, info := range infos {
		out = append(out, Device{
			ID:        encodeID(info.ID),
			Name:      info.Name(),
			IsDefault: info.IsDefault != 0,
		})
	}
	return out, nil
}

// SetDevice switches output to the device with the given id ("" = system
// default), reinitializing the playback device. Playback continues from the
// current source.
func (p *Player) SetDevice(id string) error {
	if id == "" {
		return p.initDevice(nil)
	}
	infos, err := p.ctx.Devices(malgo.Playback)
	if err != nil {
		return err
	}
	for i := range infos {
		if encodeID(infos[i].ID) == id {
			devID := infos[i].ID
			return p.initDevice(&devID)
		}
	}
	// Unknown id: fall back to default rather than failing playback.
	return p.initDevice(nil)
}

// CurrentDevice returns the selected device id ("" = default).
func (p *Player) CurrentDevice() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.selectedID == nil {
		return ""
	}
	return encodeID(*p.selectedID)
}

func encodeID(id malgo.DeviceID) string {
	b := id.String()
	return b
}
