package deezer

import (
	internaldeezer "github.com/Cycl0o0/OpenDeezer/internal/deezer"
)

// ---- type aliases ----
// Re-export every public model from internal/deezer so callers import only
// this package and never reference the internal path.

// Account summarises the logged-in user's plan and entitlements.
type Account = internaldeezer.Account

// Artist is a track/album credit.
type Artist = internaldeezer.Artist

// ArtistInfo is an artist profile returned by search or browse.
type ArtistInfo = internaldeezer.ArtistInfo

// ArtistPage bundles an artist's profile with their top tracks, albums and
// related artists.
type ArtistPage = internaldeezer.ArtistPage

// Album is a search/browse result.
type Album = internaldeezer.Album

// Chart is the global or genre top lists.
type Chart = internaldeezer.Chart

// Episode is one podcast episode. Episodes stream as unencrypted MP3.
type Episode = internaldeezer.Episode

// LyricLine is one time-synced lyric line.
type LyricLine = internaldeezer.LyricLine

// Lyrics holds a track's plain text plus optional time-synced lines.
type Lyrics = internaldeezer.Lyrics

// Playlist is a search/browse result.
type Playlist = internaldeezer.Playlist

// Podcast is a show returned by search.
type Podcast = internaldeezer.Podcast

// SearchResults groups tracks, albums, artists and playlists from a query.
type SearchResults = internaldeezer.SearchResults

// StreamPlan is the resolved CDN URL + metadata needed to download and decrypt
// a track or episode. Obtain one via [Client.PrepareStream] or
// [Client.PodcastEpisodeStream], then pass it to [DownloadTrack] or
// [sdk/player.Player.Play].
type StreamPlan = internaldeezer.StreamPlan

// Track is a Deezer track (metadata only).
type Track = internaldeezer.Track

// StripeDecryptor streams Deezer's BF_CBC_STRIPE plaintext from arbitrary
// input chunks. Use [NewStripeDecryptor] to obtain one.
type StripeDecryptor = internaldeezer.StripeDecryptor

// ---- errors ----

// ErrARLExpired is returned by Login when the ARL cookie is missing, expired
// or otherwise rejected. Use errors.Is to detect it and prompt the user to
// re-authenticate.
var ErrARLExpired = internaldeezer.ErrARLExpired

// ---- quality constants ----

const (
	// QualityNormal selects MP3 at 128 kbps.
	QualityNormal = internaldeezer.QualityNormal
	// QualityHigh selects MP3 at 320 kbps (requires a Premium account).
	QualityHigh = internaldeezer.QualityHigh
	// QualityLossless selects FLAC (requires a HiFi account); falls back to
	// MP3 320 → 128 when the account or track is not entitled.
	QualityLossless = internaldeezer.QualityLossless
)

// ---- Client ----

// Client holds an authenticated Deezer session. Create one with [New] and call
// [Client.Login] before issuing any browse or stream request.
//
// Client is safe for concurrent use.
type Client struct {
	c *internaldeezer.Client
}

// New creates a Client for the given ARL (Audio Reference Link). The ARL is
// the long-lived Deezer authentication cookie; obtain it from your browser's
// cookie store at deezer.com. Login is not called yet.
func New(arl string) *Client {
	return &Client{c: internaldeezer.New(arl)}
}

// Login authenticates the session and fetches the api_token, license_token,
// and user identity. It must be called before any browse or stream method.
// Returns [ErrARLExpired] when the ARL is rejected by Deezer.
func (cl *Client) Login() error { return cl.c.Login() }

// LoggedIn reports whether Login has completed successfully.
func (cl *Client) LoggedIn() bool { return cl.c.LoggedIn() }

// UserID returns the numeric Deezer user id (available after Login).
func (cl *Client) UserID() string { return cl.c.UserID() }

// Account returns the logged-in user's plan and streaming entitlements.
func (cl *Client) Account() Account { return cl.c.Account() }

// SetQuality selects the preferred stream quality: [QualityNormal],
// [QualityHigh], or [QualityLossless]. Deezer falls back to the highest
// quality the account and track are entitled to.
func (cl *Client) SetQuality(q int) { cl.c.SetQuality(q) }

// Quality returns the current quality preference (0–2).
func (cl *Client) Quality() int { return cl.c.Quality() }

// ---- library reads ----

// Favorites lists the user's liked songs (requires login).
func (cl *Client) Favorites() ([]Track, error) { return cl.c.Favorites() }

// Playlists lists the user's own playlists (requires login).
func (cl *Client) Playlists() ([]Playlist, error) { return cl.c.Playlists() }

// PlaylistTracks lists the tracks in a playlist by id. Works for private
// playlists the logged-in user owns (requires login).
func (cl *Client) PlaylistTracks(id string) ([]Track, error) {
	return cl.c.PlaylistTracks(id)
}

// AlbumTracks lists an album's tracks by id (public, no login required).
func (cl *Client) AlbumTracks(id string) ([]Track, error) {
	return cl.c.AlbumTracks(id)
}

// Track fetches a single track's metadata by id (requires login).
func (cl *Client) Track(id string) (Track, error) { return cl.c.Track(id) }

// Search queries tracks, albums, artists and playlists. At least some results
// are returned for each category. No login required for public search.
func (cl *Client) Search(query string) (*SearchResults, error) {
	return cl.c.Search(query)
}

// Charts fetches the global or genre top lists. Pass genreID "0" for the
// global chart (public, no login required).
func (cl *Client) Charts(genreID string) (*Chart, error) {
	return cl.c.Charts(genreID)
}

