package audio

import (
	"io"
	"math"
)

// resampleStream linearly resamples an interleaved s16 stereo pcmStream from a
// source rate to the fixed output rate (sampleRate, 44100). go-mp3 emits PCM at
// the file's native rate; the output device is fixed at 44100, so a non-44100
// podcast MP3 would otherwise play at the wrong speed and pitch. It satisfies
// the pcmStream (Read + SeekStart) contract so decode() treats it like any
// decoder. It runs on the decode goroutine (never realtime), so the modest
// per-Read buffering here is fine.
type resampleStream struct {
	src  pcmStream
	step float64 // source frames advanced per output frame (srcRate/dstRate)

	in     []byte  // buffered source PCM (interleaved s16 stereo)
	inBase int64   // source-frame index of in[0]
	pos    float64 // source-frame position (absolute) of the next output frame
	srcEOF bool
	rbuf   []byte // scratch for reading from src
}

func newResampleStream(src pcmStream, srcRate int) *resampleStream {
	return &resampleStream{src: src, step: float64(srcRate) / float64(sampleRate)}
}

// fill pulls one chunk from the source into the buffer.
func (rs *resampleStream) fill() {
	if rs.srcEOF {
		return
	}
	if rs.rbuf == nil {
		rs.rbuf = make([]byte, decodeChunk)
	}
	n, err := rs.src.Read(rs.rbuf)
	if n > 0 {
		rs.in = append(rs.in, rs.rbuf[:n]...) // append raw bytes; whole-frame
		// alignment is handled by the readers (a trailing partial frame just waits
		// for the next fill).
	}
	if err != nil {
		rs.srcEOF = true
	}
}

// compactTo drops buffered frames before absolute index idx (bounding memory).
func (rs *resampleStream) compactTo(idx int64) {
	drop := idx - rs.inBase
	if drop <= 0 {
		return
	}
	if bufFrames := int64(len(rs.in) / frameBytes); drop > bufFrames {
		drop = bufFrames // can't drop frames not yet buffered
	}
	rs.in = append(rs.in[:0], rs.in[int(drop)*frameBytes:]...)
	rs.inBase += drop
}

// frameAt returns the interleaved s16 source frame at absolute index idx,
// reading more from the source as needed.
func (rs *resampleStream) frameAt(idx int64) (l, r int16, ok bool) {
	for idx >= rs.inBase+int64(len(rs.in)/frameBytes) && !rs.srcEOF {
		rs.fill()
	}
	rel := idx - rs.inBase
	if rel < 0 || (rel+1)*frameBytes > int64(len(rs.in)) {
		return 0, 0, false
	}
	o := int(rel) * frameBytes
	l = int16(uint16(rs.in[o]) | uint16(rs.in[o+1])<<8)
	r = int16(uint16(rs.in[o+2]) | uint16(rs.in[o+3])<<8)
	return l, r, true
}

func (rs *resampleStream) Read(p []byte) (int, error) {
	n := 0
	for n+frameBytes <= len(p) {
		i0 := int64(math.Floor(rs.pos))
		rs.compactTo(i0)
		l0, r0, ok := rs.frameAt(i0)
		if !ok {
			if n > 0 {
				return n, nil
			}
			return 0, io.EOF
		}
		l1, r1, ok := rs.frameAt(i0 + 1)
		if !ok {
			l1, r1 = l0, r0 // last frame: nothing to interpolate toward
		}
		frac := rs.pos - float64(i0)
		l := int16(float64(l0)*(1-frac) + float64(l1)*frac)
		r := int16(float64(r0)*(1-frac) + float64(r1)*frac)
		p[n] = byte(l)
		p[n+1] = byte(uint16(l) >> 8)
		p[n+2] = byte(r)
		p[n+3] = byte(uint16(r) >> 8)
		n += frameBytes
		rs.pos += rs.step
	}
	return n, nil
}

// Seek moves to a byte offset in the OUTPUT (44100) stream (SeekStart only, like
// the player uses), mapping it back to the corresponding source frame.
func (rs *resampleStream) Seek(off int64, whence int) (int64, error) {
	if whence != io.SeekStart {
		return 0, io.ErrUnexpectedEOF
	}
	if off < 0 {
		off = 0
	}
	dstFrame := off / frameBytes
	srcFrame := int64(float64(dstFrame) * rs.step)
	if _, err := rs.src.Seek(srcFrame*frameBytes, io.SeekStart); err != nil {
		return 0, err
	}
	rs.in = rs.in[:0]
	rs.inBase = srcFrame
	rs.pos = float64(srcFrame)
	rs.srcEOF = false
	return off, nil
}
