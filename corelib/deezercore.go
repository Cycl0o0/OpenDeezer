// Command deezercore exposes the OpenDeezer engine (login, browse, decrypt +
// decode + playback) as a C-callable library so native GUIs (SwiftUI on macOS,
// GTK/libadwaita on GNOME, Qt on KDE) can drive it in-process.
//
// Build:
//
//	CGO_ENABLED=1 go build -buildmode=c-archive -o libdeezercore.a ./corelib
//
// which also emits libdeezercore.h. All list/search calls return a malloc'd
// JSON string the caller must release with DZFree.
package main

/*
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"
	"unsafe"

	"github.com/Cycl0o0/OpenDeezer/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/internal/deezer"
	odlog "github.com/Cycl0o0/OpenDeezer/internal/log"
)

func main() {} // required for buildmode=c-archive

var errNotReady = errors.New("not logged in")

var (
	mu       sync.Mutex
	client   *deezer.Client
	player   *audio.Player
	finished int // bumped whenever a track ends naturally
)

// ---- JSON DTOs (stable wire shape for the native UIs) ----

type jArtist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type jTrack struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	DurationMS int64     `json:"durationMs"`
	Artists    []jArtist `json:"artists"`
	ArtistLine string    `json:"artistLine"`
	AlbumName  string    `json:"albumName"`
	ArtworkURL string    `json:"artworkUrl"`
	Explicit   bool      `json:"explicit"`
}
type jAlbum struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Artists    []jArtist `json:"artists"`
	ArtworkURL string    `json:"artworkUrl"`
}
type jPlaylist struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Owner      string `json:"owner"`
	TrackCount int    `json:"trackCount"`
	ArtworkURL string `json:"artworkUrl"`
}

func toJTrack(t deezer.Track) jTrack {
	as := make([]jArtist, len(t.Artists))
	for i, a := range t.Artists {
		as[i] = jArtist{ID: a.ID, Name: a.Name}
	}
	return jTrack{
		ID: t.ID, Name: t.Name, DurationMS: t.DurationMS, Artists: as,
		ArtistLine: t.ArtistLine(), AlbumName: t.AlbumName, ArtworkURL: t.ArtworkURL,
		Explicit: t.Explicit,
	}
}
func toJTracks(ts []deezer.Track) []jTrack {
	out := make([]jTrack, len(ts))
	for i, t := range ts {
		out[i] = toJTrack(t)
	}
	return out
}

// jsonStr marshals v (or an {"error":...} object) to a malloc'd C string.
func jsonStr(v any, err error) *C.char {
	if err != nil {
		b, _ := json.Marshal(map[string]string{"error": err.Error()})
		return C.CString(string(b))
	}
	b, e := json.Marshal(v)
	if e != nil {
		eb, _ := json.Marshal(map[string]string{"error": e.Error()})
		return C.CString(string(eb))
	}
	return C.CString(string(b))
}

// ---- exported C API ----

//export DZFree
func DZFree(s *C.char) { C.free(unsafe.Pointer(s)) }

// DZFetch downloads raw bytes (e.g. cover art) so GTK/Qt frontends don't need
// their own HTTP stack. Returns a malloc'd buffer (free with DZFreeBytes) and
// writes its length to outLen; returns NULL on error.
//
//export DZFetch
func DZFetch(url *C.char, outLen *C.int) *C.uchar {
	*outLen = 0
	cl := &http.Client{Timeout: 15 * time.Second}
	resp, err := cl.Get(C.GoString(url))
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil || len(b) == 0 {
		return nil
	}
	p := C.malloc(C.size_t(len(b)))
	if p == nil {
		return nil
	}
	C.memcpy(p, unsafe.Pointer(&b[0]), C.size_t(len(b)))
	*outLen = C.int(len(b))
	return (*C.uchar)(p)
}

//export DZFreeBytes
func DZFreeBytes(p *C.uchar) { C.free(unsafe.Pointer(p)) }

//export DZInit
func DZInit(arl *C.char) C.int {
	mu.Lock()
	defer mu.Unlock()
	// This (the c-archive) is embedded in the native GUI processes. The realtime
	// audio callback re-enters Go from CoreAudio's thread; frequent GC there can
	// delay it and cause choppy playback (the standalone TUI doesn't show this).
	// The engine's heap is small, so collect far less often to keep the callback
	// timely. Set once.
	debug.SetGCPercent(400)
	if base, err := os.UserConfigDir(); err == nil {
		_, _ = odlog.OpenFile(filepath.Join(base, "opendeezer"))
	}
	if player == nil {
		p, err := audio.NewPlayer()
		if err != nil {
			return 0
		}
		player = p
		player.SetOnFinish(func() {
			mu.Lock()
			finished++
			mu.Unlock()
		})
	}
	client = deezer.New(C.GoString(arl))
	if err := client.Login(); err != nil {
		odlog.Warn("login failed: %v", err)
		return 0
	}
	odlog.Info("logged in: %s (%s)", client.Account().Name, client.Account().Offer)
	// Start engine-hosted services (Discord RP + control API) once. Pass the
	// just-created client so startServices doesn't re-lock mu (we hold it here).
	startServices(client)
	return 1
}

//export DZLastErrorJSON
func DZLastErrorJSON() *C.char {
	mu.Lock()
	defer mu.Unlock()
	msg := ""
	if player != nil {
		msg = player.LastError()
	}
	return jsonStr(map[string]string{"error": msg}, nil)
}

// DZSetQuality sets the stream quality level: 0=Normal(MP3 128), 1=High(MP3 320),
// 2=HiFi(FLAC, falls back to MP3 if the account/track isn't entitled).
//
//export DZSetQuality
func DZSetQuality(level C.int) {
	mu.Lock()
	c := client
	mu.Unlock()
	if c != nil {
		c.SetQuality(int(level))
	}
}

// DZQuality returns the current quality level (0..2).
//
//export DZQuality
func DZQuality() C.int {
	mu.Lock()
	c := client
	mu.Unlock()
	if c != nil {
		return C.int(c.Quality())
	}
	return 0
}

// DZHighQuality reports whether quality is at least MP3_320 (kept for the
// frontends that use a simple toggle; level 2 also counts as high).
//
//export DZHighQuality
func DZHighQuality() C.int {
	mu.Lock()
	c := client
	mu.Unlock()
	if c != nil && c.HighQuality() {
		return 1
	}
	return 0
}

// DZFormat returns a human label for the current stream's actual format
// (e.g. "FLAC · lossless", "MP3 · 320 kbps"), or "" if nothing is playing.
//
//export DZFormat
func DZFormat() *C.char {
	mu.Lock()
	p := player
	mu.Unlock()
	if p == nil {
		return C.CString("")
	}
	return C.CString(deezer.FormatLabel(p.Format()))
}

//export DZUserID
func DZUserID() *C.char {
	mu.Lock()
	defer mu.Unlock()
	if client == nil {
		return C.CString("")
	}
	return C.CString(client.UserID())
}

//export DZFavoritesJSON
func DZFavoritesJSON() *C.char {
	mu.Lock()
	c := client
	mu.Unlock()
	if c == nil {
		return jsonStr(nil, errNotReady)
	}
	ts, err := c.Favorites()
	return jsonStr(map[string]any{"tracks": toJTracks(ts)}, err)
}

//export DZPlaylistsJSON
func DZPlaylistsJSON() *C.char {
	mu.Lock()
	c := client
	mu.Unlock()
	if c == nil {
		return jsonStr(nil, errNotReady)
	}
	ps, err := c.Playlists()
	out := make([]jPlaylist, len(ps))
	for i, p := range ps {
		out[i] = jPlaylist{ID: p.ID, Name: p.Name, Owner: p.Owner, TrackCount: p.TrackCount, ArtworkURL: p.ArtworkURL}
	}
	return jsonStr(map[string]any{"playlists": out}, err)
}

//export DZPlaylistTracksJSON
func DZPlaylistTracksJSON(id *C.char) *C.char {
	mu.Lock()
	c := client
	mu.Unlock()
	if c == nil {
		return jsonStr(nil, errNotReady)
	}
	ts, err := c.PlaylistTracks(C.GoString(id))
	return jsonStr(map[string]any{"tracks": toJTracks(ts)}, err)
}

//export DZAlbumTracksJSON
func DZAlbumTracksJSON(id *C.char) *C.char {
	mu.Lock()
	c := client
	mu.Unlock()
	if c == nil {
		return jsonStr(nil, errNotReady)
	}
	ts, err := c.AlbumTracks(C.GoString(id))
	return jsonStr(map[string]any{"tracks": toJTracks(ts)}, err)
}

//export DZSearchJSON
func DZSearchJSON(q *C.char) *C.char {
	mu.Lock()
	c := client
	mu.Unlock()
	if c == nil {
		return jsonStr(nil, errNotReady)
	}
	r, err := c.Search(C.GoString(q))
	if err != nil {
		return jsonStr(nil, err)
	}
	albums := make([]jAlbum, len(r.Albums))
	for i, a := range r.Albums {
		as := make([]jArtist, len(a.Artists))
		for j, ar := range a.Artists {
			as[j] = jArtist{ID: ar.ID, Name: ar.Name}
		}
		albums[i] = jAlbum{ID: a.ID, Name: a.Name, Artists: as, ArtworkURL: a.ArtworkURL}
	}
	pls := make([]jPlaylist, len(r.Playlists))
	for i, p := range r.Playlists {
		pls[i] = jPlaylist{ID: p.ID, Name: p.Name, Owner: p.Owner, TrackCount: p.TrackCount, ArtworkURL: p.ArtworkURL}
	}
	return jsonStr(map[string]any{
		"tracks": toJTracks(r.Tracks), "albums": albums,
		"artists": toJArtistInfos(r.Artists), "playlists": pls,
	}, nil)
}

//export DZPlay
func DZPlay(trackID *C.char, durationMS C.longlong) C.int {
	mu.Lock()
	c := client
	p := player
	mu.Unlock()
	if c == nil || p == nil {
		return 0
	}
	id := C.GoString(trackID)
	plan, err := c.PrepareStream(id)
	if err != nil {
		return 0
	}
	if err := p.Play(plan, int64(durationMS)); err != nil {
		return 0
	}
	// Track the now-playing for Discord RP + remote status; fill in full
	// metadata (title/artist/album) asynchronously.
	setCurrentTrack(deezer.Track{ID: id, DurationMS: int64(durationMS)})
	go fetchTrackMeta(c, id)
	return 1
}

//export DZPause
func DZPause() { withPlayer(func(p *audio.Player) { p.Pause() }) }

//export DZResume
func DZResume() { withPlayer(func(p *audio.Player) { p.Resume() }) }

//export DZTogglePause
func DZTogglePause() { withPlayer(func(p *audio.Player) { p.TogglePause() }) }

//export DZStop
func DZStop() { withPlayer(func(p *audio.Player) { p.Stop() }) }

//export DZSeek
func DZSeek(ms C.longlong) { withPlayer(func(p *audio.Player) { p.SeekMS(int64(ms)) }) }

//export DZState
func DZState() C.int {
	v := 0
	withPlayer(func(p *audio.Player) { v = int(p.State()) })
	return C.int(v)
}

//export DZPositionMS
func DZPositionMS() C.longlong {
	var v int64
	withPlayer(func(p *audio.Player) { v = p.PositionMS() })
	return C.longlong(v)
}

//export DZDurationMS
func DZDurationMS() C.longlong {
	var v int64
	withPlayer(func(p *audio.Player) { v = p.DurationMS() })
	return C.longlong(v)
}

//export DZSetVolume
func DZSetVolume(v C.double) {
	withPlayer(func(p *audio.Player) {
		cur := p.Volume()
		p.AddVolume(float64(v) - cur)
	})
}

//export DZVolume
func DZVolume() C.double {
	var v float64 = 1
	withPlayer(func(p *audio.Player) { v = p.Volume() })
	return C.double(v)
}

// DZFinishedCount returns a monotonically increasing count of tracks that ended
// naturally; native UIs poll it to drive auto-advance.
//
//export DZFinishedCount
func DZFinishedCount() C.int {
	mu.Lock()
	defer mu.Unlock()
	return C.int(finished)
}

func withPlayer(fn func(*audio.Player)) {
	mu.Lock()
	p := player
	mu.Unlock()
	if p != nil {
		fn(p)
	}
}

// ---- account / browse / lyrics / loudness (added for the v0.3 roadmap) ----

type jArtistInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ArtworkURL string `json:"artworkUrl"`
	NbFans     int    `json:"nbFans"`
}

func toJArtistInfos(as []deezer.ArtistInfo) []jArtistInfo {
	out := make([]jArtistInfo, len(as))
	for i, a := range as {
		out[i] = jArtistInfo{ID: a.ID, Name: a.Name, ArtworkURL: a.ArtworkURL, NbFans: a.NbFans}
	}
	return out
}

func toJAlbums(as []deezer.Album) []jAlbum {
	out := make([]jAlbum, len(as))
	for i, a := range as {
		ar := make([]jArtist, len(a.Artists))
		for j, x := range a.Artists {
			ar[j] = jArtist{ID: x.ID, Name: x.Name}
		}
		out[i] = jAlbum{ID: a.ID, Name: a.Name, Artists: ar, ArtworkURL: a.ArtworkURL}
	}
	return out
}

// DZAccountJSON returns the logged-in plan + entitlements as JSON
// {userId,name,offer,canHq,canHifi,loggedIn} so GUIs can show the tier and
// explain why a quality tier is unavailable.
//
//export DZAccountJSON
func DZAccountJSON() *C.char {
	mu.Lock()
	c := client
	mu.Unlock()
	if c == nil {
		return jsonStr(nil, errNotReady)
	}
	return jsonStr(c.Account(), nil)
}

// DZChartsJSON returns the global top tracks/albums/artists/playlists.
//
//export DZChartsJSON
func DZChartsJSON() *C.char {
	mu.Lock()
	c := client
	mu.Unlock()
	if c == nil {
		return jsonStr(nil, errNotReady)
	}
	ch, err := c.Charts("0")
	if err != nil {
		return jsonStr(nil, err)
	}
	return jsonStr(map[string]any{
		"tracks":    toJTracks(ch.Tracks),
		"albums":    toJAlbums(ch.Albums),
		"artists":   toJArtistInfos(ch.Artists),
		"playlists": toJPlaylists(ch.Playlists),
	}, nil)
}

func toJPlaylists(ps []deezer.Playlist) []jPlaylist {
	out := make([]jPlaylist, len(ps))
	for i, p := range ps {
		out[i] = jPlaylist{ID: p.ID, Name: p.Name, Owner: p.Owner, TrackCount: p.TrackCount, ArtworkURL: p.ArtworkURL}
	}
	return out
}

// DZArtistTopJSON returns an artist's most popular tracks.
//
//export DZArtistTopJSON
func DZArtistTopJSON(id *C.char) *C.char {
	mu.Lock()
	c := client
	mu.Unlock()
	if c == nil {
		return jsonStr(nil, errNotReady)
	}
	ts, err := c.ArtistTop(C.GoString(id))
	return jsonStr(map[string]any{"tracks": toJTracks(ts)}, err)
}

// DZArtistProfileJSON returns an artist profile with top tracks, albums and
// related artists: {artist,top,albums,related}.
//
//export DZArtistProfileJSON
func DZArtistProfileJSON(id *C.char) *C.char {
	mu.Lock()
	c := client
	mu.Unlock()
	if c == nil {
		return jsonStr(nil, errNotReady)
	}
	pg, err := c.ArtistProfile(C.GoString(id))
	if err != nil {
		return jsonStr(nil, err)
	}
	info := jArtistInfo{ID: pg.Artist.ID, Name: pg.Artist.Name, ArtworkURL: pg.Artist.ArtworkURL, NbFans: pg.Artist.NbFans}
	return jsonStr(map[string]any{
		"artist":  info,
		"top":     toJTracks(pg.Top),
		"albums":  toJAlbums(pg.Albums),
		"related": toJArtistInfos(pg.Related),
	}, nil)
}

// DZLyricsJSON returns a track's lyrics: {plain, synced:[{timeMs,text}], isSynced}.
//
//export DZLyricsJSON
func DZLyricsJSON(trackID *C.char) *C.char {
	mu.Lock()
	c := client
	mu.Unlock()
	if c == nil {
		return jsonStr(nil, errNotReady)
	}
	l, err := c.Lyrics(C.GoString(trackID))
	if err != nil {
		return jsonStr(nil, err)
	}
	type jLine struct {
		TimeMS int64  `json:"timeMs"`
		Text   string `json:"text"`
	}
	synced := make([]jLine, len(l.Synced))
	for i, s := range l.Synced {
		synced[i] = jLine{TimeMS: s.TimeMS, Text: s.Text}
	}
	return jsonStr(map[string]any{"plain": l.Plain, "synced": synced, "isSynced": l.IsSynced()}, nil)
}

// DZSetReplayGain enables (1) / disables (0) loudness normalization.
//
//export DZSetReplayGain
func DZSetReplayGain(on C.int) {
	withPlayer(func(p *audio.Player) { p.SetReplayGain(on != 0) })
}

// DZReplayGain reports whether ReplayGain is enabled (1/0).
//
//export DZReplayGain
func DZReplayGain() C.int {
	v := 0
	withPlayer(func(p *audio.Player) {
		if p.ReplayGain() {
			v = 1
		}
	})
	return C.int(v)
}

// ---- library write ops (v0.4) — return 1 on success, 0 on failure ----

func ok(err error) C.int {
	if err != nil {
		return 0
	}
	return 1
}

func curClient() *deezer.Client {
	mu.Lock()
	defer mu.Unlock()
	return client
}

//export DZAddFavorite
func DZAddFavorite(trackID *C.char) C.int {
	c := curClient()
	if c == nil {
		return 0
	}
	return ok(c.AddFavoriteTrack(C.GoString(trackID)))
}

//export DZRemoveFavorite
func DZRemoveFavorite(trackID *C.char) C.int {
	c := curClient()
	if c == nil {
		return 0
	}
	return ok(c.RemoveFavoriteTrack(C.GoString(trackID)))
}

//export DZAddToPlaylist
func DZAddToPlaylist(playlistID, trackID *C.char) C.int {
	c := curClient()
	if c == nil {
		return 0
	}
	return ok(c.AddToPlaylist(C.GoString(playlistID), C.GoString(trackID)))
}

//export DZRemoveFromPlaylist
func DZRemoveFromPlaylist(playlistID, trackID *C.char) C.int {
	c := curClient()
	if c == nil {
		return 0
	}
	return ok(c.RemoveFromPlaylist(C.GoString(playlistID), C.GoString(trackID)))
}

// DZCreatePlaylist creates an empty playlist and returns its id as a JSON
// string {"id":"..."} (or {"error":"..."}).
//
//export DZCreatePlaylist
func DZCreatePlaylist(title *C.char) *C.char {
	c := curClient()
	if c == nil {
		return jsonStr(nil, errNotReady)
	}
	id, err := c.CreatePlaylist(C.GoString(title), nil)
	if err != nil {
		return jsonStr(nil, err)
	}
	return jsonStr(map[string]string{"id": id}, nil)
}

//export DZRenamePlaylist
func DZRenamePlaylist(playlistID, title *C.char) C.int {
	c := curClient()
	if c == nil {
		return 0
	}
	return ok(c.RenamePlaylist(C.GoString(playlistID), C.GoString(title)))
}

//export DZDeletePlaylist
func DZDeletePlaylist(playlistID *C.char) C.int {
	c := curClient()
	if c == nil {
		return 0
	}
	return ok(c.DeletePlaylist(C.GoString(playlistID)))
}

// DZFlowJSON returns the user's Flow personalized stream: {tracks:[...]}.
//
//export DZFlowJSON
func DZFlowJSON() *C.char {
	c := curClient()
	if c == nil {
		return jsonStr(nil, errNotReady)
	}
	ts, err := c.Flow()
	return jsonStr(map[string]any{"tracks": toJTracks(ts)}, err)
}

// ---- podcasts (v0.4) ----

type jPodcast struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	ArtworkURL   string `json:"artworkUrl"`
	EpisodeCount int    `json:"episodeCount"`
}

type jEpisode struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	ArtworkURL  string `json:"artworkUrl"`
	DurationMS  int64  `json:"durationMs"`
	ReleaseDate string `json:"releaseDate"`
}

// DZSearchPodcastsJSON returns {podcasts:[...]} for a query.
//
//export DZSearchPodcastsJSON
func DZSearchPodcastsJSON(q *C.char) *C.char {
	c := curClient()
	if c == nil {
		return jsonStr(nil, errNotReady)
	}
	ps, err := c.SearchPodcasts(C.GoString(q))
	if err != nil {
		return jsonStr(nil, err)
	}
	out := make([]jPodcast, len(ps))
	for i, p := range ps {
		out[i] = jPodcast{ID: p.ID, Name: p.Name, Description: p.Description, ArtworkURL: p.ArtworkURL, EpisodeCount: p.EpisodeCount}
	}
	return jsonStr(map[string]any{"podcasts": out}, nil)
}

// DZPodcastEpisodesJSON returns {episodes:[...]} for a show id.
//
//export DZPodcastEpisodesJSON
func DZPodcastEpisodesJSON(podcastID *C.char) *C.char {
	c := curClient()
	if c == nil {
		return jsonStr(nil, errNotReady)
	}
	es, err := c.PodcastEpisodes(C.GoString(podcastID))
	if err != nil {
		return jsonStr(nil, err)
	}
	out := make([]jEpisode, len(es))
	for i, e := range es {
		out[i] = jEpisode{ID: e.ID, Title: e.Title, Description: e.Description, ArtworkURL: e.ArtworkURL, DurationMS: e.DurationMS, ReleaseDate: e.ReleaseDate}
	}
	return jsonStr(map[string]any{"episodes": out}, nil)
}

// ---- audio: devices, gapless, crossfade, preload (v0.4) ----

// DZAudioDevicesJSON returns the available output devices:
// {devices:[{id,name,isDefault}]}. id "" is the system default.
//
//export DZAudioDevicesJSON
func DZAudioDevicesJSON() *C.char {
	mu.Lock()
	p := player
	mu.Unlock()
	if p == nil {
		return jsonStr(nil, errNotReady)
	}
	ds, err := p.Devices()
	if err != nil {
		return jsonStr(nil, err)
	}
	type jDev struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		IsDefault bool   `json:"isDefault"`
	}
	out := make([]jDev, len(ds))
	for i, d := range ds {
		out[i] = jDev{ID: d.ID, Name: d.Name, IsDefault: d.IsDefault}
	}
	return jsonStr(map[string]any{"devices": out}, nil)
}

// DZSetAudioDevice switches output to the given device id ("" = default).
//
//export DZSetAudioDevice
func DZSetAudioDevice(id *C.char) C.int {
	mu.Lock()
	p := player
	mu.Unlock()
	if p == nil {
		return 0
	}
	return ok(p.SetDevice(C.GoString(id)))
}

// DZCurrentAudioDevice returns the selected device id ("" = default).
//
//export DZCurrentAudioDevice
func DZCurrentAudioDevice() *C.char {
	mu.Lock()
	p := player
	mu.Unlock()
	if p == nil {
		return C.CString("")
	}
	return C.CString(p.CurrentDevice())
}

//export DZSetGapless
func DZSetGapless(on C.int) { withPlayer(func(p *audio.Player) { p.SetGapless(on != 0) }) }

//export DZGapless
func DZGapless() C.int {
	v := 0
	withPlayer(func(p *audio.Player) {
		if p.Gapless() {
			v = 1
		}
	})
	return C.int(v)
}

//export DZSetCrossfadeMS
func DZSetCrossfadeMS(ms C.int) { withPlayer(func(p *audio.Player) { p.SetCrossfadeMS(int(ms)) }) }

//export DZCrossfadeMS
func DZCrossfadeMS() C.int {
	v := 0
	withPlayer(func(p *audio.Player) { v = p.CrossfadeMS() })
	return C.int(v)
}

// DZPreload resolves a track and preloads it for a gapless/crossfaded
// transition after the current track ends.
//
//export DZPreload
func DZPreload(trackID *C.char, durationMS C.longlong) {
	mu.Lock()
	c := client
	p := player
	mu.Unlock()
	if c == nil || p == nil {
		return
	}
	plan, err := c.PrepareStream(C.GoString(trackID))
	if err != nil {
		return
	}
	p.Preload(plan, int64(durationMS))
}

// DZPlayEpisode resolves + plays a podcast episode (plain, unencrypted stream).
//
//export DZPlayEpisode
func DZPlayEpisode(episodeID *C.char, durationMS C.longlong) C.int {
	mu.Lock()
	c := client
	p := player
	mu.Unlock()
	if c == nil || p == nil {
		return 0
	}
	plan, err := c.PodcastEpisodeStream(C.GoString(episodeID))
	if err != nil {
		return 0
	}
	if err := p.Play(plan, int64(durationMS)); err != nil {
		return 0
	}
	return 1
}
