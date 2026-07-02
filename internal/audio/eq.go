// eq.go — 10-band graphic equalizer + mono downmix, a DSP stage on the player's
// realtime PCM path. Peaking biquads (RBJ audio-EQ cookbook) at the fixed
// 44100 Hz pipeline rate, one filter cascade per channel. A settings change
// builds a fresh immutable eqConfig (coefficients included) off the audio
// thread and swaps it in atomically; the filter memory lives on the Player and
// is touched only from the realtime callback, so the hot path stays lock-free
// and allocation-free. State persists engine-side (config.SaveEQ), so every
// client shares one set of settings, like the sleep timer shares one clock.

package audio

import (
	"fmt"
	"math"
	"time"

	"github.com/Cycl0o0/OpenDeezer/internal/config"
)

const (
	eqBands     = 10
	eqGainMinDB = -12
	eqGainMaxDB = 12
)

// eqFreqs are the ISO octave centers used by classic 10-band graphic EQs.
var eqFreqs = [eqBands]float64{31.5, 63, 125, 250, 500, 1000, 2000, 4000, 8000, 16000}

// EQPresetNames is the preset order every client shows (core-owned so the UIs
// stay identical). "custom" is implicit: any manual band edit switches to it.
var EQPresetNames = []string{
	"flat", "bass-boost", "bass-reducer", "treble-boost", "vocal",
	"rock", "pop", "jazz", "classical", "electronic",
}

