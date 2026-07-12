package audio

import (
	"bytes"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"
)

// synth_test.go holds shared helpers that synthesize decodable MP3/FLAC streams
// in memory so the streaming/decode paths can be exercised without network or
// fixture files.

// makeMP3 builds a valid MPEG-1 Layer III, 44.1kHz, 128kbps, stereo stream of
// nFrames frames. The payload is zeroed (decodes to silence), which is all the
// tests need — they assert on byte counts and timing, not audio content.
// go-mp3 accepts and fully decodes these frames.
func makeMP3(nFrames int) []byte {
	// 144 * 128000 / 44100 = 417 bytes per frame (no padding).
	fh := make([]byte, 417)
	fh[0] = 0xFF
	fh[1] = 0xFB // MPEG-1, Layer III, no CRC
	fh[2] = 0x90 // 128kbps, 44.1kHz, no padding
	fh[3] = 0x00 // stereo
	var buf bytes.Buffer
	buf.Grow(nFrames * len(fh))
	for i := 0; i < nFrames; i++ {
		buf.Write(fh)
	}
	return buf.Bytes()
}

// makeFLAC builds a valid 16-bit/44.1kHz stereo FLAC with nFrames verbatim
// frames of the given blockSize. A deterministic sawtooth fills the samples so
// decode output is reproducible.
func makeFLAC(nFrames, blockSize int) []byte {
	info := &meta.StreamInfo{
		BlockSizeMin:  uint16(blockSize),
		BlockSizeMax:  uint16(blockSize),
		SampleRate:    44100,
		NChannels:     2,
		BitsPerSample: 16,
		NSamples:      uint64(nFrames * blockSize),
	}
	var buf bytes.Buffer
	enc, err := flac.NewEncoder(&buf, info)
	if err != nil {
		panic(err)
	}
	for fi := 0; fi < nFrames; fi++ {
		left := make([]int32, blockSize)
		right := make([]int32, blockSize)
		for i := range left {
			v := int32(int16((fi*blockSize + i) * 251))
			left[i] = v
			right[i] = -v
		}
		f := &frame.Frame{
			Header: frame.Header{
				HasFixedBlockSize: true,
				BlockSize:         uint16(blockSize),
				SampleRate:        44100,
				Channels:          frame.ChannelsLR,
				BitsPerSample:     16,
				Num:               uint64(fi),
			},
			Subframes: []*frame.Subframe{
				{SubHeader: frame.SubHeader{Pred: frame.PredVerbatim}, Samples: left, NSamples: blockSize},
				{SubHeader: frame.SubHeader{Pred: frame.PredVerbatim}, Samples: right, NSamples: blockSize},
			},
		}
		if err := enc.WriteFrame(f); err != nil {
			panic(err)
		}
	}
	if err := enc.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// drainStreamBuffer reads a finished streamBuffer to completion and returns all
// its bytes.
func drainStreamBuffer(sb *streamBuffer) []byte {
	var out []byte
	buf := make([]byte, 32*1024)
	for {
		n, err := sb.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err != nil {
			return out
		}
	}
}

// sbDone reports whether the producer has marked the streamBuffer finished
// (in-package access to the unexported flag for progressive-start assertions).
func sbDone(sb *streamBuffer) bool {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.done
}
