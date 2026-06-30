package deezer

import (
	"bytes"
	"testing"
)

// Fuzz the custom BF_CBC_STRIPE decryptor (decrypt.go). Deezer's CDN streams
// arrive as untrusted bytes, so the decrypt path must never panic and must
// preserve some structural invariants on ANY input. Run locally with e.g.:
//
//	go test -run=^$ -fuzz=FuzzDecryptTrack -fuzztime=30s ./internal/deezer
//
// These are also the seeds OSS-Fuzz / ClusterFuzzLite continuously fuzzes.

// FuzzDecryptTrack feeds arbitrary track ids + ciphertext and asserts the
// length-preserving invariant: the stripe scheme decrypts every full 2048-byte
// chunk in place and passes the trailing partial chunk through, so the output
// length always equals the input length.
func FuzzDecryptTrack(f *testing.F) {
	f.Add("3135556", []byte("the quick brown fox jumps over the lazy dog 0123456789"))
	f.Add("0", []byte{})
	f.Add("", make([]byte, 2048*3+17)) // spans several full chunks + a partial
	f.Fuzz(func(t *testing.T, trackID string, data []byte) {
		out, err := DecryptTrack(trackID, data)
		if err != nil {
			return // an unusable key is a clean error, not a crash
		}
		if len(out) != len(data) {
			t.Fatalf("DecryptTrack: len(out)=%d != len(data)=%d (stripe must preserve length)", len(out), len(data))
		}
	})
}

// FuzzStripeChunking asserts the decryptor is independent of how the input is
// chopped into Feed() calls: feeding the whole buffer at once must produce the
// exact same bytes as feeding it as two arbitrary slices. This catches any
// off-by-one or buffering bug in the chunk accumulator.
func FuzzStripeChunking(f *testing.F) {
	f.Add("3135556", []byte("streaming in two halves should equal one whole feed"), uint16(7))
	f.Add("42", make([]byte, 2048*2), uint16(2048))
	f.Fuzz(func(t *testing.T, trackID string, data []byte, splitRaw uint16) {
		whole, err := newStripeOut(trackID, func(d *StripeDecryptor) []byte {
			out := d.Feed(data, nil)
			return d.Finish(out)
		})
		if err != nil {
			return
		}
		split := 0
		if len(data) > 0 {
			split = int(splitRaw) % (len(data) + 1)
		}
		parts, err := newStripeOut(trackID, func(d *StripeDecryptor) []byte {
			out := d.Feed(data[:split], nil)
			out = d.Feed(data[split:], out)
			return d.Finish(out)
		})
		if err != nil {
			return
		}
		if !bytes.Equal(whole, parts) {
			t.Fatalf("stripe output depends on chunking: split=%d len=%d", split, len(data))
		}
	})
}

func newStripeOut(trackID string, fn func(*StripeDecryptor) []byte) ([]byte, error) {
	d, err := NewStripeDecryptor(trackID)
	if err != nil {
		return nil, err
	}
	return fn(d), nil
}
