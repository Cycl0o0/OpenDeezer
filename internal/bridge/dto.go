// Package bridge defines the stable JSON wire types shared by the native
// bindings. Keep field order and JSON tags stable: native clients depend on
// this exact shape.
package bridge

import "github.com/Cycl0o0/OpenDeezer/v3/internal/deezer"

// Artist is a track or album credit on the native JSON wire.
type Artist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Track is a track on the native JSON wire.
type Track struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	DurationMS int64    `json:"durationMs"`
	Artists    []Artist `json:"artists"`
	ArtistLine string   `json:"artistLine"`
	ArtistID   string   `json:"artistId,omitempty"`
	AlbumName  string   `json:"albumName"`
	ArtworkURL string   `json:"artworkUrl"`
	Explicit   bool     `json:"explicit"`
}

// Album is an album on the native JSON wire.
type Album struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Artists    []Artist `json:"artists"`
	ArtworkURL string   `json:"artworkUrl"`
}

// Playlist is a playlist on the native JSON wire.
type Playlist struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Owner      string `json:"owner"`
	TrackCount int    `json:"trackCount"`
	ArtworkURL string `json:"artworkUrl"`
}

// ArtistInfo is an artist search result or profile on the native JSON wire.
type ArtistInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ArtworkURL string `json:"artworkUrl"`
	NbFans     int    `json:"nbFans"`
}

// Podcast is a podcast on the native JSON wire.
type Podcast struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	ArtworkURL   string `json:"artworkUrl"`
	EpisodeCount int    `json:"episodeCount"`
}

// Episode is a podcast episode on the native JSON wire.
type Episode struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	ArtworkURL  string `json:"artworkUrl"`
	DurationMS  int64  `json:"durationMs"`
	ReleaseDate string `json:"releaseDate"`
	PodcastName string `json:"podcastName"`
}

// FromArtist converts a Deezer artist to its native wire representation.
func FromArtist(a deezer.Artist) Artist {
	return Artist{ID: a.ID, Name: a.Name}
}

// FromArtists converts Deezer artists to their native wire representation.
// It deliberately returns a non-nil empty slice for nil input so JSON remains
// [] rather than null, matching the original binding implementations.
func FromArtists(artists []deezer.Artist) []Artist {
	out := make([]Artist, len(artists))
	for i, artist := range artists {
		out[i] = FromArtist(artist)
	}
	return out
}

// FromTrack converts a Deezer track to its native wire representation.
func FromTrack(track deezer.Track) Track {
	artistID := ""
	if len(track.Artists) > 0 {
		artistID = track.Artists[0].ID
	}
	return Track{
		ID:         track.ID,
		Name:       track.Name,
		DurationMS: track.DurationMS,
		Artists:    FromArtists(track.Artists),
		ArtistLine: track.ArtistLine(),
		ArtistID:   artistID,
		AlbumName:  track.AlbumName,
		ArtworkURL: track.ArtworkURL,
		Explicit:   track.Explicit,
	}
}

// FromTracks converts Deezer tracks to their native wire representation.
func FromTracks(tracks []deezer.Track) []Track {
	out := make([]Track, len(tracks))
	for i, track := range tracks {
		out[i] = FromTrack(track)
	}
	return out
}

// FromAlbum converts a Deezer album to its native wire representation.
func FromAlbum(album deezer.Album) Album {
	return Album{
		ID:         album.ID,
		Name:       album.Name,
		Artists:    FromArtists(album.Artists),
		ArtworkURL: album.ArtworkURL,
	}
}

// FromAlbums converts Deezer albums to their native wire representation.
func FromAlbums(albums []deezer.Album) []Album {
	out := make([]Album, len(albums))
	for i, album := range albums {
		out[i] = FromAlbum(album)
	}
	return out
}

// FromPlaylist converts a Deezer playlist to its native wire representation.
func FromPlaylist(playlist deezer.Playlist) Playlist {
	return Playlist{
		ID:         playlist.ID,
		Name:       playlist.Name,
		Owner:      playlist.Owner,
		TrackCount: playlist.TrackCount,
		ArtworkURL: playlist.ArtworkURL,
	}
}

// FromPlaylists converts Deezer playlists to their native wire representation.
func FromPlaylists(playlists []deezer.Playlist) []Playlist {
	out := make([]Playlist, len(playlists))
	for i, playlist := range playlists {
		out[i] = FromPlaylist(playlist)
	}
	return out
}

// FromArtistInfo converts Deezer artist information to its native wire
// representation.
func FromArtistInfo(artist deezer.ArtistInfo) ArtistInfo {
	return ArtistInfo{
		ID:         artist.ID,
		Name:       artist.Name,
		ArtworkURL: artist.ArtworkURL,
		NbFans:     artist.NbFans,
	}
}

// FromArtistInfos converts Deezer artist information to its native wire
// representation.
func FromArtistInfos(artists []deezer.ArtistInfo) []ArtistInfo {
	out := make([]ArtistInfo, len(artists))
	for i, artist := range artists {
		out[i] = FromArtistInfo(artist)
	}
	return out
}

// FromPodcast converts a Deezer podcast to its native wire representation.
func FromPodcast(podcast deezer.Podcast) Podcast {
	return Podcast{
		ID:           podcast.ID,
		Name:         podcast.Name,
		Description:  podcast.Description,
		ArtworkURL:   podcast.ArtworkURL,
		EpisodeCount: podcast.EpisodeCount,
	}
}

// FromPodcasts converts Deezer podcasts to their native wire representation.
func FromPodcasts(podcasts []deezer.Podcast) []Podcast {
	out := make([]Podcast, len(podcasts))
	for i, podcast := range podcasts {
		out[i] = FromPodcast(podcast)
	}
	return out
}

// FromEpisode converts a Deezer podcast episode to its native wire
// representation.
func FromEpisode(episode deezer.Episode) Episode {
	return Episode{
		ID:          episode.ID,
		Title:       episode.Title,
		Description: episode.Description,
		ArtworkURL:  episode.ArtworkURL,
		DurationMS:  episode.DurationMS,
		ReleaseDate: episode.ReleaseDate,
		PodcastName: episode.PodcastName,
	}
}

// FromEpisodes converts Deezer podcast episodes to their native wire
// representation.
func FromEpisodes(episodes []deezer.Episode) []Episode {
	out := make([]Episode, len(episodes))
	for i, episode := range episodes {
		out[i] = FromEpisode(episode)
	}
	return out
}
