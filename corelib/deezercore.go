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
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/bridge"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/config"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/deezer"
	odlog "github.com/Cycl0o0/OpenDeezer/v2/internal/log"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/mediacache"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/update"
)

func main() {} // required for buildmode=c-archive

var errNotReady = errors.New("not logged in")

var (
	mu       sync.Mutex
	client   *deezer.Client
	player   *audio.Player
	finished int // bumped whenever a track ends naturally
)

// lastLoginKind records why the most recent DZInit login attempt failed so the
// native UIs can tell "no internet" apart from "expired ARL" (DZInit itself only
// returns a bare 0/1). Read via DZLoginErrorKind. Atomic: DZInit's Login runs off
// the mu lock. Values match loginKind below.
var lastLoginKind int32

// loginKind maps a Login/DZInit error to the stable code the GUIs branch on:
// 0 = ok, 1 = ARL expired/invalid (send to re-auth), 2 = no internet (show the
// No-Internet screen + Retry), 3 = other (generic failure).
func loginKind(err error) int32 {
	switch {
	case err == nil:
		return 0
	case deezer.IsNoNetwork(err):
		return 2
	case deezer.IsARLExpired(err):
		return 1
	default:
		return 3
	}
}

// Stable JSON DTOs and Deezer-to-wire conversions live in internal/bridge.
// These aliases keep the sibling c-archive source compatible without
// reintroducing a binding-local wire type or conversion implementation.
type jTrack = bridge.Track

func toJTrack(track deezer.Track) bridge.Track { return bridge.FromTrack(track) }

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
	// One-time, non-network setup under the lock.
	mu.Lock()
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
			mu.Unlock()
			return 0
		}
		player = p
		// Opt-in on-disk raw-stream cache (media.json: mediaCacheMB > 0), attached
		// once here — before any playback, as SetStreamCache requires. Best-effort:
		// a cache failure only logs; playback simply runs uncached.
		if mb := config.LoadMedia().MediaCacheMB; mb > 0 {
			if dir, err := config.Dir(); err == nil {
				if mc, err := mediacache.New(filepath.Join(dir, "mediacache"), int64(mb)<<20); err == nil {
					player.SetStreamCache(mc)
					odlog.Info("media cache on (%d MB)", mb)
				} else {
					odlog.Warn("media cache: %v", err)
				}
			}
		}
		player.SetOnFinish(func() {
			// The finished track was listened to its natural end — record it in the
			// local history now (covers both a real finish and a gapless promote; a
			// following Play sees the player Stopped/already-swapped and won't
			// re-record it). Must run before engineAdvanceOnFinish, which moves
			// currentTrack forward on a promote.
			noteTrackFinished()
			// Engine-side auto-advance: when a queued track ends naturally, move to
			// the next track in the engine queue and start it. The network resolve +
			// Play is offloaded to its own goroutine inside engineAdvanceOnFinish, so
			// this callback — invoked from the player's manage() goroutine — never
			// blocks on I/O.
			//
			// engineAdvanceOnFinish reports whether the engine queue actually owned
			// this finish. Only bump the GUI-facing finished counter (which native
			// c-archive GUIs poll via DZFinishedCount to run their OWN queue) when it
			// did NOT, so exactly one queue mechanism advances per natural finish —
			// otherwise a remote-controlled GUI that also has its own queue loaded
			// would double-advance.
			if !engineAdvanceOnFinish() {
				mu.Lock()
				finished++
				mu.Unlock()
			}
			notifyControlState() // track ended/advanced: push a fresh SSE snapshot
		})
	}
	mu.Unlock()

	// Login is a network round-trip (up to 30s): do it WITHOUT holding mu, so it
	// can't block every other engine call meanwhile.
	c := deezer.New(C.GoString(arl))
	if err := c.Login(); err != nil {
		atomic.StoreInt32(&lastLoginKind, loginKind(err))
		odlog.Warn("login failed: %v", err)
		return 0
	}
	atomic.StoreInt32(&lastLoginKind, 0)
	c.SetAdsDisabled(config.LoadAdsDisabled()) // free-tier ads opt-out (persisted)
	mu.Lock()
	client = c
	mu.Unlock()

	odlog.Info("logged in: %s (%s)", c.Account().Name, c.Account().Offer)
	// Start engine-hosted services (Discord RP + control API) once.
	startServices(c)
	// On a re-login with a different ARL, rebuild the control server around the
	// new client so an account switch stops serving the previous account's
	// library (startServices runs under a sync.Once and won't re-run). No-op on
	// the first login and on a same-account re-login.
	refreshControlServer(c)
	return 1
}

