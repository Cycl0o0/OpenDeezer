package ui

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Cycl0o0/OpenDeezer/internal/deezer"
)

// configDir is ~/.config/opendeezer.
func configDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "opendeezer"), nil
}

// LoadARL resolves the Deezer ARL from, in order: $DEEZER_ARL, then
// ~/.config/opendeezer/arl.txt. Returns "" if neither is set.
func LoadARL() string {
	if v := strings.TrimSpace(os.Getenv("DEEZER_ARL")); v != "" {
		return v
	}
	dir, err := configDir()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(dir, "arl.txt"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// SaveARL writes the ARL to ~/.config/opendeezer/arl.txt (0600).
func SaveARL(arl string) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "arl.txt"), []byte(strings.TrimSpace(arl)+"\n"), 0600)
}

// LoadQuality reads the persisted quality level: 0=Normal, 1=High, 2=HiFi.
func LoadQuality() int {
	dir, err := configDir()
	if err != nil {
		return 0
	}
	b, err := os.ReadFile(filepath.Join(dir, "quality.txt"))
	if err != nil {
		return 0
	}
	switch strings.TrimSpace(string(b)) {
	case "high":
		return 1
	case "hifi", "flac", "lossless":
		return 2
	default:
		return 0
	}
}

// SaveQuality persists the quality level (0..2).
func SaveQuality(level int) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	v := "normal"
	switch level {
	case 1:
		v = "high"
	case 2:
		v = "hifi"
	}
	return os.WriteFile(filepath.Join(dir, "quality.txt"), []byte(v+"\n"), 0600)
}

// boolFile reads a "1"/"0" toggle file (default false).
func boolFile(name string) bool {
	dir, err := configDir()
	if err != nil {
		return false
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(b)) == "1"
}

func saveBoolFile(name string, v bool) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	s := "0"
	if v {
		s = "1"
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(s+"\n"), 0600)
}

// LoadReplayGain / SaveReplayGain persist the loudness-normalization toggle.
func LoadReplayGain() bool        { return boolFile("replaygain.txt") }
func SaveReplayGain(v bool) error { return saveBoolFile("replaygain.txt", v) }

// LoadGapless / SaveGapless persist the gapless toggle (default: on).
func LoadGapless() bool {
	dir, err := configDir()
	if err != nil {
		return true
	}
	b, err := os.ReadFile(filepath.Join(dir, "gapless.txt"))
	if err != nil {
		return true // default on
	}
	return strings.TrimSpace(string(b)) != "0"
}
func SaveGapless(v bool) error { return saveBoolFile("gapless.txt", v) }

// LoadCrossfadeMS / SaveCrossfadeMS persist the crossfade duration in ms.
func LoadCrossfadeMS() int {
	dir, err := configDir()
	if err != nil {
		return 0
	}
	b, err := os.ReadFile(filepath.Join(dir, "crossfade.txt"))
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if n < 0 {
		n = 0
	}
	return n
}
func SaveCrossfadeMS(ms int) error {
	if ms < 0 {
		ms = 0
	}
	return saveStringFile("crossfade.txt", strconv.Itoa(ms))
}

func saveStringFile(name, v string) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(v+"\n"), 0600)
}

// ControlConfig holds the control-API settings (remote control + MCP).
type ControlConfig struct {
	Enabled     bool
	Addr        string // host:port; "" -> 127.0.0.1:7654
	Token       string // bearer token ("" = no auth, localhost only)
	SameAccount bool   // require a matching Deezer account when no token (LAN)
}

