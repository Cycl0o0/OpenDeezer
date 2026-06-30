package deezer_test

import (
	"bytes"
	"testing"

	dz "github.com/Cycl0o0/OpenDeezer/sdk/deezer"
)

func TestNew(t *testing.T) {
	c := dz.New("test-arl")
	if c == nil {
		t.Fatal("New returned nil")
	}
	if c.LoggedIn() {
		t.Error("freshly created client should not be logged in")
	}
}

func TestQualityConstants(t *testing.T) {
	c := dz.New("arl")
	c.SetQuality(dz.QualityLossless)
	if c.Quality() != dz.QualityLossless {
		t.Errorf("SetQuality(%d): Quality()=%d", dz.QualityLossless, c.Quality())
	}
	c.SetQuality(dz.QualityNormal)
	if c.Quality() != dz.QualityNormal {
		t.Errorf("SetQuality(%d): Quality()=%d", dz.QualityNormal, c.Quality())
	}
}

func TestFormatLabel(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"FLAC", "FLAC · lossless"},
		{"MP3_320", "MP3 · 320 kbps"},
		{"MP3_128", "MP3 · 128 kbps"},
		{"MP3_256", "MP3 · 256 kbps"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := dz.FormatLabel(tc.raw); got != tc.want {
			t.Errorf("FormatLabel(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestTrackIDOf(t *testing.T) {
	cases := []struct{ uri, want string }{
		{"deezer:track:3135556", "3135556"},
		{"https://www.deezer.com/track/3135556", "3135556"},
		{"3135556", "3135556"},
	}
	for _, tc := range cases {
		if got := dz.TrackIDOf(tc.uri); got != tc.want {
			t.Errorf("TrackIDOf(%q) = %q, want %q", tc.uri, got, tc.want)
		}
	}
}

// TestBlowfishKey verifies the known key-derivation vector from the internal tests.
func TestBlowfishKey(t *testing.T) {
	want := []byte{108, 108, 102, 107, 57, 102, 44, 55, 101, 37, 117, 96, 60, 100, 52, 57}
	got := dz.BlowfishKey("3135556")
	if !bytes.Equal(got, want) {
		t.Fatalf("BlowfishKey(3135556):\n got  %v\n want %v", got, want)
	}
}

// TestDecryptBytes verifies that DecryptBytes is consistent with the internal
// stripe decryption: chunks 1 and 2 (index%3 != 0) pass through unchanged.
func TestDecryptBytes(t *testing.T) {
	const chunkSize = 2048
	data := make([]byte, chunkSize*3)
	for i := range data {
		data[i] = byte(i % 251)
	}
	out, err := dz.DecryptBytes("3135556", data)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(data) {
		t.Fatalf("length changed: %d -> %d", len(data), len(out))
	}
	// Chunks 1 and 2 are plaintext (not decrypted).
	if !bytes.Equal(out[chunkSize:], data[chunkSize:]) {
		t.Error("chunks 1 and 2 should be unchanged (plaintext)")
	}
	// Chunk 0 should be transformed.
	if bytes.Equal(out[:chunkSize], data[:chunkSize]) {
		t.Error("chunk 0 should be decrypted (different from input)")
	}
}

func TestUnwrap(t *testing.T) {
	c := dz.New("arl")
	if dz.Unwrap(c) == nil {
		t.Error("Unwrap should return the inner client")
	}
	if dz.Unwrap(nil) != nil {
		t.Error("Unwrap(nil) should return nil")
	}
}
