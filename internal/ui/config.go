package ui

import (
	"encoding/json"
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