// DZLoginErrorKind returns why the most recent DZInit failed, so the UI can show
// a No-Internet retry screen instead of pushing the user to re-authenticate:
// 0 = ok / logged in, 1 = ARL expired or invalid, 2 = no internet, 3 = other.
//
//export DZLoginErrorKind
func DZLoginErrorKind() C.int {
	return C.int(atomic.LoadInt32(&lastLoginKind))
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
	if routedRemote() != nil {
		return C.CString(deezer.FormatLabel(remoteSnapshot().Format))
	}
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
	return jsonStr(map[string]any{"tracks": bridge.FromTracks(ts)}, err)
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
	return jsonStr(map[string]any{"playlists": bridge.FromPlaylists(ps)}, err)
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
	return jsonStr(map[string]any{"tracks": bridge.FromTracks(ts)}, err)
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
	return jsonStr(map[string]any{"tracks": bridge.FromTracks(ts)}, err)
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
	return jsonStr(map[string]any{
		"tracks": bridge.FromTracks(r.Tracks), "albums": bridge.FromAlbums(r.Albums),
		"artists": bridge.FromArtistInfos(r.Artists), "playlists": bridge.FromPlaylists(r.Playlists),
	}, nil)
}

//export DZPlay
func DZPlay(trackID *C.char, durationMS C.longlong) C.int {
	id := C.GoString(trackID)
	// OpenDeezer Connect: when a device is selected, play there instead.
	if rc := routedRemote(); rc != nil {
		st, err := rc.PlayTrack(id)
		if err != nil {
			return 0
		}
		setRemoteState(st)
		return 1
	}
	mu.Lock()
	c := client
	p := player
	mu.Unlock()
	if c == nil || p == nil {
		return 0
	}
	plan, err := c.PrepareStream(id)
	if err != nil {
		return 0
	}
	noteTrackTransition(p) // record the outgoing listen before the swap
	if err := p.Play(plan, int64(durationMS)); err != nil {
		return 0
	}
	clearPreloadedTrack() // p.Play discarded any pending preload; drop its stash too
	// Track the now-playing for Discord RP + remote status; fill in full
	// metadata (title/artist/album) asynchronously.
	setCurrentTrack(deezer.Track{ID: id, DurationMS: int64(durationMS)})
	go fetchTrackMeta(c, id)
	// Report the play to Deezer (log.listen), like the web player — free-tier ad
	// accounting + artist play-counts depend on it. Best-effort, off the hot path.
	go func() { _ = c.NowPlaying(id) }()
	return 1
}

//export DZPause
func DZPause() {
	if rc := routedRemote(); rc != nil {
		if remoteSnapshot().State == "playing" {
			if st, err := rc.PlayPause(); err == nil {
				setRemoteState(st)
			}
		}
		return
	}
	withPlayerNotify(func(p *audio.Player) { p.Pause() })
}

//export DZResume
func DZResume() {
	if rc := routedRemote(); rc != nil {
		if remoteSnapshot().State == "paused" {
			if st, err := rc.PlayPause(); err == nil {
				setRemoteState(st)
			}
		}
		return
	}
	withPlayerNotify(func(p *audio.Player) { p.Resume() })
}

