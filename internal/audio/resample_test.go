package audio

import (
	"io"
	"testing"
)

// memPCM is an in-memory interleaved-s16 pcmStream for testing the resampler.
type memPCM struct {
	buf []byte
	pos int
}

func (m *memPCM) Read(p []byte) (int, error) {
	if m.pos >= len(m.buf) {
		return 0, io.EOF
	}
	n := copy(p, m.buf[m.pos:])
	m.pos += n
	return n, nil
}

func (m *memPCM) Seek(off int64, whence int) (int64, error) {
	if whence != io.SeekStart {
		return 0, io.ErrUnexpectedEOF
	}
	if off < 0 {
		off = 0
	}
	if off > int64(len(m.buf)) {
		off = int64(len(m.buf))
	}
	m.pos = int(off)
	return off, nil
}

// makeConst builds `frames` stereo s16 frames all equal to v.
func makeConst(frames int, v int16) []byte {
	b := make([]byte, frames*frameBytes)
	for i := 0; i < frames; i++ {
		o := i * frameBytes
		b[o] = byte(v)
		b[o+1] = byte(uint16(v) >> 8)
		b[o+2] = byte(v)
		b[o+3] = byte(uint16(v) >> 8)
	}
	return b
}

func TestResampleUpsampleFrameCount(t *testing.T) {
	// 22050 Hz -> 44100 Hz doubles the frame count. Feed 1s of a constant signal;
	// expect ~2s out, and (linear interp of a constant) every sample unchanged.
	const srcRate = 22050
	const srcFrames = srcRate // ~1s
	const v int16 = 10000
	rs := newResampleStream(&memPCM{buf: makeConst(srcFrames, v)}, srcRate)

	out := make([]byte, 8192)
	total := 0
	for {
		n, err := rs.Read(out)
		for i := 0; i+1 < n; i += 2 {
			s := int16(uint16(out[i]) | uint16(out[i+1])<<8)
			if s != v {
				t.Fatalf("constant signal changed after resample: got %d want %d", s, v)
			}
		}
		total += n
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
	}
	gotFrames := total / frameBytes
	want := srcFrames * sampleRate / srcRate
	if diff := gotFrames - want; diff < -4 || diff > 4 {
		t.Fatalf("output frames = %d, want ~%d (srcFrames=%d)", gotFrames, want, srcFrames)
	}
}

func TestResampleDownsampleFrameCount(t *testing.T) {
	// 48000 Hz -> 44100 Hz shrinks the frame count proportionally.
	const srcRate = 48000
	const srcFrames = srcRate // ~1s
	rs := newResampleStream(&memPCM{buf: makeConst(srcFrames, -5000)}, srcRate)

	out := make([]byte, 4096)
	total := 0
	for {
		n, err := rs.Read(out)
		total += n
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
	}
	gotFrames := total / frameBytes
	want := srcFrames * sampleRate / srcRate
	if diff := gotFrames - want; diff < -4 || diff > 4 {
		t.Fatalf("output frames = %d, want ~%d", gotFrames, want)
	}
}

func TestResampleSeek(t *testing.T) {
	// A ramp lets us verify Seek lands near the right source position: source
	// sample s has value s (mod wrap), so after seeking to output byte offset the
	// first decoded sample should reflect the corresponding source frame.
	const srcRate = 48000
	const srcFrames = 20000
	buf := make([]byte, srcFrames*frameBytes)
	for i := 0; i < srcFrames; i++ {
		v := int16(i % 1000)
		o := i * frameBytes
		buf[o] = byte(v)
		buf[o+1] = byte(uint16(v) >> 8)
		buf[o+2] = byte(v)
		buf[o+3] = byte(uint16(v) >> 8)
	}
	rs := newResampleStream(&memPCM{buf: buf}, srcRate)

	// Seek to output frame 5000 -> source frame ~ 5000 * 48000/44100.
	dstFrame := int64(5000)
	if _, err := rs.Seek(dstFrame*frameBytes, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	out := make([]byte, frameBytes)
	n, err := rs.Read(out)
	if err != nil && err != io.EOF {
		t.Fatalf("Read after seek: %v", err)
	}
	if n < frameBytes {
		t.Fatalf("short read after seek: %d", n)
	}
	got := int16(uint16(out[0]) | uint16(out[1])<<8)
	wantSrc := int16((int64(float64(dstFrame)*rs.step) % 1000))
	if d := got - wantSrc; d < -2 || d > 2 {
		t.Fatalf("first sample after seek = %d, want ~%d", got, wantSrc)
	}
}
