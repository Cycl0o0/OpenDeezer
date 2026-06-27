package audio

import (
	"io"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
)

// flacStream decodes a whole (already-decrypted) FLAC buffer to interleaved
// s16 stereo PCM, satisfying the same Read+Seek contract as go-mp3's decoder so
// the player can treat both uniformly. Deezer HiFi is 16-bit/44100 FLAC; other
// bit depths are shifted to 16-bit (sample rate is assumed 44100, matching the
// oto context).
type flacStream struct {
	s     *flac.Stream
	buf   []byte // leftover interleaved PCM not yet returned
	shift int    // bitsPerSample - 16
}

func newFLACStream(r io.ReadSeeker) (*flacStream, error) {
	s, err := flac.NewSeek(r)
	if err != nil {
		return nil, err
	}
	return &flacStream{s: s, shift: int(s.Info.BitsPerSample) - 16}, nil
}

func (f *flacStream) conv(s int32) int16 {
	if f.shift > 0 {
		s >>= f.shift
	} else if f.shift < 0 {
		s <<= -f.shift
	}
	if s > 32767 {
		s = 32767
	} else if s < -32768 {
		s = -32768
	}
	return int16(s)
}

// framePCM renders one FLAC frame to interleaved s16 stereo bytes (mono is
// duplicated; >2 channels keep the first two). frame.Correlate has already
// undone any inter-channel decorrelation by the time ParseNext returns.
func (f *flacStream) framePCM(fr *frame.Frame) []byte {
	n := int(fr.BlockSize)
	out := make([]byte, n*4) // n * 2ch * 2 bytes
	left := fr.Subframes[0].Samples
	right := left
	if len(fr.Subframes) > 1 {
		right = fr.Subframes[1].Samples
	}
	for i := 0; i < n; i++ {
		l := f.conv(left[i])
		r := f.conv(right[i])
		o := i * 4
		out[o] = byte(l)
		out[o+1] = byte(l >> 8)
		out[o+2] = byte(r)
		out[o+3] = byte(r >> 8)
	}
	return out
}

func (f *flacStream) Read(p []byte) (int, error) {
	for len(f.buf) == 0 {
		fr, err := f.s.ParseNext()
		if err != nil {
			return 0, err // includes io.EOF
		}
		f.buf = f.framePCM(fr)
	}
	n := copy(p, f.buf)
	f.buf = f.buf[n:]
	return n, nil
}

// Seek moves to a PCM byte offset from the start (SeekStart only, like the
// player uses). Converts the byte offset to a sample number and seeks the FLAC
// stream to the frame containing it.
func (f *flacStream) Seek(off int64, whence int) (int64, error) {
	if whence != io.SeekStart {
		return 0, io.ErrUnexpectedEOF
	}
	sample := uint64(off) / uint64(channels*2)
	if _, err := f.s.Seek(sample); err != nil {
		return 0, err
	}
	f.buf = nil
	return off, nil
}