// Flow returns Deezer's personalised radio (requires login and a Premium
// account).
func (cl *Client) Flow() ([]Track, error) { return cl.c.Flow() }

// ArtistTop lists an artist's most popular tracks (public).
func (cl *Client) ArtistTop(id string) ([]Track, error) {
	return cl.c.ArtistTop(id)
}

// ArtistProfile fetches an artist's full profile: biography, top tracks,
// albums, and related artists (public).
func (cl *Client) ArtistProfile(id string) (*ArtistPage, error) {
	return cl.c.ArtistProfile(id)
}

// Lyrics fetches a track's plain-text lyrics and, when Deezer provides them,
// time-synced lines (requires login).
func (cl *Client) Lyrics(trackID string) (*Lyrics, error) {
	return cl.c.Lyrics(trackID)
}

// ---- podcasts ----

// SearchPodcasts finds shows by keyword (public).
func (cl *Client) SearchPodcasts(query string) ([]Podcast, error) {
	return cl.c.SearchPodcasts(query)
}

// PodcastEpisodes lists a show's episodes by podcast id (public).
func (cl *Client) PodcastEpisodes(podcastID string) ([]Episode, error) {
	return cl.c.PodcastEpisodes(podcastID)
}

// EpisodeMeta fetches a single episode's metadata by id (public).
func (cl *Client) EpisodeMeta(id string) (Episode, error) {
	return cl.c.EpisodeMeta(id)
}

// ---- library write ops ----

// AddFavoriteTrack likes a track (adds it to Liked Songs). Requires login and
// a Premium account.
func (cl *Client) AddFavoriteTrack(trackID string) error {
	return cl.c.AddFavoriteTrack(trackID)
}

// RemoveFavoriteTrack unlikes a track. Requires login.
func (cl *Client) RemoveFavoriteTrack(trackID string) error {
	return cl.c.RemoveFavoriteTrack(trackID)
}

// AddToPlaylist appends a track to a playlist the logged-in user can edit.
func (cl *Client) AddToPlaylist(playlistID, trackID string) error {
	return cl.c.AddToPlaylist(playlistID, trackID)
}

// RemoveFromPlaylist removes a track from a playlist the user owns.
func (cl *Client) RemoveFromPlaylist(playlistID, trackID string) error {
	return cl.c.RemoveFromPlaylist(playlistID, trackID)
}

// CreatePlaylist creates a new playlist (optionally seeded with tracks) and
// returns its id.
func (cl *Client) CreatePlaylist(title string, trackIDs []string) (string, error) {
	return cl.c.CreatePlaylist(title, trackIDs)
}

// RenamePlaylist changes a playlist's title.
func (cl *Client) RenamePlaylist(playlistID, title string) error {
	return cl.c.RenamePlaylist(playlistID, title)
}

// DeletePlaylist deletes a playlist the user owns.
func (cl *Client) DeletePlaylist(playlistID string) error {
	return cl.c.DeletePlaylist(playlistID)
}

// ---- stream resolution ----

// PrepareStream resolves a track id to a [StreamPlan] containing the CDN URL,
// format, ReplayGain, and an Encrypted flag. Pass the plan to [DownloadTrack]
// or [sdk/player.Player.Play].
//
// Requires login and a Premium account. Quality falls back automatically when
// the account is not entitled to the preferred level.
func (cl *Client) PrepareStream(trackID string) (*StreamPlan, error) {
	return cl.c.PrepareStream(trackID)
}

// PodcastEpisodeStream resolves a podcast episode to an unencrypted [StreamPlan]
// (StreamPlan.Encrypted == false). Episodes are plain MP3; no decryption needed.
func (cl *Client) PodcastEpisodeStream(episodeID string) (*StreamPlan, error) {
	return cl.c.PodcastEpisodeStream(episodeID)
}

// ---- helpers ----

// FormatLabel converts a raw Deezer format string (e.g. "MP3_320") to a human
// label (e.g. "MP3 · 320 kbps").
func FormatLabel(raw string) string { return internaldeezer.FormatLabel(raw) }

// TrackIDOf extracts the numeric Deezer track id from a URI
// ("deezer:track:123"), a URL, or a bare numeric string.
func TrackIDOf(uri string) string { return internaldeezer.TrackIDOf(uri) }

// BlowfishKey derives the per-track Blowfish key used for BF_CBC_STRIPE
// decryption. The key is a 16-byte XOR of the MD5 hex of trackID and a fixed
// secret. Most callers should use [DownloadTrack] or [DecryptBytes] instead.
func BlowfishKey(trackID string) []byte { return internaldeezer.BlowfishKey(trackID) }

// NewStripeDecryptor builds a streaming BF_CBC_STRIPE decryptor keyed for
// trackID. Feed it arbitrary-sized chunks via StripeDecryptor.Feed; flush the
// final partial chunk with StripeDecryptor.Finish.
func NewStripeDecryptor(trackID string) (*StripeDecryptor, error) {
	return internaldeezer.NewStripeDecryptor(trackID)
}

// DecryptBytes decrypts a complete in-memory BF_CBC_STRIPE buffer for the
// given track id. Use [DownloadTrack] for streaming decryption to a writer.
func DecryptBytes(trackID string, data []byte) ([]byte, error) {
	return internaldeezer.DecryptTrack(trackID, data)
}

// Unwrap returns the underlying internal Deezer client. This is used by the
// sdk/control and sdk/player packages, which live in the same module and can
// import internal/deezer directly. External callers should not depend on this.
func Unwrap(cl *Client) *internaldeezer.Client {
	if cl == nil {
		return nil
	}
	return cl.c
}