//export DZTogglePause
func DZTogglePause() {
	if rc := routedRemote(); rc != nil {
		if st, err := rc.PlayPause(); err == nil {
			setRemoteState(st)
		}
		return
	}
	withPlayerNotify(func(p *audio.Player) { p.TogglePause() })
}

//export DZStop
func DZStop() {
	if rc := routedRemote(); rc != nil {
		if st, err := rc.Stop(); err == nil {
			setRemoteState(st)
		}
		return
	}
	// Record the in-progress listen before halting: the next Play sees the
	// player Stopped and skips recording, so this is the only chance.
	withPlayerNotify(func(p *audio.Player) { noteTrackTransition(p); p.Stop() })
}

//export DZSeek
func DZSeek(ms C.longlong) {
	if rc := routedRemote(); rc != nil {
		if st, err := rc.Seek(int64(ms)); err == nil {
			setRemoteState(st)
		}
		return
	}
	withPlayerNotify(func(p *audio.Player) { p.SeekMS(int64(ms)) })
}

// DZSetRepeat sets the repeat mode (mode: 0=off, 1=all, 2=one). The mode is
// always recorded on the engine-side queue — /status reports it and the engine
// queue honors it when it advances — and it is forwarded to the connected
// remote device when one is selected.
//
//export DZSetRepeat
func DZSetRepeat(mode C.int) {
	m := "off"
	switch int(mode) {
	case 1:
		m = "all"
	case 2:
		m = "one"
	}
	engineSetRepeat(m)
	if rc := routedRemote(); rc != nil {
		if st, err := rc.SetRepeat(m); err == nil {
			setRemoteState(st)
		}
	}
}

// DZSetShuffle sets shuffle on (1) or off (0). The flag is always recorded on
// the engine-side queue — /status reports it and the engine queue honors it
// when it advances — and it is forwarded to the connected remote device when
// one is selected.
//
//export DZSetShuffle
func DZSetShuffle(on C.int) {
	engineSetShuffle(on != 0)
	if rc := routedRemote(); rc != nil {
		if st, err := rc.SetShuffle(on != 0); err == nil {
			setRemoteState(st)
		}
	}
}

//export DZState
func DZState() C.int {
	if routedRemote() != nil {
		return C.int(remoteStateInt(remoteSnapshot().State))
	}
	v := 0
	withPlayer(func(p *audio.Player) { v = int(p.State()) })
	return C.int(v)
}

//export DZPositionMS
func DZPositionMS() C.longlong {
	if routedRemote() != nil {
		return C.longlong(remoteSnapshot().PositionMS)
	}
	var v int64
	withPlayer(func(p *audio.Player) { v = p.PositionMS() })
	return C.longlong(v)
}

//export DZDurationMS
func DZDurationMS() C.longlong {
	if routedRemote() != nil {
		return C.longlong(remoteSnapshot().DurationMS)
	}
	var v int64
	withPlayer(func(p *audio.Player) { v = p.DurationMS() })
	return C.longlong(v)
}

//export DZSetVolume
func DZSetVolume(v C.double) {
	if rc := routedRemote(); rc != nil {
		if st, err := rc.SetVolume(float64(v)); err == nil {
			setRemoteState(st)
		}
		return
	}
	withPlayerNotify(func(p *audio.Player) {
		cur := p.Volume()
		p.AddVolume(float64(v) - cur)
	})
}

