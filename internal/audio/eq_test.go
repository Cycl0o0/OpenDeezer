package audio

import (
	"math"
	"testing"
)

// sineS16 renders n frames of a stereo sine at freq into interleaved s16.
func sineS16(freq float64, n int, amp float64) []byte {
	b := make([]byte, n*frameBytes)
	for i := 0; i < n; i++ {
		v := amp * math.Sin(2*math.Pi*freq*float64(i)/sampleRate)
		s := int16(v * 32767)
		b[i*4], b[i*4+1] = byte(s), byte(uint16(s)>>8)
		b[i*4+2], b[i*4+3] = byte(s), byte(uint16(s)>>8)
	}
	return b
}

// rmsS16 returns the RMS of the left channel of interleaved s16 stereo.
func rmsS16(b []byte) float64 {
	var sum float64
	n := 0
	for i := 0; i+frameBytes <= len(b); i += frameBytes {
		v := float64(int16(uint16(b[i]) | uint16(b[i+1])<<8))
		sum += v * v
		n++
	}
	return math.Sqrt(sum / float64(n))
}

// newTestPlayer builds a Player with EQ state but no audio device, redirecting
// the debounced eq.json save into a scratch HOME.
func newTestPlayer(t *testing.T) *Player {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	p := &Player{}
	p.eqCfg.Store(newEQConfig(false, false, 0, [eqBands]float64{}, "flat"))
	return p
}

func TestEQBoostRaisesBandLevel(t *testing.T) {
	p := newTestPlayer(t)
	p.SetEQEnabled(true)
	if err := p.SetEQGain(5, 12); err != nil { // 1 kHz +12 dB
		t.Fatal(err)
	}
	// Half-amplitude sine so a +12 dB boost stays below clipping.
	in := sineS16(1000, sampleRate, 0.25)
	before := rmsS16(in)
	p.applyEQ(in)
	// Skip the first 2000 frames (filter settle) when measuring.
	after := rmsS16(in[2000*frameBytes:])
	gainDB := 20 * math.Log10(after/before)
	if gainDB < 10 || gainDB > 13 {
		t.Fatalf("1kHz band +12dB boost measured %.2f dB, want ~12", gainDB)
	}
}

func TestEQCutLowersBandLevel(t *testing.T) {
	p := newTestPlayer(t)
	p.SetEQEnabled(true)
	if err := p.SetEQGain(5, -12); err != nil {
		t.Fatal(err)
	}
	in := sineS16(1000, sampleRate, 0.5)
	before := rmsS16(in)
	p.applyEQ(in)
	after := rmsS16(in[2000*frameBytes:])
	gainDB := 20 * math.Log10(after/before)
	if gainDB > -10 || gainDB < -13 {
		t.Fatalf("1kHz band -12dB cut measured %.2f dB, want ~-12", gainDB)
	}
}

func TestEQOffBandLeavesOtherFrequenciesAlone(t *testing.T) {
	p := newTestPlayer(t)
	p.SetEQEnabled(true)
	if err := p.SetEQGain(0, 12); err != nil { // 31.5 Hz boost
		t.Fatal(err)
	}
	in := sineS16(8000, sampleRate/2, 0.5) // far from the boosted band
	before := rmsS16(in)
	p.applyEQ(in)
	after := rmsS16(in[2000*frameBytes:])
	gainDB := 20 * math.Log10(after/before)
	if math.Abs(gainDB) > 1 {
		t.Fatalf("8kHz level moved %.2f dB under a 31.5Hz boost, want ~0", gainDB)
	}
}

func TestMonoDownmixFoldsChannels(t *testing.T) {
	p := newTestPlayer(t)
	p.SetMonoDownmix(true)
	// L = sine, R = inverted sine: mono fold must cancel to silence.
	n := 1000
	b := make([]byte, n*frameBytes)
	for i := 0; i < n; i++ {
		v := int16(16000 * math.Sin(2*math.Pi*440*float64(i)/sampleRate))
		b[i*4], b[i*4+1] = byte(uint16(v)), byte(uint16(v)>>8)
		w := -v
		b[i*4+2], b[i*4+3] = byte(uint16(w)), byte(uint16(w)>>8)
	}
	p.applyEQ(b)
	if r := rmsS16(b); r > 1 {
		t.Fatalf("mono fold of L=-R should cancel, RMS=%f", r)
	}
}

func TestEQSettersClampAndValidate(t *testing.T) {
	p := newTestPlayer(t)
	if err := p.SetEQGain(-1, 0); err == nil {
		t.Fatal("band -1 accepted")
	}
	if err := p.SetEQGain(eqBands, 0); err == nil {
		t.Fatal("band out of range accepted")
	}
	if err := p.SetEQGain(3, 99); err != nil {
		t.Fatal(err)
	}
	if g := p.EQGains()[3]; g != eqGainMaxDB {
		t.Fatalf("gain not clamped: %v", g)
	}
	if err := p.SetEQPreset("nope"); err == nil {
		t.Fatal("unknown preset accepted")
	}
	if err := p.SetEQPreset("rock"); err != nil {
		t.Fatal(err)
	}
	if p.EQPreset() != "rock" {
		t.Fatalf("preset = %q", p.EQPreset())
	}
	// A manual edit flips the preset to custom.
	if err := p.SetEQGain(0, 1); err != nil {
		t.Fatal(err)
	}
	if p.EQPreset() != "custom" {
		t.Fatalf("preset after manual edit = %q, want custom", p.EQPreset())
	}
	if err := p.SetEQGains(make([]float64, 3)); err == nil {
		t.Fatal("short gains slice accepted")
	}
}

func TestEQPresetTablesMatchBandCount(t *testing.T) {
	for _, name := range EQPresetNames {
		if _, ok := eqPresets[name]; !ok {
			t.Fatalf("preset %q listed but missing from table", name)
		}
	}
	if len(EQPresetNames) != len(eqPresets) {
		t.Fatalf("preset name list (%d) and table (%d) out of sync",
			len(EQPresetNames), len(eqPresets))
	}
}

func TestEQDisabledIsPassthrough(t *testing.T) {
	p := newTestPlayer(t)
	_ = p.SetEQGain(5, 12)
	p.SetEQEnabled(false)
	in := sineS16(1000, 4410, 0.5)
	want := append([]byte(nil), in...)
	p.applyEQ(in)
	for i := range in {
		if in[i] != want[i] {
			t.Fatal("disabled EQ modified samples")
		}
	}
}
