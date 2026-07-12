package audio

import (
	"bytes"
	"math/rand"
	"testing"
)

// ---- Old byte-wise implementations for equivalence testing ----

func oldApplyGain(b []byte, g float64) {
	if g >= 0.999 {
		return
	}
	for i := 0; i+1 < len(b); i += 2 {
		v := int16(uint16(b[i]) | uint16(b[i+1])<<8)
		v = int16(float64(v) * g)
		b[i] = byte(v)
		b[i+1] = byte(uint16(v) >> 8)
	}
}

func oldApplyFadeIn(b []byte, remaining int64) int64 {
	for i := 0; i+frameBytes <= len(b) && remaining > 0; i += frameBytes {
		g := float64(fadeInFrames-remaining) / float64(fadeInFrames)
		if g < 0 {
			g = 0
		} else if g > 1 {
			g = 1
		}
		for c := 0; c < channels; c++ {
			off := i + c*2
			v := int16(uint16(b[off]) | uint16(b[off+1])<<8)
			v = int16(float64(v) * g)
			b[off] = byte(v)
			b[off+1] = byte(uint16(v) >> 8)
		}
		remaining--
	}
	return remaining
}

func oldApplyFadeOut(b []byte, remaining int64) int64 {
	i := 0
	for ; i+frameBytes <= len(b) && remaining > 0; i += frameBytes {
		g := float64(remaining) / float64(fadeOutFrames)
		if g < 0 {
			g = 0
		} else if g > 1 {
			g = 1
		}
		for c := 0; c < channels; c++ {
			off := i + c*2
			v := int16(uint16(b[off]) | uint16(b[off+1])<<8)
			v = int16(float64(v) * g)
			b[off] = byte(v)
			b[off+1] = byte(uint16(v) >> 8)
		}
		remaining--
	}
	if remaining == 0 { // ramp spent: silence the rest of this buffer
		for ; i < len(b); i++ {
			b[i] = 0
		}
	}
	return remaining
}

func oldMixPCM(dst, src []byte, g float64) {
	for i := 0; i+1 < len(src) && i+1 < len(dst); i += 2 {
		v := int16(uint16(src[i]) | uint16(src[i+1])<<8)
		v = int16(float64(v) * g)
		dst[i] = byte(v)
		dst[i+1] = byte(uint16(v) >> 8)
	}
}

func oldAddPCM(dst, src []byte) {
	for i := 0; i+1 < len(src) && i+1 < len(dst); i += 2 {
		a := int32(int16(uint16(dst[i]) | uint16(dst[i+1])<<8))
		b := int32(int16(uint16(src[i]) | uint16(src[i+1])<<8))
		s := a + b
		if s > 32767 {
			s = 32767
		} else if s < -32768 {
			s = -32768
		}
		dst[i] = byte(int16(s))
		dst[i+1] = byte(uint16(int16(s)) >> 8)
	}
}

func oldApplyEQ(p *Player, b []byte) {
	cfg := p.eqCfg.Load()
	if cfg == nil {
		return
	}
	if cfg != p.eqPrev {
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
		b[i], b[i+1] = oldS16Bytes(l)
		b[i+2], b[i+3] = oldS16Bytes(r)
	}
}

func oldS16Bytes(v float64) (lo, hi byte) {
	if v > 32767 {
		v = 32767
	} else if v < -32768 {
		v = -32768
	}
	s := int16(v)
	return byte(s), byte(uint16(s) >> 8)
}

// ---- Equivalence Tests ----

func TestApplyGainEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	lengths := []int{0, 1, 2, 3, 4, 5, 8, 15, 100, 1024, 1025}
	gains := []float64{0.0, 0.1, 0.5, 0.99, 1.0, 1.5, 2.0}

	for _, l := range lengths {
		for _, g := range gains {
			buf1 := make([]byte, l)
			rng.Read(buf1)
			buf2 := make([]byte, l)
			copy(buf2, buf1)

			applyGain(buf1, g)
			oldApplyGain(buf2, g)

			if !bytes.Equal(buf1, buf2) {
				t.Errorf("applyGain mismatch for length %d, gain %v", l, g)
			}
		}
	}
}

func TestApplyFadeInEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	lengths := []int{0, 1, 2, 3, 4, 5, 8, 15, 16, 32, 100, 1024}
	remainings := []int64{0, 1, 5, 10, 100, 200, 1000}

	for _, l := range lengths {
		for _, rem := range remainings {
			buf1 := make([]byte, l)
			rng.Read(buf1)
			buf2 := make([]byte, l)
			copy(buf2, buf1)

			rem1 := applyFadeIn(buf1, rem)
			rem2 := oldApplyFadeIn(buf2, rem)

			if rem1 != rem2 {
				t.Errorf("applyFadeIn remaining mismatch: got %d, want %d", rem1, rem2)
			}
			if !bytes.Equal(buf1, buf2) {
				t.Errorf("applyFadeIn buffer mismatch for length %d, remaining %d", l, rem)
			}
		}
	}
}

func TestApplyFadeOutEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	lengths := []int{0, 1, 2, 3, 4, 5, 8, 15, 16, 32, 100, 1024}
	remainings := []int64{0, 1, 5, 10, 100, 200, 1000}

	for _, l := range lengths {
		for _, rem := range remainings {
			buf1 := make([]byte, l)
			rng.Read(buf1)
			buf2 := make([]byte, l)
			copy(buf2, buf1)

			rem1 := applyFadeOut(buf1, rem)
			rem2 := oldApplyFadeOut(buf2, rem)

			if rem1 != rem2 {
				t.Errorf("applyFadeOut remaining mismatch: got %d, want %d", rem1, rem2)
			}
			if !bytes.Equal(buf1, buf2) {
				t.Errorf("applyFadeOut buffer mismatch for length %d, remaining %d", l, rem)
			}
		}
	}
}

func TestMixPCMEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	lengths := []int{0, 1, 2, 3, 4, 5, 8, 15, 32, 100, 1024}
	gains := []float64{0.0, 0.1, 0.5, 1.0, 1.5, 2.0}

	for _, l := range lengths {
		for _, g := range gains {
			dst1 := make([]byte, l)
			rng.Read(dst1)
			src1 := make([]byte, l)
			rng.Read(src1)

			dst2 := make([]byte, l)
			copy(dst2, dst1)
			src2 := make([]byte, l)
			copy(src2, src1)

			mixPCM(dst1, src1, g)
			oldMixPCM(dst2, src2, g)

			if !bytes.Equal(dst1, dst2) {
				t.Errorf("mixPCM mismatch for length %d, gain %v", l, g)
			}

			// Aliased case
			aliased1 := make([]byte, l)
			rng.Read(aliased1)
			aliased2 := make([]byte, l)
			copy(aliased2, aliased1)

			mixPCM(aliased1, aliased1, g)
			oldMixPCM(aliased2, aliased2, g)

			if !bytes.Equal(aliased1, aliased2) {
				t.Errorf("mixPCM aliased mismatch for length %d, gain %v", l, g)
			}
		}
	}
}

func TestAddPCMEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	lengths := []int{0, 1, 2, 3, 4, 5, 8, 15, 32, 100, 1024}

	for _, l := range lengths {
		dst1 := make([]byte, l)
		rng.Read(dst1)
		src1 := make([]byte, l)
		rng.Read(src1)

		dst2 := make([]byte, l)
		copy(dst2, dst1)
		src2 := make([]byte, l)
		copy(src2, src1)

		addPCM(dst1, src1)
		oldAddPCM(dst2, src2)

		if !bytes.Equal(dst1, dst2) {
			t.Errorf("addPCM mismatch for length %d", l)
		}
	}
}

func TestApplyEQEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	lengths := []int{0, 1, 2, 3, 4, 5, 8, 15, 16, 32, 100, 1024, 2048}

	gains := [eqBands]float64{2.0, -1.0, 3.0, 0.0, -2.5, 1.5, -3.0, 0.5, 4.0, -5.0}
	configs := []*eqConfig{
		newEQConfig(true, false, 0.0, gains, "custom"),
		newEQConfig(true, true, 0.0, gains, "custom"),
		newEQConfig(false, true, 0.0, gains, "custom"),
		newEQConfig(false, false, 0.0, gains, "custom"),
	}

	for _, l := range lengths {
		for _, cfg := range configs {
			p1 := &Player{}
			p1.eqCfg.Store(cfg)
			p2 := &Player{}
			p2.eqCfg.Store(cfg)

			buf1 := make([]byte, l)
			rng.Read(buf1)
			buf2 := make([]byte, l)
			copy(buf2, buf1)

			p1.applyEQ(buf1)
			oldApplyEQ(p2, buf2)

			if !bytes.Equal(buf1, buf2) {
				t.Errorf("applyEQ mismatch for length %d, config %+v", l, cfg)
			}
		}
	}
}

// ---- Benchmarks ----

func BenchmarkApplyGainOld(b *testing.B) {
	buf := make([]byte, 4096)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		oldApplyGain(buf, 0.5)
	}
}

func BenchmarkApplyGainNew(b *testing.B) {
	buf := make([]byte, 4096)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		applyGain(buf, 0.5)
	}
}

func BenchmarkApplyFadeInOld(b *testing.B) {
	buf := make([]byte, 4096)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		oldApplyFadeIn(buf, 100)
	}
}

func BenchmarkApplyFadeInNew(b *testing.B) {
	buf := make([]byte, 4096)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		applyFadeIn(buf, 100)
	}
}

func BenchmarkApplyFadeOutOld(b *testing.B) {
	buf := make([]byte, 4096)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		oldApplyFadeOut(buf, 100)
	}
}

func BenchmarkApplyFadeOutNew(b *testing.B) {
	buf := make([]byte, 4096)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		applyFadeOut(buf, 100)
	}
}

func BenchmarkMixPCMOld(b *testing.B) {
	dst := make([]byte, 4096)
	src := make([]byte, 4096)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		oldMixPCM(dst, src, 0.5)
	}
}

func BenchmarkMixPCMNew(b *testing.B) {
	dst := make([]byte, 4096)
	src := make([]byte, 4096)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mixPCM(dst, src, 0.5)
	}
}

func BenchmarkAddPCMOld(b *testing.B) {
	dst := make([]byte, 4096)
	src := make([]byte, 4096)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		oldAddPCM(dst, src)
	}
}

func BenchmarkAddPCMNew(b *testing.B) {
	dst := make([]byte, 4096)
	src := make([]byte, 4096)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		addPCM(dst, src)
	}
}

func BenchmarkApplyEQOld(b *testing.B) {
	p := &Player{}
	gains := [eqBands]float64{2.0, -1.0, 3.0, 0.0, -2.5, 1.5, -3.0, 0.5, 4.0, -5.0}
	p.eqCfg.Store(newEQConfig(true, true, 0.0, gains, "custom"))
	buf := make([]byte, 4096)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		oldApplyEQ(p, buf)
	}
}

func BenchmarkApplyEQNew(b *testing.B) {
	p := &Player{}
	gains := [eqBands]float64{2.0, -1.0, 3.0, 0.0, -2.5, 1.5, -3.0, 0.5, 4.0, -5.0}
	p.eqCfg.Store(newEQConfig(true, true, 0.0, gains, "custom"))
	buf := make([]byte, 4096)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.applyEQ(buf)
	}
}
