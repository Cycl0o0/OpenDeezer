package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// useTempConfigDir redirects the config dir (and the ~/.config fallback) into a
// fresh temp dir on every platform (darwin/linux/windows) and returns it.
func useTempConfigDir(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("AppData", filepath.Join(tmp, "AppData"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
	return tmp
}

func TestIsLoopbackAddr(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:7654", true},
		{"localhost:7654", true},
		{"[::1]:7654", true},
		{"192.168.1.5:7654", false},
		{":7654", false},
		{"0.0.0.0:7654", false},
	}
	for _, c := range cases {
		if got := isLoopbackAddr(c.addr); got != c.want {
			t.Errorf("isLoopbackAddr(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}

func TestLoadControlEnv(t *testing.T) {
	t.Setenv("OPENDEEZER_CONTROL", ":7654")
	t.Setenv("OPENDEEZER_CONTROL_TOKEN", "")
	c := LoadControl()
	if !c.Enabled || c.Addr != ":7654" {
		t.Fatalf("LoadControl = %+v", c)
	}
	if !c.SameAccount {
		t.Fatal("LAN bind without token should default to same-account auth")
	}

	t.Setenv("OPENDEEZER_CONTROL", "1")
	c = LoadControl()
	if !c.Enabled || c.Addr != "127.0.0.1:7654" || c.SameAccount {
		t.Fatalf("localhost LoadControl = %+v", c)
	}
}

func TestLoadControlDisableValues(t *testing.T) {
	t.Setenv("OPENDEEZER_CONTROL_TOKEN", "")
	for _, v := range []string{"0", "off", "false", "no", "FALSE", "No"} {
		t.Setenv("OPENDEEZER_CONTROL", v)
		if c := LoadControl(); c.Enabled {
			t.Fatalf("OPENDEEZER_CONTROL=%q should disable, got %+v", v, c)
		}
	}
}

func TestNormalizePeer(t *testing.T) {
	cases := []struct {
		in, hostport string
	}{
		{"host", "host:7654"},
		{"host:9000", "host:9000"},
		{"http://host:9000", "host:9000"},
		{"192.168.1.5", "192.168.1.5:7654"},
		{"fd7a:115c:a1e0::42", "[fd7a:115c:a1e0::42]:7654"},
		{"[::1]", "[::1]:7654"},
		{"[::1]:7654", "[::1]:7654"},
		{"::1", "[::1]:7654"},
	}
	for _, c := range cases {
		base, hp := NormalizePeer(c.in)
		if hp != c.hostport || base != "http://"+c.hostport {
			t.Errorf("NormalizePeer(%q) = %q,%q want http://%s,%s", c.in, base, hp, c.hostport, c.hostport)
		}
	}
	if base, hp := NormalizePeer("  "); base != "" || hp != "" {
		t.Errorf("NormalizePeer(empty) = %q,%q want empty", base, hp)
	}
}

// TestMediaConfigRoundTrip verifies the media.json defaults, round-trip and the
// negative-size clamp on both save and load.
func TestMediaConfigRoundTrip(t *testing.T) {
	useTempConfigDir(t)

	if m := LoadMedia(); m.MediaCacheMB != 0 {
		t.Fatalf("default MediaCacheMB = %d, want 0 (cache disabled)", m.MediaCacheMB)
	}
	if err := SaveMedia(Media{MediaCacheMB: 512}); err != nil {
		t.Fatal(err)
	}
	if m := LoadMedia(); m.MediaCacheMB != 512 {
		t.Fatalf("MediaCacheMB after save = %d, want 512", m.MediaCacheMB)
	}
	if err := SaveMedia(Media{MediaCacheMB: -3}); err != nil {
		t.Fatal(err)
	}
	if m := LoadMedia(); m.MediaCacheMB != 0 {
		t.Fatalf("negative MediaCacheMB not clamped on save: %d", m.MediaCacheMB)
	}

	// A hand-edited negative value is clamped on load too.
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "media.json"), []byte(`{"mediaCacheMB":-42}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if m := LoadMedia(); m.MediaCacheMB != 0 {
		t.Fatalf("negative MediaCacheMB not clamped on load: %d", m.MediaCacheMB)
	}
}

// TestEnsureMediaWritesDefaultsOnce verifies that the first launch creates an
// editable media.json holding the defaults, and that an existing file is kept.
func TestEnsureMediaWritesDefaultsOnce(t *testing.T) {
	useTempConfigDir(t)
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "media.json")

	if m := EnsureMedia(); m != (Media{}) {
		t.Fatalf("EnsureMedia on first launch = %+v, want the defaults", m)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("media.json not created on first launch: %v", err)
	}
	var onDisk map[string]any
	if err := json.Unmarshal(b, &onDisk); err != nil {
		t.Fatalf("generated media.json is not valid JSON: %v\n%s", err, b)
	}
	if v, ok := onDisk["mediaCacheMB"]; !ok || v != float64(0) {
		t.Fatalf("generated media.json = %s, want mediaCacheMB 0", b)
	}

	// The user's own file — even one that doesn't parse — is never rewritten.
	for _, contents := range []string{`{"mediaCacheMB":1024}`, `{not json`} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		m := EnsureMedia()
		if b, _ := os.ReadFile(path); string(b) != contents {
			t.Fatalf("EnsureMedia rewrote media.json: got %q, want %q", b, contents)
		}
		if contents == `{"mediaCacheMB":1024}` && m.MediaCacheMB != 1024 {
			t.Fatalf("EnsureMedia = %+v, want the saved 1024 MB", m)
		}
	}
}

// TestEnsureMediaKeepsFallbackFile: a media.json that only exists in the
// ~/.config/opendeezer fallback must not get a default written in front of it
// in the primary config dir (which readFile would then prefer).
func TestEnsureMediaKeepsFallbackFile(t *testing.T) {
	tmp := useTempConfigDir(t)
	// Keep the primary dir apart from ~/.config on Linux too.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
	fallback := filepath.Join(tmp, ".config", "opendeezer")
	if err := os.MkdirAll(fallback, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fallback, "media.json"), []byte(`{"mediaCacheMB":256}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if m := EnsureMedia(); m.MediaCacheMB != 256 {
		t.Fatalf("EnsureMedia = %+v, want the fallback file's 256 MB", m)
	}
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "media.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("EnsureMedia wrote a default in front of the fallback file (stat: %v)", err)
	}
}

// TestVolumeRoundTrip covers the persisted volume: nothing saved by default,
// clamping and rounding on save, clamping on load, and unreadable values.
func TestVolumeRoundTrip(t *testing.T) {
	useTempConfigDir(t)
	if v, ok := LoadVolume(); ok {
		t.Fatalf("LoadVolume in a fresh config dir = %v, want nothing saved", v)
	}
	for _, c := range []struct{ save, want float64 }{
		{0.35, 0.35},
		{0.25 + 0.05000001, 0.3}, // slider arithmetic is rounded to 0.001
		{-0.5, 0},
		{7, 1},
	} {
		if err := SaveVolume(c.save); err != nil {
			t.Fatal(err)
		}
		if v, ok := LoadVolume(); !ok || v != c.want {
			t.Fatalf("SaveVolume(%v) then LoadVolume = %v, %v; want %v, true", c.save, v, ok, c.want)
		}
	}
	if err := SaveVolume(math.NaN()); err == nil {
		t.Fatal("SaveVolume(NaN) succeeded")
	}

	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "volume.txt")
	for _, bad := range []string{"loud", "NaN", "+Inf", ""} {
		if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if v, ok := LoadVolume(); ok {
			t.Fatalf("LoadVolume accepted %q as %v", bad, v)
		}
	}
	// A hand-edited out-of-range level is clamped on load.
	if err := os.WriteFile(path, []byte("1.5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if v, ok := LoadVolume(); !ok || v != 1 {
		t.Fatalf("LoadVolume of 1.5 = %v, %v; want 1, true", v, ok)
	}
}

func TestLoadDiscordAppIDEnv(t *testing.T) {
	t.Setenv("OPENDEEZER_DISCORD_APP_ID", "12345")
	if LoadDiscordAppID() != "12345" {
		t.Fatal("env app id not read")
	}
}
