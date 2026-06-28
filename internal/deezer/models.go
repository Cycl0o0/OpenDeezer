package deezer

import "strings"

// Artist is a track/album credit.
type Artist struct {
	ID   string
	Name string
}

// Track mirrors the C++ core::Track.
type Track struct {
	ID         string
	Name       string
	DurationMS int64
	Artists    []Artist
	AlbumName  string
	ArtworkURL string
	Explicit   bool // explicit lyrics/content (show an "E" badge)
}

// ArtistLine joins artist names: "Artist A, Artist B".
func (t Track) ArtistLine() string {
	names := make([]string, len(t.Artists))
	for i, a := range t.Artists {
		names[i] = a.Name
	}
	return strings.Join(names, ", ")
}

// Album is a search/browse result.
type Album struct {
	ID         string
	Name       string
	Artists    []Artist
	ArtworkURL string
}

// ArtistInfo is an artist profile (search result / browse target).
type ArtistInfo struct {
	ID         string
	Name       string
	ArtworkURL string
	NbFans     int
}

// ArtistPage bundles an artist's profile with their top tracks, albums and
// related artists for a one-shot profile view.
type ArtistPage struct {
	Artist  ArtistInfo
	Top     []Track
	Albums  []Album
	Related []ArtistInfo
}

// LyricLine is one synced lyric line (TimeMS is the line's start offset).
type LyricLine struct {
	TimeMS int64
	Text   string
}

// Lyrics holds a track's plain text plus, when available, time-synced lines.
type Lyrics struct {
	Plain  string
	Synced []LyricLine
}

// IsSynced reports whether time-synced lyrics are present.
func (l Lyrics) IsSynced() bool { return len(l.Synced) > 0 }

// Chart is the global/genre top lists from the public REST /chart endpoint.
type Chart struct {
	Tracks    []Track
	Albums    []Album
	Artists   []ArtistInfo
	Playlists []Playlist
}

// Podcast is a show (search/browse result).
type Podcast struct {
	ID           string
	Name         string
	Description  string
	ArtworkURL   string
	EpisodeCount int
}

// Episode is one podcast episode. Episodes stream as plain (unencrypted) MP3.
type Episode struct {
	ID          string
	Title       string
	Description string
	ArtworkURL  string
	DurationMS  int64
	ReleaseDate string
	PodcastName string
}

// AsTrack adapts an episode to a Track so the existing queue/player/now-playing
// paths can carry it (episodes play via a plain StreamPlan).
func (e Episode) AsTrack() Track {
	return Track{
		ID:         e.ID,
		Name:       e.Title,
		DurationMS: e.DurationMS,
		Artists:    []Artist{{Name: e.PodcastName}},
		AlbumName:  e.PodcastName,
		ArtworkURL: e.ArtworkURL,
	}
}

// Playlist is a search/browse result.
type Playlist struct {
	ID         string
	Name       string
	Owner      string
	TrackCount int
	ArtworkURL string
}

// SearchResults groups the searched entity kinds.
type SearchResults struct {
	Tracks    []Track
	Albums    []Album
	Artists   []ArtistInfo
	Playlists []Playlist
}

// FormatLabel turns a raw Deezer media format into a human label for the UI.
func FormatLabel(raw string) string {
	switch strings.ToUpper(raw) {
	case "":
		return ""
	case "FLAC":
		return "FLAC · lossless"
	case "MP3_320":
		return "MP3 · 320 kbps"
	case "MP3_256":
		return "MP3 · 256 kbps"
	case "MP3_128":
		return "MP3 · 128 kbps"
	case "MP3_64":
		return "MP3 · 64 kbps"
	default:
		return raw
	}
}
