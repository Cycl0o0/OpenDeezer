package audio

import (
	"bytes"
	"testing"
)

// Fuzz the FLAC decode path (flac.go). The engine decodes whatever bytes the
// CDN (or, for podcasts, an off-Deezer host) returns, so a malformed stream
// must never panic the player. Run locally with:
//
//	go test -run=^$ -fuzz=FuzzFLACDecode -fuzztime=60s ./internal/audio
//
// Also fuzzed continuously by OSS-Fuzz / ClusterFuzzLite.
func FuzzFLACDecode(f *testing.F) {
	f.Add([]byte("fLaC"))               // FLAC magic, then truncated
	f.Add([]byte("fLaC\x00\x00\x00\x22")) // magic + a bogus STREAMINFO header
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		st, err := newFLACStream(bytes.NewReader(data))
		if err != nil {
			return // not a valid FLAC stream → clean error, not a crash
		}
		buf := make([]byte, 4096)
		// Drain a bounded number of frames; a panic here (e.g. an out-of-range
		// subframe index in framePCM on a malformed frame) is the bug we hunt.
		for i := 0; i < 4096; i++ {
			if _, err := st.Read(buf); err != nil {
				break
			}
		}
	})
}
