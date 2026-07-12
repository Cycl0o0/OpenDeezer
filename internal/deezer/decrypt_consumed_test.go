package deezer

import (
	"bytes"
	"testing"
)

// TestStripeConsumedOffset verifies StripeDecryptor.Consumed tracks exactly the
// number of ciphertext bytes fed (full chunks emitted plus the buffered partial),
// which is the HTTP Range offset used to resume a torn CDN download.
func TestStripeConsumedOffset(t *testing.T) {
	d, err := NewStripeDecryptor("3135556")
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Consumed(); got != 0 {
		t.Fatalf("fresh decryptor Consumed=%d, want 0", got)
	}
	// Feed a non-chunk-aligned amount and confirm Consumed == bytes fed.
	total := int64(0)
	var out []byte
	for _, n := range []int{500, chunkSize, 2000, chunkSize*2 + 7} {
		out = d.Feed(make([]byte, n), out[:0])
		total += int64(n)
		if got := d.Consumed(); got != total {
			t.Fatalf("after feeding %d total bytes, Consumed=%d, want %d", total, got, total)
		}
	}
}

// TestStripeResumeEquivalence verifies that splitting the ciphertext at an
// arbitrary (non-chunk-aligned) offset and continuing to feed the SAME decryptor
// from that offset yields output identical to a single-shot decrypt — the
// property the audio engine relies on for Range resume.
func TestStripeResumeEquivalence(t *testing.T) {
	const trackID = "3135556"
	cipher := make([]byte, 40000)
	for i := range cipher {
		cipher[i] = byte((i*5 + 9) % 251)
	}
	want, err := DecryptTrack(trackID, cipher)
	if err != nil {
		t.Fatal(err)
	}

	d, err := NewStripeDecryptor(trackID)
	if err != nil {
		t.Fatal(err)
	}
	var got []byte
	got = d.Feed(cipher[:17777], got) // first piece
	off := d.Consumed()
	if off != 17777 {
		t.Fatalf("Consumed after first piece = %d, want 17777", off)
	}
	got = d.Feed(cipher[off:], got) // resume from the reported offset
	got = d.Finish(got)

	if !bytes.Equal(got, want) {
		t.Fatalf("resumed decrypt differs from single-shot: got %d bytes, want %d", len(got), len(want))
	}
}