var eqPresets = map[string][eqBands]float64{
	"flat":         {0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	"bass-boost":   {6, 5, 4, 2.5, 1, 0, 0, 0, 0, 0},
	"bass-reducer": {-6, -5, -4, -2.5, -1, 0, 0, 0, 0, 0},
	"treble-boost": {0, 0, 0, 0, 0, 1, 2.5, 4, 5, 6},
	"vocal":        {-2, -1, 0, 1.5, 3, 3, 2, 1, 0, -1},
	"rock":         {5, 4, 3, 1, -0.5, -1, 0.5, 2.5, 3.5, 4.5},
	"pop":          {-1, 0, 1.5, 3, 4, 3, 1, 0, -1, -1.5},
	"jazz":         {3, 2, 1, 2, -1.5, -1.5, 0, 1, 2, 3},
	"classical":    {4, 3, 2, 1.5, -1, -1, 0, 1.5, 3, 4},
	"electronic":   {4, 3.5, 1, 0, -2, 1, 0.5, 1, 3, 4.5},
}

// biquad holds normalized (a0=1) peaking-filter coefficients.
type biquad struct{ b0, b1, b2, a1, a2 float64 }

// peakingCoeffs computes RBJ cookbook peaking-EQ coefficients for a one-octave
// bandwidth at the pipeline sample rate.
func peakingCoeffs(freq, gainDB float64) biquad {
	if gainDB == 0 {
		return biquad{b0: 1}
	}
	a := math.Pow(10, gainDB/40)
	w0 := 2 * math.Pi * freq / sampleRate
	sw, cw := math.Sin(w0), math.Cos(w0)
	const bwOctaves = 1.0
	alpha := sw / 2 * math.Sinh(math.Ln2/2*bwOctaves*w0/sw)
	a0 := 1 + alpha/a
	return biquad{
		b0: (1 + alpha*a) / a0,
		b1: -2 * cw / a0,
		b2: (1 - alpha*a) / a0,
		a1: -2 * cw / a0,
		a2: (1 - alpha/a) / a0,
	}
}

// eqConfig is one immutable snapshot of the EQ settings plus the derived
// coefficients. Swapped whole so the RT callback never sees a half-update.
type eqConfig struct {
	enabled  bool
	mono     bool
	preampDB float64
	preampF  float64 // linear factor derived from preampDB
	gainsDB  [eqBands]float64
	preset   string
	coeffs   [eqBands]biquad
	active   [eqBands]bool
}

func newEQConfig(enabled, mono bool, preampDB float64, gains [eqBands]float64, preset string) *eqConfig {
	c := &eqConfig{enabled: enabled, mono: mono, preampDB: preampDB, gainsDB: gains, preset: preset}
	c.preampF = math.Pow(10, preampDB/20)
	for i, g := range gains {
		if g != 0 {
			c.coeffs[i] = peakingCoeffs(eqFreqs[i], g)
			c.active[i] = true
		}
	}
	return c
}

// process runs one sample through the active band cascade + preamp. z is the
// per-channel filter memory (direct form II transposed).
func (c *eqConfig) process(z *[eqBands][2]float64, x float64) float64 {
	for b := 0; b < eqBands; b++ {
		if !c.active[b] {
			continue
		}
		co := &c.coeffs[b]
		y := co.b0*x + z[b][0]
		z[b][0] = co.b1*x - co.a1*y + z[b][1]
		z[b][1] = co.b2*x - co.a2*y
		x = y
	}
	return x * c.preampF
}

// applyEQ runs mono downmix + the EQ cascade over interleaved s16 stereo PCM in
// place. Called only from readPCM (RT path): no locks, no allocation.
func (p *Player) applyEQ(b []byte) {
	cfg := p.eqCfg.Load()
	if cfg == nil {
		return
	}
	if cfg != p.eqPrev {
		// Reset the filter memory when the EQ turns on after being off, so state
		// from minutes ago can't pop. Plain band edits keep the memory to avoid a
		// reset click on every slider move.
		if cfg.enabled && (p.eqPrev == nil || !p.eqPrev.enabled) {
			p.eqZ = [channels][eqBands][2]float64{}
		}
		p.eqPrev = cfg
	}
	if !cfg.enabled && !cfg.mono {
		return
	}
	for i := 0; i+frameBytes <= len(b); i += frameBytes {
		l := float64(int16(uint16(b[i]) | uint16(b[i+1])<<8))
		r := float64(int16(uint16(b[i+2]) | uint16(b[i+3])<<8))
		if cfg.mono {
			m := (l + r) * 0.5
			l, r = m, m
		}
		if cfg.enabled {
			l = cfg.process(&p.eqZ[0], l)
			r = cfg.process(&p.eqZ[1], r)
		}
		b[i], b[i+1] = s16Bytes(l)
		b[i+2], b[i+3] = s16Bytes(r)
	}
}

// s16Bytes clamps a float sample to int16 and returns its little-endian bytes.
func s16Bytes(v float64) (lo, hi byte) {
	if v > 32767 {
		v = 32767
	} else if v < -32768 {
		v = -32768
	}
	s := int16(v)
	return byte(s), byte(uint16(s) >> 8)
}

// ---- public API ----
// Every setter rebuilds + atomically swaps the config, then schedules a
// debounced save to eq.json so slider drags don't hammer the disk.

func clampEQDB(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	if v < eqGainMinDB {
		return eqGainMinDB
	}
	if v > eqGainMaxDB {
		return eqGainMaxDB
	}
	return v
}

func (p *Player) eqSnapshot() *eqConfig {
	if c := p.eqCfg.Load(); c != nil {
		return c
	}
	return newEQConfig(false, false, 0, [eqBands]float64{}, "flat")
}

// SetEQEnabled turns the equalizer on/off (mono downmix is independent).
func (p *Player) SetEQEnabled(on bool) {
	c := p.eqSnapshot()
	p.swapEQ(newEQConfig(on, c.mono, c.preampDB, c.gainsDB, c.preset))
}

// EQEnabled reports whether the equalizer is on.
func (p *Player) EQEnabled() bool { return p.eqSnapshot().enabled }

// SetMonoDownmix folds stereo to mono (accessibility / single-speaker setups).
func (p *Player) SetMonoDownmix(on bool) {
	c := p.eqSnapshot()
	p.swapEQ(newEQConfig(c.enabled, on, c.preampDB, c.gainsDB, c.preset))
}

// MonoDownmix reports whether mono downmix is on.
func (p *Player) MonoDownmix() bool { return p.eqSnapshot().mono }

// SetEQGain sets one band's gain in dB (clamped to ±12). Preset becomes "custom".
func (p *Player) SetEQGain(band int, db float64) error {
	if band < 0 || band >= eqBands {
		return fmt.Errorf("eq: band %d out of range 0..%d", band, eqBands-1)
	}
	c := p.eqSnapshot()
	g := c.gainsDB
	g[band] = clampEQDB(db)
	p.swapEQ(newEQConfig(c.enabled, c.mono, c.preampDB, g, "custom"))
	return nil
}

// SetEQGains sets all band gains in dB (clamped). Preset becomes "custom".
func (p *Player) SetEQGains(db []float64) error {
	if len(db) != eqBands {
		return fmt.Errorf("eq: want %d gains, got %d", eqBands, len(db))
	}
	c := p.eqSnapshot()
	var g [eqBands]float64
	for i, v := range db {
		g[i] = clampEQDB(v)
	}
	p.swapEQ(newEQConfig(c.enabled, c.mono, c.preampDB, g, "custom"))
	return nil
}

// EQGains returns the band gains in dB.
func (p *Player) EQGains() []float64 {
	c := p.eqSnapshot()
	out := make([]float64, eqBands)
	copy(out, c.gainsDB[:])
	return out
}

// SetEQPreamp sets the output preamp in dB (clamped to ±12), for taming
// clipping when several bands are boosted.
func (p *Player) SetEQPreamp(db float64) {
	c := p.eqSnapshot()
	p.swapEQ(newEQConfig(c.enabled, c.mono, clampEQDB(db), c.gainsDB, c.preset))
}

// EQPreampDB returns the preamp in dB.
func (p *Player) EQPreampDB() float64 { return p.eqSnapshot().preampDB }

// SetEQPreset applies a named preset's band gains.
func (p *Player) SetEQPreset(name string) error {
	g, ok := eqPresets[name]
	if !ok {
		return fmt.Errorf("eq: unknown preset %q", name)
	}
	c := p.eqSnapshot()
	p.swapEQ(newEQConfig(c.enabled, c.mono, c.preampDB, g, name))
	return nil
}

// EQPreset returns the current preset name ("custom" after manual edits).
func (p *Player) EQPreset() string { return p.eqSnapshot().preset }

// EQBands returns the band center frequencies in Hz.
func (p *Player) EQBands() []float64 {
	out := make([]float64, eqBands)
	copy(out, eqFreqs[:])
	return out
}

func (p *Player) swapEQ(c *eqConfig) {
	p.eqCfg.Store(c)
	p.saveEQSoon()
}

// ---- persistence ----

// loadEQ restores the persisted state; called once from NewPlayer before the
// output starts pulling.
func (p *Player) loadEQ() {
	s, ok := config.LoadEQ()
	if !ok {
		p.eqCfg.Store(newEQConfig(false, false, 0, [eqBands]float64{}, "flat"))
		return
	}
	var g [eqBands]float64
	for i := 0; i < eqBands && i < len(s.GainsDB); i++ {
		g[i] = clampEQDB(s.GainsDB[i])
	}
	preset := s.Preset
	if _, known := eqPresets[preset]; !known {
		preset = "custom"
	}
	p.eqCfg.Store(newEQConfig(s.Enabled, s.Mono, clampEQDB(s.PreampDB), g, preset))
}

// saveEQSoon schedules a debounced write of the current state.
func (p *Player) saveEQSoon() {
	p.eqSaveMu.Lock()
	defer p.eqSaveMu.Unlock()
	if p.eqSaveTimer != nil {
		p.eqSaveTimer.Stop()
	}
	p.eqSaveTimer = time.AfterFunc(500*time.Millisecond, p.saveEQNow)
}

// saveEQNow writes the current state immediately (also used by Close to flush
// a pending debounce).
func (p *Player) saveEQNow() {
	c := p.eqSnapshot()
	_ = config.SaveEQ(config.EQ{
		Enabled: c.enabled, Mono: c.mono, PreampDB: c.preampDB,
		GainsDB: append([]float64(nil), c.gainsDB[:]...), Preset: c.preset,
	})
}
