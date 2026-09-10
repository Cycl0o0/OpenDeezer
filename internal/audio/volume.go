package audio

import (
	"time"

	"github.com/Cycl0o0/OpenDeezer/v3/internal/config"
)

// The volume persists engine-side like the EQ (see eq.go): NewPlayer restores
// the last level and every SetVolume/AddVolume schedules a debounced save, so
// the TUI, every GUI and the control API resume at the level the user left
// instead of full volume on every launch.

// loadVolume restores the persisted volume, or full volume when none is saved;
// called once from NewPlayer. It stores directly, so it never schedules a save.
func (p *Player) loadVolume() {
	v := 1.0
	if saved, ok := config.LoadVolume(); ok {
		v = saved
	}
	p.setVolume(v)
}

// saveVolumeSoon schedules a debounced write of the current volume: a slider
// drag calls SetVolume continuously, and only the level it settles on needs to
// reach the disk.
func (p *Player) saveVolumeSoon() {
	p.volSaveMu.Lock()
	defer p.volSaveMu.Unlock()
	if p.volSaveTimer != nil {
		p.volSaveTimer.Stop()
	}
	p.volSaveTimer = time.AfterFunc(500*time.Millisecond, p.saveVolumeNow)
}

// saveVolumeNow writes the current volume immediately.
func (p *Player) saveVolumeNow() { _ = config.SaveVolume(p.Volume()) }

// flushVolumeSave writes a pending debounced save right away (Close calls it so
// a change made just before quitting isn't lost).
func (p *Player) flushVolumeSave() {
	p.volSaveMu.Lock()
	pending := p.volSaveTimer != nil && p.volSaveTimer.Stop()
	p.volSaveMu.Unlock()
	if pending {
		p.saveVolumeNow()
	}
}
