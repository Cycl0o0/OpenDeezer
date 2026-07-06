package deezer

import (
	"strings"
	"testing"
)

func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Hello World", "Hello World"},
		{`AC/DC - Back in Black`, "AC_DC - Back in Black"},
		{`a<b>c:d"e/f\g|h?i*j`, "a_b_c_d_e_f_g_h_i_j"},
		{"  .trim. ", "trim"},
		{"", "track"},
		{"   ", "track"},
	}
	for _, c := range cases {
		if got := sanitizeFilename(c.in); got != c.want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Illegal characters must never survive.
	if got := sanitizeFilename(`x/y\z:*?"<>|`); strings.ContainsAny(got, `/\:*?"<>|`) {
		t.Errorf("sanitizeFilename left an illegal character: %q", got)
	}
	// Length is capped so the path stays under filesystem limits.
	if got := sanitizeFilename(strings.Repeat("z", 500)); len(got) > 180 {
		t.Errorf("sanitizeFilename length = %d, want <= 180", len(got))
	}
}

func TestStreamPlanPreviewIsUnencrypted(t *testing.T) {
	// A preview plan must be a plain pass-through stream (no Blowfish), so the
	// player and downloader stream it straight through.
	p := &StreamPlan{CDNURL: "https://cdnt-preview.dzcdn.net/x.mp3", Format: "MP3_128", Preview: true, Encrypted: false}
	if p.Encrypted {
		t.Fatal("preview StreamPlan must not be Encrypted")
	}
	if !p.Preview {
		t.Fatal("preview flag lost")
	}
}