// LoadControl reads the control-API config: $OPENDEEZER_CONTROL ("1"/addr) +
// $OPENDEEZER_CONTROL_TOKEN, else ~/.config/opendeezer/{control.txt,control-token.txt}.
func LoadControl() ControlConfig {
	c := ControlConfig{Addr: "127.0.0.1:7654"}
	v := strings.TrimSpace(os.Getenv("OPENDEEZER_CONTROL"))
	if v == "" {
		if dir, err := configDir(); err == nil {
			if b, e := os.ReadFile(filepath.Join(dir, "control.txt")); e == nil {
				v = strings.TrimSpace(string(b))
			}
		}
	}
	switch {
	case v == "":
		return c
	case v == "1" || strings.EqualFold(v, "on") || strings.EqualFold(v, "true"):
		c.Enabled = true
	case v == "0" || strings.EqualFold(v, "off"):
		c.Enabled = false
	default:
		c.Enabled = true
		c.Addr = v // an explicit host:port
	}
	c.Token = strings.TrimSpace(os.Getenv("OPENDEEZER_CONTROL_TOKEN"))
	if c.Token == "" {
		if dir, err := configDir(); err == nil {
			if b, e := os.ReadFile(filepath.Join(dir, "control-token.txt")); e == nil {
				c.Token = strings.TrimSpace(string(b))
			}
		}
	}
	// When bound to a non-loopback address (LAN remote) with no token, default
	// to same-account auth: the user's own devices (same Deezer login) connect
	// without copying a token; foreign accounts are rejected. Override with
	// $OPENDEEZER_CONTROL_SAMEACCOUNT=0.
	if c.Enabled && c.Token == "" && !isLoopbackAddr(c.Addr) {
		c.SameAccount = true
	}
	if v := strings.TrimSpace(os.Getenv("OPENDEEZER_CONTROL_SAMEACCOUNT")); v != "" {
		c.SameAccount = v == "1" || strings.EqualFold(v, "on") || strings.EqualFold(v, "true")
	}
	return c
}

// isLoopbackAddr reports whether a host:port binds only the loopback interface.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	switch host {
	case "", "0.0.0.0", "::":
		return false // wildcard = all interfaces
	case "localhost":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// LoadDiscordAppID returns the Discord application id for Rich Presence, from
// $OPENDEEZER_DISCORD_APP_ID or ~/.config/opendeezer/discord-app-id.txt. Empty
// disables the feature.
func LoadDiscordAppID() string {
	if v := strings.TrimSpace(os.Getenv("OPENDEEZER_DISCORD_APP_ID")); v != "" {
		return v
	}
	dir, err := configDir()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(dir, "discord-app-id.txt"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// LoadAudioDevice / SaveAudioDevice persist the selected output device id.
func LoadAudioDevice() string {
	dir, err := configDir()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(dir, "device.txt"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
func SaveAudioDevice(id string) error { return saveStringFile("device.txt", id) }

// LoadLastPeer / SaveLastPeer remember the last remote-control peer address so
// the connect screen can prefill it.
func LoadLastPeer() string {
	dir, err := configDir()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(dir, "remote-peer.txt"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
func SaveLastPeer(addr string) error { return saveStringFile("remote-peer.txt", addr) }

// LoadTheme returns the saved theme name ("" if none).
func LoadTheme() string {
	dir, err := configDir()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(dir, "theme.txt"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// SaveTheme persists the theme name.
func SaveTheme(name string) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "theme.txt"), []byte(name+"\n"), 0600)
}

// ResumeState is the last-played track plus the position to resume from.
type ResumeState struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ArtistLine string `json:"artistLine"`
	AlbumName  string `json:"albumName"`
	ArtworkURL string `json:"artworkUrl"`
	DurationMS int64  `json:"durationMs"`
	PositionMS int64  `json:"positionMs"`
}

// Track reconstructs a deezer.Track from the saved state.
func (r ResumeState) Track() deezer.Track {
	return deezer.Track{
		ID:         r.ID,
		Name:       r.Name,
		DurationMS: r.DurationMS,
		Artists:    []deezer.Artist{{Name: r.ArtistLine}},
		AlbumName:  r.AlbumName,
		ArtworkURL: r.ArtworkURL,
	}
}

// SaveResume writes the current track + position to resume.json. Positions in
// the first few seconds are treated as "start over" and clear the state.
func SaveResume(t deezer.Track, positionMS int64) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dir, "resume.json")
	if t.ID == "" || positionMS < 3000 {
		_ = os.Remove(path)
		return nil
	}
	rs := ResumeState{
		ID: t.ID, Name: t.Name, ArtistLine: t.ArtistLine(), AlbumName: t.AlbumName,
		ArtworkURL: t.ArtworkURL, DurationMS: t.DurationMS, PositionMS: positionMS,
	}
	b, err := json.Marshal(rs)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}

// LoadResume reads the saved resume state, or nil if none/invalid.
func LoadResume() *ResumeState {
	dir, err := configDir()
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(dir, "resume.json"))
	if err != nil {
		return nil
	}
	var rs ResumeState
	if json.Unmarshal(b, &rs) != nil || rs.ID == "" {
		return nil
	}
	return &rs
}
