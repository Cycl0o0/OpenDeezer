package audio

import (
	"io"

	"github.com/hajimehoshi/go-mp3"
)

// readerOnly hides an io.ReadSeeker's Seek method so go-mp3 treats the source as
// a plain stream. go-mp3's NewDecoder, given a seekable source, scans the whole
// stream at construction to build a frame index (for Length + Seek) — which
// forces a full download before the first sample. Constructing over a non-seeker
// instead reads only the first frame, so decoding can begin on a watermark and
// proceed progressively as bytes arrive.
type readerOnly struct{ r io.Reader }

func (ro readerOnly) Read(p []byte) (int, error) { return ro.r.Read(p) }

// mp3Stream adapts go-mp3 to the pcmStream (Read + SeekStart) contract with a
// progressive-then-seekable strategy:
//
//   - Construction builds the decoder over a non-seeking view of the streamBuffer
//     so the first sample is available almost immediately (no full-file scan).
//   - The first Seek "upgrades": it blocks for the complete download (the frame
//     index can't be built from a partial stream), rebuilds a seekable decoder,
//     and seeks it. Subsequent seeks reuse that decoder and are instant.
//
// This gives fast time-to-first-audio for the common case (sequential playback)
// while keeping seek correct — it degrades to "block until fully downloaded",
// which is exactly the old always-full-download behaviour but only when the user
// actually seeks.
type mp3Stream struct {
	sb       *streamBuffer
	dec      *mp3.Decoder
	rate     int
	upgraded bool // dec was rebuilt seekable (post full-download); Seek works
}

func newMP3Stream(sb *streamBuffer) (*mp3Stream, error) {
	d, err := mp3.NewDecoder(readerOnly{sb})
	if err != nil {
		return nil, err
	}
	return &mp3Stream{sb: sb, dec: d, rate: d.SampleRate()}, nil
}

// SampleRate reports the decoded stream's native sample rate (from the first
// frame). decode() wraps mp3Stream in a resampler when it differs from 44100.
func (m *mp3Stream) SampleRate() int { return m.rate }

func (m *mp3Stream) Read(p []byte) (int, error) { return m.dec.Read(p) }

func (m *mp3Stream) Seek(off int64, whence int) (int64, error) {
	if !m.upgraded {
		// Building go-mp3's frame index needs the whole stream; block until the
		// download completes (graceful degradation to the pre-progressive path).
		m.sb.waitDone()
		if _, err := m.sb.Seek(0, io.SeekStart); err != nil {
			return 0, err
		}
		nd, err := mp3.NewDecoder(m.sb) // seekable: scans + indexes frames
		if err != nil {
			return 0, err
		}
		m.dec = nd
		m.upgraded = true
	}
	return m.dec.Seek(off, whence)
}
