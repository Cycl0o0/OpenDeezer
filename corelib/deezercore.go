// Command deezercore exposes the DeezerTUI engine (login, browse, decrypt +
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
*/
import "C"

import (
	"encoding/json"
	"errors"
	"sync"
	"unsafe"

	"github.com/Cycl0o0/DeezerTUI/internal/audio"
	"github.com/Cycl0o0/DeezerTUI/internal/deezer"
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

//export DZInit
func DZInit(arl *C.char) C.int {
	mu.Lock()
	defer mu.Unlock()
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
		return 0
	}
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
		"tracks": toJTracks(r.Tracks), "albums": albums, "playlists": pls,
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
	plan, err := c.PrepareStream(C.GoString(trackID))
	if err != nil {
		return 0
	}
	if err := p.Play(plan, int64(durationMS)); err != nil {
		return 0
	}
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