//export DZVolume
func DZVolume() C.double {
	if routedRemote() != nil {
		return C.double(remoteSnapshot().Volume)
	}
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

// ---- downloads (premium-only, full track to disk) ----

// DZDownloadTrack downloads trackID to a file in destDir and returns JSON
// {"path":"..."} on success or {"error":"..."} on failure. Pass "" for destDir
// to use the shared default folder (DZDownloadDir). The filename is derived from
// the track title/artist and the resolved format's extension. Blocking — GUIs
// should call it off the UI thread (as they already do for DZPlay). The result
// string must be released with DZFree. Downloads are premium-only; a free
// account gets {"error":"downloads require a paid Deezer plan"}.
//
//export DZDownloadTrack
func DZDownloadTrack(trackID, destDir *C.char) *C.char {
	c := curClient()
	if c == nil {
		return jsonStr(nil, errNotReady)
	}
	dir := C.GoString(destDir)
	if dir == "" {
		dir = config.LoadDownloadDir()
	}
	path, err := c.SaveTrack(context.Background(), C.GoString(trackID), dir)
	if err != nil {
		return jsonStr(nil, err)
	}
	return jsonStr(map[string]string{"path": path}, nil)
}

// batchSummary is the shared JSON summary for the batch downloaders:
// {"saved":N,"failed":N,"dir":"...","error":""}. error is "" on full success
// and carries the batch error message (ErrPartialDownload's counts, a premium
// gate, a cancellation, ...) otherwise.
func batchSummary(saved []string, failed int, dir string, err error) map[string]any {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return map[string]any{"saved": len(saved), "failed": failed, "dir": dir, "error": msg}
}

// DZDownloadAlbum downloads every track of albumID to the shared download folder
// (DZDownloadDir) and returns the batch summary
// {"saved":N,"failed":N,"dir":"...","error":""}. Blocking and premium-only, the
// same gate + folder DZDownloadTrack uses (GUIs already call the single-track
// download off the UI thread). On a partial download the per-track failures are
// counted in "failed" and "error" carries the batch message; a free account
// yields saved:0,failed:0 with the premium error. Release with DZFree.
//
//export DZDownloadAlbum
func DZDownloadAlbum(id *C.char) *C.char {
	c := curClient()
	if c == nil {
		return jsonStr(nil, errNotReady)
	}
	dir := config.LoadDownloadDir()
	var failed int
	opts := deezer.DownloadOptions{Progress: func(p deezer.DownloadProgress) {
		if p.Err != nil {
			failed++
		}
	}}
	saved, err := c.SaveAlbum(context.Background(), C.GoString(id), dir, opts)
	return jsonStr(batchSummary(saved, failed, dir, err), nil)
}

// DZDownloadPlaylist downloads every track of playlistID to the shared download
// folder and returns the same batch summary as DZDownloadAlbum.
//
//export DZDownloadPlaylist
func DZDownloadPlaylist(id *C.char) *C.char {
	c := curClient()
	if c == nil {
		return jsonStr(nil, errNotReady)
	}
	dir := config.LoadDownloadDir()
	var failed int
	opts := deezer.DownloadOptions{Progress: func(p deezer.DownloadProgress) {
		if p.Err != nil {
			failed++
		}
	}}
	saved, err := c.SavePlaylist(context.Background(), C.GoString(id), dir, opts)
	return jsonStr(batchSummary(saved, failed, dir, err), nil)
}

// DZDownloadDir returns the current download folder (env/config/default). The
// result must be released with DZFree.
//
//export DZDownloadDir
func DZDownloadDir() *C.char { return C.CString(config.LoadDownloadDir()) }

// DZSetDownloadDir persists the download folder ("" resets to the default) and
// returns 1 on success, 0 on failure.
//
//export DZSetDownloadDir
func DZSetDownloadDir(path *C.char) C.int { return ok(config.SaveDownloadDir(C.GoString(path))) }

// DZMediaCacheMB returns the on-disk raw-stream cache budget in megabytes
// (media.json; 0 = cache disabled, the default).
//
//export DZMediaCacheMB
func DZMediaCacheMB() C.int { return C.int(config.LoadMedia().MediaCacheMB) }

// DZSetMediaCacheMB persists the raw-stream cache budget in megabytes (0 or a
// negative value disables the cache). The cache is attached to the player once
// at startup, before any playback, so a change takes effect at the next
// launch. Returns 1 on success, 0 on failure.
//
//export DZSetMediaCacheMB
func DZSetMediaCacheMB(mb C.int) C.int {
	m := config.LoadMedia()
	m.MediaCacheMB = int(mb)
	return ok(config.SaveMedia(m))
}

// DZIsPreview returns 1 when the current track is Deezer's 30-second preview
// (the free-account fallback) rather than the full stream, else 0.
//
//export DZIsPreview
func DZIsPreview() C.int {
	mu.Lock()
	p := player
	mu.Unlock()
	if p != nil && p.IsPreview() {
		return 1
	}
	return 0
}

// DZSetAdsDisabled turns Deezer Free's play-reporting/ads off (1) or on (0) and
// persists the choice. FREE accounts only: reporting plays (log.listen) credits
// artists and drives the ad schedule; disabling it is ad-free but stops
// reporting — the user's at-own-risk choice. Paid accounts are unaffected.
//
//export DZSetAdsDisabled
func DZSetAdsDisabled(disabled C.int) C.int {
	off := disabled != 0
	if c := curClient(); c != nil {
		c.SetAdsDisabled(off)
	}
	return ok(config.SaveAdsDisabled(off))
}

// DZAdsDisabled returns 1 when the free-tier ads/play-reporting opt-out is on.
//
//export DZAdsDisabled
func DZAdsDisabled() C.int {
	if c := curClient(); c != nil {
		if c.AdsDisabled() {
			return 1
		}
		return 0
	}
	if config.LoadAdsDisabled() {
		return 1
	}
	return 0
}

// ---- account / browse / lyrics / loudness (added for the v0.3 roadmap) ----

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
		"tracks":    bridge.FromTracks(ch.Tracks),
		"albums":    bridge.FromAlbums(ch.Albums),
		"artists":   bridge.FromArtistInfos(ch.Artists),
		"playlists": bridge.FromPlaylists(ch.Playlists),
	}, nil)
}

