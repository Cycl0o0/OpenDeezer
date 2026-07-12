package audio

import (
	"bytes"
	"testing"

	"github.com/mewkiz/flac/frame"
)

// TestFramePCMReuseNoAllocs verifies flacStream.framePCM reuses its scratch
// buffer instead of allocating a fresh []byte per frame. framePCM is tested in
// isolation (not through Read) so the measurement excludes go-mp3/mewkiz internal
// allocations in ParseNext and reflects only framePCM's own behavior.
func TestFramePCMReuseNoAllocs(t *testing.T) {
	const block = 4096
	left := make([]int32, block)
	right := make([]int32, block)
	for i := range left {
		left[i] = int32(int16(i * 7))
		right[i] = -left[i]
	}
	fr := &frame.Frame{
		Header: frame.Header{BlockSize: block, BitsPerSample: 16},
		Subframes: []*frame.Subframe{
			{Samples: left, NSamples: block},
			{Samples: right, NSamples: block},
		},
	}
	f := &flacStream{shift: 0}
	f.framePCM(fr) // warm up: first call allocates the reusable buffer

	allocs := testing.AllocsPerRun(1000, func() {
		f.framePCM(fr)
	})
	if allocs != 0 {
		t.Fatalf("framePCM allocated %.1f times/call after warmup; want 0 (buffer should be reused)", allocs)
	}
}

// TestFLACProgressiveDecodeCorrect verifies the FLAC decoder (which now starts
// before the download finishes) yields the same PCM whether it reads from a
// fully-buffered stream or a progressively-fed one — proving the reuse rewrite
// and progressive path preserve output.
func TestFLACProgressiveDecodeCorrect(t *testing.T) {
	data := makeFLAC(20, 4096)

	// Reference: decode from a complete in-memory reader.
	ref, err := newFLACStream(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	want := decodeAll(t, ref)

	// Progressive: feed a streamBuffer in slices, decode concurrently.
	sb := newStreamBuffer()
	go func() {
		for off := 0; off < len(data); off += 4096 {
			end := off + 4096
			if end > len(data) {
				end = len(data)
			}
			sb.append(data[off:end])
		}
		sb.finish(nil)
	}()
	prog, err := newFLACStream(sb)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeAll(t, prog)

	if !bytes.Equal(got, want) {
		t.Fatalf("progressive FLAC decode differs: got %d PCM bytes, want %d", len(got), len(want))
	}
}

func decodeAll(t *testing.T, s pcmStream) []byte {
	t.Helper()
	var out []byte
	buf := make([]byte, 8192)
	for {
		n, err := s.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err != nil {
			return out
		}
	}
}