// DZHomeJSON aggregates the Home-screen sections in one call so every GUI shows
// the same landing page: {topTracks, topAlbums} from the charts and the user's
// own {playlists}. Best-effort — a section that fails to load comes back empty
// rather than failing the whole page. The greeting + quick-pick cards
// (Liked/Flow/Charts/Podcasts) are client-side.
//
//export DZHomeJSON
func DZHomeJSON() *C.char {
	mu.Lock()
	c := client
	mu.Unlock()
	if c == nil {
		return jsonStr(nil, errNotReady)
	}
	var tracks []deezer.Track
	var albums []deezer.Album
	if ch, err := c.Charts("0"); err == nil && ch != nil {
		tracks, albums = ch.Tracks, ch.Albums
	}
	ps, _ := c.Playlists()
	return jsonStr(map[string]any{
		"topTracks": bridge.FromTracks(tracks),
		"topAlbums": bridge.FromAlbums(albums),
		"playlists": bridge.FromPlaylists(ps),
	}, nil)
}

// DZCheckUpdateJSON checks GitHub for a newer OpenDeezer release and returns
// {current, latest, hasUpdate, url, notes}. Network failure -> hasUpdate:false.
//
//export DZCheckUpdateJSON
func DZCheckUpdateJSON() *C.char {
	info, _ := update.Check(coreVersion)
	return jsonStr(info, nil)
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
	return jsonStr(map[string]any{"tracks": bridge.FromTracks(ts)}, err)
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
	info := bridge.FromArtistInfo(pg.Artist)
	return jsonStr(map[string]any{
		"artist":  info,
		"top":     bridge.FromTracks(pg.Top),
		"albums":  bridge.FromAlbums(pg.Albums),
		"related": bridge.FromArtistInfos(pg.Related),
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
	return jsonStr(map[string]any{"tracks": bridge.FromTracks(ts)}, err)
}

// DZTrackMixJSON returns a "song radio" mix seeded from a track (the seed track
// is kept as the first entry): {tracks:[...]} in the shared wire shape, exactly
// like DZFlowJSON so a GUI reuses its Flow parsing. Browse-style (like
// DZFlowJSON, it fetches from the local client regardless of any routed remote —
// only the /play/mix/* engine path forwards to a device). Release with DZFree.
//
//export DZTrackMixJSON
func DZTrackMixJSON(id *C.char) *C.char {
	c := curClient()
	if c == nil {
		return jsonStr(nil, errNotReady)
	}
	ts, err := c.TrackMix(C.GoString(id))
	return jsonStr(map[string]any{"tracks": bridge.FromTracks(ts)}, err)
}

// DZArtistMixJSON returns an "artist radio" mix seeded from an artist:
// {tracks:[...]} in the shared wire shape, mirroring DZFlowJSON.
//
//export DZArtistMixJSON
func DZArtistMixJSON(id *C.char) *C.char {
	c := curClient()
	if c == nil {
		return jsonStr(nil, errNotReady)
	}
	ts, err := c.ArtistMix(C.GoString(id))
	return jsonStr(map[string]any{"tracks": bridge.FromTracks(ts)}, err)
}

// DZHistoryRecentJSON returns the newest n entries of the machine-local
// listening history as a JSON array (newest first, same stable shape the
// control API's /history/recent serves); n <= 0 returns all. Empty/unavailable
// history yields "[]". Release with DZFree.
//
//export DZHistoryRecentJSON
func DZHistoryRecentJSON(n C.int) *C.char {
	st := historyStore()
	if st == nil {
		return C.CString("[]")
	}
	entries, err := st.Recent(int(n))
	if err != nil {
		return C.CString("[]")
	}
	return jsonStr(entries, nil)
}

// DZHistoryStatsJSON returns local listening stats over the last sinceDays
// (sinceDays <= 0 = all history) as
// {topTracks:[{trackId,title,artist,plays,totalSec}],
//
//	topArtists:[{artist,plays,totalSec}], totalSeconds:N}. Empty/unavailable
//
// history yields the same shape with empty arrays and totalSeconds:0. Release
// with DZFree.
//
//export DZHistoryStatsJSON
func DZHistoryStatsJSON(sinceDays C.int) *C.char {
	return jsonStr(historyStats(int(sinceDays)), nil)
}

// ---- podcasts (v0.4) ----

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
	return jsonStr(map[string]any{"podcasts": bridge.FromPodcasts(ps)}, nil)
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
	return jsonStr(map[string]any{"episodes": bridge.FromEpisodes(es)}, nil)
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

// DZSetSleepTimer arms the sleep timer: pause after `minutes` (with a fade-out),
// or when the current track ends if endOfTrack != 0 (minutes ignored). Pass
// minutes <= 0 with endOfTrack == 0 to cancel.
//
//export DZSetSleepTimer
func DZSetSleepTimer(minutes C.int, endOfTrack C.int) {
	withPlayerNotify(func(p *audio.Player) {
		p.SetSleepTimer(time.Duration(int(minutes))*time.Minute, endOfTrack != 0)
	})
}

// DZCancelSleepTimer disarms the sleep timer.
//
//export DZCancelSleepTimer
func DZCancelSleepTimer() { withPlayerNotify(func(p *audio.Player) { p.CancelSleepTimer() }) }

// DZSleepTimerActive returns 1 if a sleep timer is armed, else 0.
//
//export DZSleepTimerActive
func DZSleepTimerActive() C.int {
	v := 0
	withPlayer(func(p *audio.Player) {
		if p.SleepActive() {
			v = 1
		}
	})
	return C.int(v)
}

// DZSleepTimerEndOfTrack returns 1 if the armed timer is end-of-track mode.
//
//export DZSleepTimerEndOfTrack
func DZSleepTimerEndOfTrack() C.int {
	v := 0
	withPlayer(func(p *audio.Player) {
		if p.SleepEndOfTrack() {
			v = 1
		}
	})
	return C.int(v)
}

// DZSleepTimerRemainingMS returns milliseconds until the timer fires (0 if none).
//
//export DZSleepTimerRemainingMS
func DZSleepTimerRemainingMS() C.longlong {
	var v int64
	withPlayer(func(p *audio.Player) { v = p.SleepRemainingMS() })
	return C.longlong(v)
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
	id := C.GoString(trackID)
	plan, err := c.PrepareStream(id)
	if err != nil {
		return
	}
	p.Preload(plan, int64(durationMS))
	// Stash the armed track's identity so a gapless promote can move now-playing
	// onto it even when the GUI never syncs its queue into engineQ. DZPreload is
	// fully synchronous — the resolve runs on the caller's thread and nothing is
	// armed after it returns — so no generation counter is needed to pair the
	// stash with the player's preload.
	setPreloadedTrack(deezer.Track{ID: id, DurationMS: int64(durationMS)})
}

// DZPlayEpisode resolves + plays a podcast episode (plain, unencrypted stream).
// Mirrors DZPlay's now-playing pattern: sets the episode as the current track
// immediately (with id + duration), then asynchronously enriches title / podcast
// name / artwork via the REST /episode endpoint.
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
	id := C.GoString(episodeID)
	plan, err := c.PodcastEpisodeStream(id)
	if err != nil {
		return 0
	}
	noteTrackTransition(p) // record the outgoing listen before the swap
	if err := p.Play(plan, int64(durationMS)); err != nil {
		return 0
	}
	clearPreloadedTrack() // p.Play discarded any pending preload; drop its stash too
	// Track the now-playing so DZNowPlayingJSON reflects this episode;
	// enrich with title / podcast name / artwork asynchronously.
	setCurrentTrack(deezer.Track{ID: id, DurationMS: int64(durationMS)})
	go fetchEpisodeMeta(c, id)
	return 1
}

// ---- equalizer + mono downmix (v1.7) ----

// jEQState is the full EQ snapshot shared by every GUI: current settings plus
// the core-owned band/preset tables so the UIs never hardcode them.
type jEQState struct {
	Enabled  bool      `json:"enabled"`
	Mono     bool      `json:"mono"`
	PreampDB float64   `json:"preampDb"`
	GainsDB  []float64 `json:"gainsDb"`
	Preset   string    `json:"preset"`
	Bands    []float64 `json:"bands"`
	Presets  []string  `json:"presets"`
}

// DZEQJSON returns the equalizer state:
// {enabled,mono,preampDb,gainsDb:[10],preset,bands:[10],presets:[...]}.
//
//export DZEQJSON
func DZEQJSON() *C.char {
	var st jEQState
	withPlayer(func(p *audio.Player) {
		st = jEQState{
			Enabled:  p.EQEnabled(),
			Mono:     p.MonoDownmix(),
			PreampDB: p.EQPreampDB(),
			GainsDB:  p.EQGains(),
			Preset:   p.EQPreset(),
			Bands:    p.EQBands(),
			Presets:  audio.EQPresetNames,
		}
	})
	return jsonStr(st, nil)
}

// DZSetEQJSON applies a partial EQ update. Recognized keys (all optional):
// enabled (bool), mono (bool), preampDb (number), gainsDb ([10]number),
// preset (string), band ({"index":N,"gainDb":X}). Returns 1 on success, 0 if
// any present key failed to apply (unknown preset, bad band index/length).
//
//export DZSetEQJSON
func DZSetEQJSON(js *C.char) C.int {
	var req struct {
		Enabled  *bool      `json:"enabled"`
		Mono     *bool      `json:"mono"`
		PreampDB *float64   `json:"preampDb"`
		GainsDB  *[]float64 `json:"gainsDb"`
		Preset   *string    `json:"preset"`
		Band     *struct {
			Index  int     `json:"index"`
			GainDB float64 `json:"gainDb"`
		} `json:"band"`
	}
	if err := json.Unmarshal([]byte(C.GoString(js)), &req); err != nil {
		return 0
	}
	ok := C.int(0)
	withPlayer(func(p *audio.Player) {
		ok = 1
		if req.Enabled != nil {
			p.SetEQEnabled(*req.Enabled)
		}
		if req.Mono != nil {
			p.SetMonoDownmix(*req.Mono)
		}
		if req.PreampDB != nil {
			p.SetEQPreamp(*req.PreampDB)
		}
		if req.Preset != nil {
			if err := p.SetEQPreset(*req.Preset); err != nil {
				ok = 0
			}
		}
		if req.GainsDB != nil {
			if err := p.SetEQGains(*req.GainsDB); err != nil {
				ok = 0
			}
		}
		if req.Band != nil {
			if err := p.SetEQGain(req.Band.Index, req.Band.GainDB); err != nil {
				ok = 0
			}
		}
	})
	return ok
}

// DZClearPreload discards a preloaded next track. Call when the upcoming track
// is no longer determined (shuffle/repeat toggled after a preload was armed) so
// a stale preload can't be gaplessly swapped in.
//
//export DZClearPreload
func DZClearPreload() { clearEnginePreload() }

// ---- engine queue sync (GUI queue -> engine) ----

// DZQueueSet replaces the engine-side playback queue with the GUI's queue so
// remote controllers see it on /status and /next + /prev walk it. js is a JSON
// array of tracks in the same wire shape every list call returns
// ({id,name,durationMs,artistLine,artistId,artists,albumName,artworkUrl,
// explicit}); only id is required, but durationMs should be set so controllers'
// end-of-track detection works. The cursor resets to 0 — follow with
// DZQueueSetIndex to point it at the playing row. Pass "[]" to clear. Once the
// cursor is aligned with the playing track, the ENGINE owns auto-advance for
// natural finishes (DZFinishedCount no longer bumps for queue-owned finishes;
// poll DZQueueIndex / DZNowPlayingJSON instead). GUIs that never call this keep
// today's behavior exactly. Returns 1 on success, 0 on a parse error.
//
//export DZQueueSet
func DZQueueSet(js *C.char) C.int {
	ts, err := queueTracksFromJSON(C.GoString(js))
	if err != nil {
		return 0
	}
	engineQueueSet(ts)
	return 1
}

// DZQueueSetIndex moves the engine queue's cursor to i (clamped; no-op when the
// queue is empty). Call it whenever the GUI changes the playing row so the
// engine queue tracks what is audible — that alignment is what lets the engine
// own auto-advance and gapless cursor sync for a synced queue.
//
//export DZQueueSetIndex
func DZQueueSetIndex(i C.int) { engineQueueSetIndex(int(i)) }

// DZQueueIndex returns the engine queue's cursor (-1 when empty/unsynced), so a
// GUI can resync its own cursor after an engine-driven advance (natural finish,
// remote /next|/prev, gapless promote).
//
//export DZQueueIndex
func DZQueueIndex() C.int { return C.int(engineQueueIndex()) }

// DZQueueVersion returns a counter bumped on every engine-queue CONTENT change
// (DZQueueSet, remote /queue/add|remove|move, /play/album, /play/mix/*) —
// cursor moves don't bump it. A GUI polls this cheaply and pulls DZQueueJSON
// only when a remote controller actually edited the queue.
//
//export DZQueueVersion
func DZQueueVersion() C.longlong { return C.longlong(engineQueueVersion()) }

// DZQueueJSON returns the engine queue as {"version":N,"index":I,"tracks":[..]}
// (tracks in the shared wire shape), so a GUI can adopt remote queue edits.
// Release with DZFree.
//
//export DZQueueJSON
func DZQueueJSON() *C.char {
	ts, idx, ver := engineQueueSnapshot()
	return jsonStr(map[string]any{
		"version": ver,
		"index":   idx,
		"tracks":  bridge.FromTracks(ts),
	}, nil)
}
