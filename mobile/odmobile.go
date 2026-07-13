// Package odmobile is the OpenDeezer engine exposed for gomobile (gobind), so a
// native Android (or iOS) app can drive the same login/decrypt/decode/playback
// pipeline the desktop GUIs use. Build it with:
//
//	gomobile bind -target=android -androidapi 24 -o gui/android/app/libs/odmobile.aar ./mobile
//
// Every browse/list call returns a JSON string (the wire shape the GUIs already
// use); mutations return bool/string. The caller polls FinishedCount to drive
// auto-advance, mirroring the C-archive frontends.
package odmobile

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/bridge"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/config"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/control"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/discovery"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/history"
	odlog "github.com/Cycl0o0/OpenDeezer/v2/internal/log"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/mediacache"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/queue"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/update"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/version"
)

// Version is the engine/app version (single source: internal/version).
const Version = version.Number

var (
	mu       sync.Mutex
	client   *deezer.Client
	player   *audio.Player
	finished int

	// lastLoginKind records why the most recent Init login failed so the Kotlin/
	// Swift UIs can tell "no internet" apart from "expired ARL" (Init returns a
	// bare bool). Read via LoginErrorKind. See loginKind for the value mapping.
	lastLoginKind int32

	curMu    sync.Mutex
	curTrack deezer.Track
	curGen   uint64 // bumped by setCurrentTrack; lets async meta fetches detect a newer track
	// curKind is the media kind of curTrack for history recording: "" = song
	// (setCurrentTrack) and history.KindEpisode = podcast (setCurrentEpisode).
	// Metadata enrichment via setCurrentTrackAt preserves it. Guarded by curMu.
	curKind string

	// preloadedTrack remembers the identity (id + duration) of the stream last
	// armed on the player via Preload. The player's pending next source is set
	// ONLY by Preload, so on a gapless/crossfade promote this is exactly the
	// just-promoted track — it lets syncQueueOnGaplessPromote move now-playing
	// forward even when the app never synced its queue via SetQueueJSON.
	// Cleared whenever the player's preload is dropped (ClearPreload, queue
	// edits, repeat/shuffle toggles, and a fresh Play — audio.Player.Play
	// discards any pending preload). Guarded by preloadMu.
	preloadMu      sync.Mutex
	preloadedTrack deezer.Track

	// engineQ mirrors the app's playback queue once it calls SetQueueJSON /
	// SetQueueIndex, so remote /status shows the real queue and /next + /prev
	// walk it. Apps that never sync keep an empty queue and today's behavior
	// (app-owned queue driven by FinishedCount). Guarded by queueMu.
	queueMu sync.Mutex
	engineQ = queue.New()
)

// Stable JSON DTOs and Deezer-to-wire conversions live in internal/bridge.

func jstr(v any, err error) string {
	if err != nil {
		b, _ := json.Marshal(map[string]string{"error": err.Error()})
		return string(b)
	}
	b, e := json.Marshal(v)
	if e != nil {
		eb, _ := json.Marshal(map[string]string{"error": e.Error()})
		return string(eb)
	}
	return string(b)
}

func curClient() *deezer.Client { mu.Lock(); defer mu.Unlock(); return client }
func curPlayer() *audio.Player  { mu.Lock(); defer mu.Unlock(); return player }
func setCurrentTrack(t deezer.Track) uint64 {
	curMu.Lock()
	curGen++
	curTrack = t
	curKind = "" // a song; episodes go through setCurrentEpisode
	gen := curGen
	curMu.Unlock()
	// Every track change (play, gapless promote, metadata enrichment, remote
	// sync) is a state change controllers care about.
	notifyControlState()
	return gen
}

// setCurrentEpisode is setCurrentTrack for a podcast episode: same now-playing
// bookkeeping (and gen bump) but it tags the media kind so the history recorder
// stamps Kind="episode". Native history screens then replay the entry via
// PlayEpisodeMS instead of the music-track resolver (PlayTrackMS). The returned
// gen guards a following async metadata enrichment exactly like setCurrentTrack.
func setCurrentEpisode(t deezer.Track) uint64 {
	curMu.Lock()
	curGen++
	curTrack = t
	curKind = history.KindEpisode
	gen := curGen
	curMu.Unlock()
	notifyControlState()
	return gen
}

// setCurrentTrackAt applies an async metadata enrichment only if no newer
// setCurrentTrack landed since gen (a bare ID re-check would race a concurrent
// Play and pin the previous track's metadata for the whole next track). It
// leaves curKind untouched, so enriching an episode keeps its "episode" kind and
// enriching a song keeps "" — the kind set by the initiating play survives.
func setCurrentTrackAt(gen uint64, t deezer.Track) {
	curMu.Lock()
	applied := curGen == gen
	if applied {
		curTrack = t
	}
	curMu.Unlock()
	if applied {
		notifyControlState()
	}
}

// notifyControlState nudges the control server's SSE event loop to publish a
// fresh /events snapshot right away. The loop has its own 1s fallback ticker,
// so this is purely a latency optimization — safe to skip (nil server) and
// cheap to repeat (notifications are coalesced, the send never blocks).
func notifyControlState() {
	mu.Lock()
	srv := ctrlSrv
	mu.Unlock()
	if srv != nil {
		srv.NotifyStateChanged()
	}
}

// withPlayerNotify runs a state-mutating player action, then pokes the SSE
// loop. Read-only accessors keep the plain curPlayer() pattern.
func withPlayerNotify(fn func(*audio.Player)) {
	if p := curPlayer(); p != nil {
		fn(p)
	}
	notifyControlState()
}
func currentTrack() deezer.Track {
	curMu.Lock()
	defer curMu.Unlock()
	return curTrack
}

// currentTrackSnapshot returns the now-playing track together with its media
// kind ("" = song, history.KindEpisode = podcast), read under a single lock so
// the history recorder never pairs a track with a stale kind.
func currentTrackSnapshot() (deezer.Track, string) {
	curMu.Lock()
	defer curMu.Unlock()
	return curTrack, curKind
}

// setPreloadedTrack stashes the identity of the stream just armed on the
// player via Preload (see the preloadedTrack doc).
func setPreloadedTrack(t deezer.Track) {
	preloadMu.Lock()
	preloadedTrack = t
	preloadMu.Unlock()
}

// takePreloadedTrack returns and clears the armed-preload identity. The player
// consumes its preload on every promote, so the stash is single-use.
func takePreloadedTrack() (t deezer.Track, armed bool) {
	preloadMu.Lock()
	defer preloadMu.Unlock()
	t, armed = preloadedTrack, preloadedTrack.ID != ""
	preloadedTrack = deezer.Track{}
	return t, armed
}

// clearPreloadedTrack drops the armed-preload identity. Call wherever the
// player's pending next source is discarded so the stash can't outlive it.
func clearPreloadedTrack() { setPreloadedTrack(deezer.Track{}) }

// clearEnginePreload discards the player's armed preload AND its stashed
// identity together, so the two can never disagree. Use it on every path where
// the upcoming track is no longer determined (queue edits, repeat/shuffle
// toggles).
func clearEnginePreload() {
	if p := curPlayer(); p != nil {
		p.ClearPreload()
	}
	clearPreloadedTrack()
}

// ---- lifecycle ----

// Init logs in with the ARL and starts the engine. Returns true on success.
func Init(arl string) bool {
	mu.Lock()
	debug.SetGCPercent(400)
	if player == nil {
		p, err := audio.NewPlayer()
		if err != nil {
			mu.Unlock()
			odlog.Warn("audio init: %v", err)
			return false
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
			// An errored finish is NOT a natural end-of-track (B4): the audio layer
			// stores the source's decode/stream error on the player (or flips to
			// Errored on device loss) with essentially nothing played before firing
			// onFinish. Don't record a phantom listen, advance the queue, or bump the
			// finished counter — just refresh state so the UI leaves 'playing'.
			if erroredFinish(curPlayer()) {
				notifyControlState()
				return
			}
			// The finished track was listened to its natural end — record it in the
			// local history now (covers both a real finish and a gapless promote; a
			// following Play sees the player Stopped/already-swapped and won't
			// re-record it). Must run before syncQueueOnGaplessPromote, which moves
			// currentTrack forward on a promote.
			noteTrackFinished()
			// Keep the synced engine queue + now-playing aligned when the player
			// gaplessly promoted a preloaded next track. The finished counter still
			// bumps in every case — the app drives its own advance off it (and must
			// NOT re-Play after a promote: State() is still Playing then).
			//
			// B3: mobile's auto-advance is fully synchronous. onFinish does no
			// network resolve/Play of its own — the app drives its next track off
			// FinishedCount, and the engine command paths (engineNext/enginePrev/
			// engineQueueJump) resolve + Play inline on the caller's goroutine. There
			// is no late async resolve that could overwrite a newer user Play, so no
			// playSeq generation guard is needed here (unlike corelib, whose onFinish
			// resolves the next track on a fresh goroutine).
			syncQueueOnGaplessPromote()
			mu.Lock()
			finished++
			mu.Unlock()
			notifyControlState() // track ended/advanced: push a fresh SSE snapshot
		})
	}
	mu.Unlock()

	c := deezer.New(arl)
	if err := c.Login(); err != nil {
		atomic.StoreInt32(&lastLoginKind, loginKind(err))
		odlog.Warn("login failed: %v", err)
		return false
	}
	atomic.StoreInt32(&lastLoginKind, 0)
	c.SetAdsDisabled(config.LoadAdsDisabled()) // free-tier ads opt-out (persisted)
	mu.Lock()
	client = c
	mu.Unlock()
	startServices(c)
	refreshControlServer(c)
	return true
}

// loginKind maps a Login/Init error to the stable code the mobile UIs branch on:
// 0 = ok, 1 = ARL expired/invalid (re-auth), 2 = no internet (No-Internet screen
// + Retry), 3 = other.
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

// LoginErrorKind returns why the most recent Init failed so the UI shows a
// No-Internet retry screen instead of forcing re-auth: 0 = ok, 1 = ARL expired
// or invalid, 2 = no internet, 3 = other.
func LoginErrorKind() int { return int(atomic.LoadInt32(&lastLoginKind)) }

// LoggedIn reports whether Init succeeded.
func LoggedIn() bool { c := curClient(); return c != nil && c.LoggedIn() }

// CheckUpdate checks GitHub for a newer release; returns JSON
// {current, latest, hasUpdate, url, notes}.
func CheckUpdate() string {
	info, _ := update.Check(Version)
	return jstr(info, nil)
}

// ---- account / settings ----

func Account() string {
	c := curClient()
	if c == nil {
		return jstr(nil, fmt.Errorf("not logged in"))
	}
	return jstr(c.Account(), nil)
}
func UserID() string {
	if c := curClient(); c != nil {
		return c.UserID()
	}
	return ""
}
func SetQuality(level int) {
	if c := curClient(); c != nil {
		c.SetQuality(level)
	}
}
func Quality() int {
	if c := curClient(); c != nil {
		return c.Quality()
	}
	return 0
}
func SetReplayGain(on bool) {
	if p := curPlayer(); p != nil {
		p.SetReplayGain(on)
	}
}
func ReplayGain() bool { p := curPlayer(); return p != nil && p.ReplayGain() }
func SetGapless(on bool) {
	if p := curPlayer(); p != nil {
		p.SetGapless(on)
	}
}
func Gapless() bool { p := curPlayer(); return p == nil || p.Gapless() }
func SetCrossfadeMS(ms int) {
	if p := curPlayer(); p != nil {
		p.SetCrossfadeMS(ms)
	}
}

// SetSleepTimer arms the sleep timer: pause after `minutes` (with a fade-out), or
// when the current track ends if endOfTrack != 0 (minutes ignored). minutes <= 0
// with endOfTrack == 0 cancels it.
func SetSleepTimer(minutes int, endOfTrack int) {
	withPlayerNotify(func(p *audio.Player) {
		p.SetSleepTimer(time.Duration(minutes)*time.Minute, endOfTrack != 0)
	})
}

// CancelSleepTimer disarms the sleep timer.
func CancelSleepTimer() {
	withPlayerNotify(func(p *audio.Player) { p.CancelSleepTimer() })
}

// SleepActive reports whether a sleep timer is armed (1) or not (0).
func SleepActive() int {
	if p := curPlayer(); p != nil && p.SleepActive() {
		return 1
	}
	return 0
}

// SleepEndOfTrack reports whether the armed timer is end-of-track mode (1/0).
func SleepEndOfTrack() int {
	if p := curPlayer(); p != nil && p.SleepEndOfTrack() {
		return 1
	}
	return 0
}

// SleepRemainingMS returns milliseconds until the timer fires (0 if none).
func SleepRemainingMS() int64 {
	if p := curPlayer(); p != nil {
		return p.SleepRemainingMS()
	}
	return 0
}

func CrossfadeMS() int {
	if p := curPlayer(); p != nil {
		return p.CrossfadeMS()
	}
	return 0
}

// Preload resolves trackID's stream (exactly like Play does) and arms the
// player's preload, so the transition after the current track ends is gapless /
// crossfaded instead of a full network re-resolve at track end. No-op when both
// gapless and crossfade are off, or when routed to a Connect device. Blocking
// (network round-trip) — call it off the UI thread. The track duration is taken
// from the synced queue (SetQueueJSON) when present, else fetched best-effort.
func Preload(id string) error {
	if routedRemote() != nil {
		return nil // remote playback: nothing to preload locally
	}
	c, p := curClient(), curPlayer()
	if c == nil || p == nil {
		return fmt.Errorf("engine not ready")
	}
	if !p.Gapless() && p.CrossfadeMS() == 0 {
		return nil // the player would drop the preload; skip the network resolve
	}
	plan, err := c.PrepareStream(id)
	if err != nil {
		return err
	}
	durationMS := queuedDurationMS(id)
	if durationMS == 0 {
		if t, err := c.Track(id); err == nil {
			durationMS = t.DurationMS
		}
	}
	p.Preload(plan, durationMS)
	// Stash the armed track's identity so a gapless promote can move now-playing
	// onto it even when the app never syncs its queue via SetQueueJSON. Preload
	// is fully synchronous — the resolve runs on the caller's thread and nothing
	// is armed after it returns — so no generation counter is needed to pair the
	// stash with the player's preload.
	setPreloadedTrack(deezer.Track{ID: id, DurationMS: durationMS})
	return nil
}

// ClearPreload discards a preloaded next track. Call when the upcoming track is
// no longer determined (shuffle/repeat toggled, queue edited after a preload
// was armed) so a stale preload can't be gaplessly swapped in.
func ClearPreload() { clearEnginePreload() }

// queuedDurationMS looks a track's duration up in the synced engine queue
// (0 when absent), sparing Preload a metadata round-trip.
func queuedDurationMS(id string) int64 {
	queueMu.Lock()
	defer queueMu.Unlock()
	for _, t := range engineQ.Tracks() {
		if t.ID == id {
			return t.DurationMS
		}
	}
	return 0
}

// ---- browse ----

func withClient(fn func(c *deezer.Client) (any, error)) string {
	c := curClient()
	if c == nil {
		return jstr(nil, fmt.Errorf("not logged in"))
	}
	return jstr(fn(c))
}

func Favorites() string {
	return withClient(func(c *deezer.Client) (any, error) {
		ts, err := c.Favorites()
		return map[string]any{"tracks": bridge.FromTracks(ts)}, err
	})
}

// FavoriteIDsJSON returns the account's liked (favorite) track ids as a JSON
// array of strings, e.g. ["123","456"]. It reuses the same c.Favorites() the
// library view fetches, so the app can render truthful "liked" hearts across
// lists without a per-track lookup. Returns "[]" when not logged in or on a
// fetch error.
func FavoriteIDsJSON() string {
	c := curClient()
	if c == nil {
		return "[]"
	}
	ts, err := c.Favorites()
	if err != nil {
		return "[]"
	}
	ids := make([]string, 0, len(ts))
	for _, t := range ts {
		if t.ID != "" {
			ids = append(ids, t.ID)
		}
	}
	return jstr(ids, nil)
}
func Playlists() string {
	return withClient(func(c *deezer.Client) (any, error) {
		ps, err := c.Playlists()
		return map[string]any{"playlists": bridge.FromPlaylists(ps)}, err
	})
}
func PlaylistTracks(id string) string {
	return withClient(func(c *deezer.Client) (any, error) {
		ts, err := c.PlaylistTracks(id)
		return map[string]any{"tracks": bridge.FromTracks(ts)}, err
	})
}
func AlbumTracks(id string) string {
	return withClient(func(c *deezer.Client) (any, error) {
		ts, err := c.AlbumTracks(id)
		return map[string]any{"tracks": bridge.FromTracks(ts)}, err
	})
}
func Flow() string {
	return withClient(func(c *deezer.Client) (any, error) {
		ts, err := c.Flow()
		return map[string]any{"tracks": bridge.FromTracks(ts)}, err
	})
}

// TrackMixJSON returns a "song radio" mix seeded from a track (the seed kept as
// the first entry): {tracks:[...]} in the shared wire shape, mirroring Flow.
func TrackMixJSON(id string) string {
	return withClient(func(c *deezer.Client) (any, error) {
		ts, err := c.TrackMix(id)
		return map[string]any{"tracks": bridge.FromTracks(ts)}, err
	})
}

// ArtistMixJSON returns an "artist radio" mix seeded from an artist:
// {tracks:[...]} in the shared wire shape, mirroring Flow.
func ArtistMixJSON(id string) string {
	return withClient(func(c *deezer.Client) (any, error) {
		ts, err := c.ArtistMix(id)
		return map[string]any{"tracks": bridge.FromTracks(ts)}, err
	})
}
func ArtistTop(id string) string {
	return withClient(func(c *deezer.Client) (any, error) {
		ts, err := c.ArtistTop(id)
		return map[string]any{"tracks": bridge.FromTracks(ts)}, err
	})
}
func ArtistProfile(id string) string {
	return withClient(func(c *deezer.Client) (any, error) { return c.ArtistProfile(id) })
}
func Lyrics(id string) string {
	return withClient(func(c *deezer.Client) (any, error) { return c.Lyrics(id) })
}

func Search(q string) string {
	c := curClient()
	if c == nil {
		return jstr(nil, fmt.Errorf("not logged in"))
	}
	r, err := c.Search(q)
	if err != nil {
		return jstr(nil, err)
	}
	return searchJSON(r.Tracks, r.Albums, r.Artists, r.Playlists)
}
func Charts() string {
	c := curClient()
	if c == nil {
		return jstr(nil, fmt.Errorf("not logged in"))
	}
	ch, err := c.Charts("0")
	if err != nil {
		return jstr(nil, err)
	}
	return searchJSON(ch.Tracks, ch.Albums, ch.Artists, ch.Playlists)
}

// Home aggregates the Home-screen sections (charts top tracks/albums + the
// user's playlists) in one call, mirroring corelib DZHomeJSON. Best-effort.
func Home() string {
	return withClient(func(c *deezer.Client) (any, error) {
		var tracks []deezer.Track
		var albums []deezer.Album
		if ch, err := c.Charts("0"); err == nil && ch != nil {
			tracks, albums = ch.Tracks, ch.Albums
		}
		ps, _ := c.Playlists()
		return map[string]any{
			"topTracks": bridge.FromTracks(tracks),
			"topAlbums": bridge.FromAlbums(albums),
			"playlists": bridge.FromPlaylists(ps),
		}, nil
	})
}

func searchJSON(tracks []deezer.Track, albums []deezer.Album, artists []deezer.ArtistInfo, playlists []deezer.Playlist) string {
	return jstr(map[string]any{
		"tracks": bridge.FromTracks(tracks), "albums": bridge.FromAlbums(albums),
		"artists": bridge.FromArtistInfos(artists), "playlists": bridge.FromPlaylists(playlists),
	}, nil)
}

// ---- podcasts ----

func SearchPodcasts(q string) string {
	return withClient(func(c *deezer.Client) (any, error) {
		ps, err := c.SearchPodcasts(q)
		return map[string]any{"podcasts": bridge.FromPodcasts(ps)}, err
	})
}
func PodcastEpisodes(id string) string {
	return withClient(func(c *deezer.Client) (any, error) {
		es, err := c.PodcastEpisodes(id)
		return map[string]any{"episodes": bridge.FromEpisodes(es)}, err
	})
}

// PlayEpisode resolves + plays a podcast episode (plain stream) with an unknown
// duration. Prefer PlayEpisodeMS when the caller knows the duration.
func PlayEpisode(id string) bool { return PlayEpisodeMS(id, 0) }

// PlayEpisodeMS resolves + plays a podcast episode (plain stream). Sets the
// episode as the current track immediately (id + duration, mirroring corelib
// DZPlayEpisode — the player never derives duration from the stream), then
// asynchronously enriches title / podcast name / artwork via REST /episode.
func PlayEpisodeMS(id string, durationMS int64) bool {
	c, p := curClient(), curPlayer()
	if c == nil || p == nil {
		return false
	}
	plan, err := c.PodcastEpisodeStream(id)
	if err != nil {
		odlog.Warn("episode %s: %v", id, err)
		return false
	}
	noteTrackTransition(p) // record the outgoing listen before the swap
	if err := p.Play(plan, durationMS); err != nil {
		return false
	}
	clearPreloadedTrack() // p.Play discarded any pending preload; drop its stash too
	// setCurrentEpisode (not setCurrentTrack) tags the media kind so this listen
	// lands in history as Kind="episode" and native history replays it via
	// PlayEpisodeMS rather than the music-track resolver.
	gen := setCurrentEpisode(deezer.Track{ID: id, DurationMS: durationMS})
	go fetchEpisodeMeta(c, id, gen)
	return true
}

func fetchEpisodeMeta(c *deezer.Client, id string, gen uint64) {
	ep, err := c.EpisodeMeta(id)
	if err != nil || ep.ID == "" {
		return
	}
	setCurrentTrackAt(gen, ep.AsTrack())
}

// ---- library writes ----

func ok(err error) bool { return err == nil }

func AddFavorite(id string) bool {
	c := curClient()
	return c != nil && ok(c.AddFavoriteTrack(id))
}
func RemoveFavorite(id string) bool {
	c := curClient()
	return c != nil && ok(c.RemoveFavoriteTrack(id))
}
func AddToPlaylist(playlistID, trackID string) bool {
	c := curClient()
	return c != nil && ok(c.AddToPlaylist(playlistID, trackID))
}
func RemoveFromPlaylist(playlistID, trackID string) bool {
	c := curClient()
	return c != nil && ok(c.RemoveFromPlaylist(playlistID, trackID))
}
func CreatePlaylist(title string) string {
	return withClient(func(c *deezer.Client) (any, error) {
		id, err := c.CreatePlaylist(title, nil)
		return map[string]string{"id": id}, err
	})
}
func RenamePlaylist(id, title string) bool {
	c := curClient()
	return c != nil && ok(c.RenamePlaylist(id, title))
}
func DeletePlaylist(id string) bool {
	c := curClient()
	return c != nil && ok(c.DeletePlaylist(id))
}

// ---- playback ----

// Play resolves + plays a track. When routed to a Connect device, it plays there.
func Play(trackID string, durationMS int64) bool {
	if rc := routedRemote(); rc != nil {
		st, err := rc.PlayTrack(trackID)
		if err != nil {
			return false
		}
		setRemoteState(st)
		return true
	}
	c, p := curClient(), curPlayer()
	if c == nil || p == nil {
		return false
	}
	plan, err := c.PrepareStream(trackID)
	if err != nil {
		odlog.Warn("resolve %s: %v", trackID, err)
		return false
	}
	noteTrackTransition(p) // record the outgoing listen before the swap
	if err := p.Play(plan, durationMS); err != nil {
		return false
	}
	clearPreloadedTrack() // p.Play discarded any pending preload; drop its stash too
	gen := setCurrentTrack(deezer.Track{ID: trackID, DurationMS: durationMS})
	go fetchTrackMeta(c, trackID, gen)
	// Report the play (log.listen) like the web player — free-tier ad accounting +
	// artist play-counts depend on it. Best-effort, off the hot path.
	go func() { _ = c.NowPlaying(trackID) }()
	return true
}

func fetchTrackMeta(c *deezer.Client, id string, gen uint64) {
	if t, err := c.Track(id); err == nil && t.ID != "" {
		setCurrentTrackAt(gen, t)
	}
}

// ---- downloads (premium-only, full track to disk) ----

// DownloadTrack downloads trackID to a file in destDir and returns JSON
// {"path":"..."} on success or {"error":"..."} on failure. Pass "" for destDir
// to use the shared default folder (DownloadDir). Blocking — call it off the UI
// thread. Downloads are premium-only; a free account gets an error.
func DownloadTrack(trackID, destDir string) string {
	c := curClient()
	if c == nil {
		return jstr(nil, fmt.Errorf("not logged in"))
	}
	if destDir == "" {
		destDir = config.LoadDownloadDir()
	}
	path, err := c.SaveTrack(context.Background(), trackID, destDir)
	if err != nil {
		return jstr(nil, err)
	}
	return jstr(map[string]string{"path": path}, nil)
}

// batchSummary is the shared JSON summary for the batch downloaders:
// {"saved":N,"failed":N,"dir":"...","error":""}. error is "" on full success
// and carries the batch error message otherwise.
func batchSummary(saved []string, failed int, dir string, err error) map[string]any {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return map[string]any{"saved": len(saved), "failed": failed, "dir": dir, "error": msg}
}

// DownloadAlbum downloads every track of albumID to the shared download folder
// (DownloadDir) and returns the batch summary
// {"saved":N,"failed":N,"dir":"...","error":""}. Blocking and premium-only, the
// same gate + folder DownloadTrack uses — call it off the UI thread. On a
// partial download "failed" counts the per-track failures and "error" carries
// the batch message; a free account yields saved:0,failed:0 with the premium
// error.
func DownloadAlbum(id string) string {
	c := curClient()
	if c == nil {
		return jstr(nil, fmt.Errorf("not logged in"))
	}
	dir := config.LoadDownloadDir()
	var failed int
	opts := deezer.DownloadOptions{Progress: func(p deezer.DownloadProgress) {
		if p.Err != nil {
			failed++
		}
	}}
	saved, err := c.SaveAlbum(context.Background(), id, dir, opts)
	return jstr(batchSummary(saved, failed, dir, err), nil)
}

// DownloadPlaylist downloads every track of playlistID to the shared download
// folder and returns the same batch summary as DownloadAlbum.
func DownloadPlaylist(id string) string {
	c := curClient()
	if c == nil {
		return jstr(nil, fmt.Errorf("not logged in"))
	}
	dir := config.LoadDownloadDir()
	var failed int
	opts := deezer.DownloadOptions{Progress: func(p deezer.DownloadProgress) {
		if p.Err != nil {
			failed++
		}
	}}
	saved, err := c.SavePlaylist(context.Background(), id, dir, opts)
	return jstr(batchSummary(saved, failed, dir, err), nil)
}

// DownloadDir returns the current download folder (env/config/default).
func DownloadDir() string { return config.LoadDownloadDir() }

// SetDownloadDir persists the download folder ("" resets to the default).
func SetDownloadDir(path string) bool { return ok(config.SaveDownloadDir(path)) }

// MediaCacheMB returns the on-disk raw-stream cache budget in megabytes
// (media.json; 0 = cache disabled, the default).
func MediaCacheMB() int { return config.LoadMedia().MediaCacheMB }

// SetMediaCacheMB persists the raw-stream cache budget in megabytes (0 or a
// negative value disables the cache). The cache is attached to the player once
// at startup, before any playback, so a change takes effect at the next launch.
func SetMediaCacheMB(mb int) bool {
	m := config.LoadMedia()
	m.MediaCacheMB = mb
	return ok(config.SaveMedia(m))
}

// IsPreview reports whether the current track is Deezer's 30-second preview (the
// free-account fallback) rather than the full stream.
func IsPreview() bool {
	if p := curPlayer(); p != nil {
		return p.IsPreview()
	}
	return false
}

// SetAdsDisabled turns Deezer Free's play-reporting/ads off/on and persists it.
// FREE accounts only: reporting plays (log.listen) credits artists and drives the
// ad schedule; disabling it is ad-free but stops reporting — the user's
// at-own-risk choice. Paid accounts are unaffected.
func SetAdsDisabled(disabled bool) bool {
	if c := curClient(); c != nil {
		c.SetAdsDisabled(disabled)
	}
	return ok(config.SaveAdsDisabled(disabled))
}

// AdsDisabled reports whether the free-tier ads/play-reporting opt-out is on.
func AdsDisabled() bool {
	if c := curClient(); c != nil {
		return c.AdsDisabled()
	}
	return config.LoadAdsDisabled()
}

func Pause() {
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
func Resume() {
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
func TogglePause() {
	if rc := routedRemote(); rc != nil {
		if st, err := rc.PlayPause(); err == nil {
			setRemoteState(st)
		}
		return
	}
	withPlayerNotify(func(p *audio.Player) { p.TogglePause() })
}
func Stop() {
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

// SetOutputSuspended suspends (true) or resumes (false) the local OS audio
// device without touching playback state — for audio-focus/route handling on
// mobile. Local-only: it never routes to a Connect device.
func SetOutputSuspended(on bool) {
	if p := curPlayer(); p != nil {
		_ = p.SetOutputSuspended(on)
	}
}
func Seek(ms int64) {
	if rc := routedRemote(); rc != nil {
		if st, err := rc.Seek(ms); err == nil {
			setRemoteState(st)
		}
		return
	}
	withPlayerNotify(func(p *audio.Player) { p.SeekMS(ms) })
}
func SetVolume(v float64) {
	if rc := routedRemote(); rc != nil {
		if st, err := rc.SetVolume(v); err == nil {
			setRemoteState(st)
		}
		return
	}
	withPlayerNotify(func(p *audio.Player) { p.SetVolume(v) })
}
func Volume() float64 {
	if routedRemote() != nil {
		return remoteSnapshot().Volume
	}
	if p := curPlayer(); p != nil {
		return p.Volume()
	}
	return 1
}
func State() int {
	if routedRemote() != nil {
		return remoteStateInt(remoteSnapshot().State)
	}
	if p := curPlayer(); p != nil {
		return int(p.State())
	}
	return 0
}
func PositionMS() int64 {
	if routedRemote() != nil {
		return remoteSnapshot().PositionMS
	}
	if p := curPlayer(); p != nil {
		return p.PositionMS()
	}
	return 0
}
func DurationMS() int64 {
	if routedRemote() != nil {
		return remoteSnapshot().DurationMS
	}
	if p := curPlayer(); p != nil {
		return p.DurationMS()
	}
	return 0
}
func Format() string {
	if routedRemote() != nil {
		return deezer.FormatLabel(remoteSnapshot().Format)
	}
	if p := curPlayer(); p != nil {
		return deezer.FormatLabel(p.Format())
	}
	return ""
}
func FinishedCount() int {
	mu.Lock()
	defer mu.Unlock()
	return finished
}

// NowPlaying returns the track actually playing (remote when routed, else local).
func NowPlaying() string {
	if routedRemote() != nil {
		if t := remoteSnapshot().Track; t != nil {
			return jstr(bridge.Track{
				ID: t.ID, Name: t.Title, ArtistLine: t.Artist, ArtistID: t.ArtistID,
				AlbumName: t.Album, Explicit: t.Explicit, DurationMS: t.DurationMS,
				ArtworkURL: t.ArtworkURL,
			}, nil)
		}
		return jstr(map[string]any{}, nil)
	}
	if cur := currentTrack(); cur.ID != "" {
		return jstr(bridge.FromTrack(cur), nil)
	}
	return jstr(map[string]any{}, nil)
}

// ---- engine queue sync (app queue -> engine) ----

// SetQueueJSON replaces the engine-side playback queue with the app's queue so
// remote controllers see it on /status and /next + /prev walk it. js is a JSON
// array of tracks in the same wire shape every list call returns
// ({id,name,durationMs,artistLine,artistId,artists,albumName,artworkUrl,
// explicit}); only id is required, but durationMs should be set so controllers'
// end-of-track detection works. The cursor resets to 0 — follow with
// SetQueueIndex to point it at the playing row. Pass "[]" to clear. Apps that
// never call this keep today's behavior exactly (app-owned queue via
// FinishedCount). Returns an error on a parse failure.
func SetQueueJSON(js string) error {
	ts, err := queueTracksFromJSON(js)
	if err != nil {
		return err
	}
	queueMu.Lock()
	engineQ.Set(ts, 0)
	queueMu.Unlock()
	notifyControlState()
	return nil
}

// SetQueueIndex aligns the engine queue's cursor to i (clamped; no-op when the
// queue is empty). Call it whenever the app changes the playing row so the
// engine queue tracks what is audible. It uses AlignIndex, not SetIndex, so
// this pure synchronisation records no navigation history (a synthetic entry
// would make a later remote Prev jump to a never-played track). Genuine remote
// jumps go through engineQueueSelect (SetIndex), which does record history.
func SetQueueIndex(i int) {
	queueMu.Lock()
	engineQ.AlignIndex(i)
	queueMu.Unlock()
	notifyControlState()
}

// QueueIndex returns the engine queue's cursor (-1 when empty/unsynced), so the
// app can resync its own cursor after an engine-driven advance (remote
// /next|/prev, gapless promote).
func QueueIndex() int {
	queueMu.Lock()
	defer queueMu.Unlock()
	return engineQ.Index()
}

// QueueVersion returns a counter bumped on every engine-queue CONTENT change
// (SetQueueJSON, remote /queue/add|remove|move, /play/album, /play/mix/{track,artist}) —
// cursor moves don't bump it. The app polls this cheaply and pulls QueueJSON
// only when a remote controller actually edited the queue.
func QueueVersion() int64 {
	queueMu.Lock()
	defer queueMu.Unlock()
	return int64(engineQ.Version())
}

// QueueJSON returns the engine queue as {"version":N,"index":I,"tracks":[..]}
// (tracks in the shared wire shape), so the app can adopt remote queue edits.
func QueueJSON() string {
	queueMu.Lock()
	ts := append([]deezer.Track(nil), engineQ.Tracks()...)
	idx := engineQ.Index()
	ver := engineQ.Version()
	queueMu.Unlock()
	return jstr(map[string]any{
		"version": ver,
		"index":   idx,
		"tracks":  bridge.FromTracks(ts),
	}, nil)
}

// queueTracksFromJSON decodes an app queue payload: a JSON array of tracks in
// the same wire shape every list/browse call returns (bridge.Track). Only "id"
// is required. Entries without an id are dropped; "" and "null" decode to an
// empty queue (clear).
func queueTracksFromJSON(js string) ([]deezer.Track, error) {
	if js == "" {
		return nil, nil
	}
	var wire []bridge.Track
	if err := json.Unmarshal([]byte(js), &wire); err != nil {
		return nil, err
	}
	ts := make([]deezer.Track, 0, len(wire))
	for _, w := range wire {
		if w.ID == "" {
			continue
		}
		ts = append(ts, trackFromWire(w))
	}
	return ts, nil
}

// trackFromWire converts a wire track (bridge.Track) back to a deezer.Track.
// When the artists array is absent it falls back to artistLine/artistId so the
// queue still shows an artist on /status.
func trackFromWire(w bridge.Track) deezer.Track {
	t := deezer.Track{
		ID: w.ID, Name: w.Name, DurationMS: w.DurationMS,
		AlbumName: w.AlbumName, ArtworkURL: w.ArtworkURL, Explicit: w.Explicit,
	}
	for _, a := range w.Artists {
		t.Artists = append(t.Artists, deezer.Artist{ID: a.ID, Name: a.Name})
	}
	if len(t.Artists) == 0 && (w.ArtistID != "" || w.ArtistLine != "") {
		t.Artists = []deezer.Artist{{ID: w.ArtistID, Name: w.ArtistLine}}
	}
	return t
}

// syncQueueOnGaplessPromote runs from the player's onFinish callback. When the
// player gaplessly promoted the preloaded next track it is still Playing there
// (a real finish passes through Stopped first); the promoted track is always
// the deterministic linear next, so walk the synced queue cursor + now-playing
// along with the audio.
func syncQueueOnGaplessPromote() {
	p := curPlayer()
	if p == nil || p.State() != audio.Playing {
		return
	}
	advanceNowPlayingOnGaplessPromote()
}

// advanceNowPlayingOnGaplessPromote is the queue + now-playing bookkeeping for
// a gapless promote, split out so tests can drive it without a live audio
// device. When the synced queue owned the finished track, the cursor walks
// forward with the audio. When the queue is unsynced/empty or misaligned (the
// shipped apps preload without calling SetQueueJSON), the promoted stream can
// only be the armed preload — the player's next source is set solely by
// Preload, which stashes its identity — so now-playing moves onto the stash.
// Otherwise NowPlaying/status would stay on the finished track, whose NEXT
// finish would re-record it in history while the promoted track never gets
// recorded. The outgoing listen itself was already recorded by
// noteTrackFinished (it runs before this in the onFinish callback).
func advanceNowPlayingOnGaplessPromote() {
	cur := currentTrack()
	queueMu.Lock()
	qcur, ok := engineQ.Current()
	owned := ok && qcur.ID != "" && qcur.ID == cur.ID && engineQ.AdvanceLinear()
	var next deezer.Track
	if owned {
		next, _ = engineQ.Current()
	}
	queueMu.Unlock()
	// Either way the player just consumed its armed preload: take the stashed
	// identity so a later finish can't reuse it.
	promoted, armed := takePreloadedTrack()
	if owned {
		if next.ID != "" {
			setCurrentTrack(next)
		}
		return
	}
	if armed {
		gen := setCurrentTrack(promoted)
		if c := curClient(); c != nil {
			go fetchTrackMeta(c, promoted.ID, gen) // enrich id+duration, like Play
		}
	}
}

// engineSetRepeat / engineSetShuffle persist the repeat/shuffle choice on the
// engine queue so /status reports the real values (previously hard-coded to
// off/false) and the engine queue honors them when it advances.
func engineSetRepeat(mode string) {
	r := queue.RepeatOff
	switch mode {
	case "all":
		r = queue.RepeatAll
	case "one":
		r = queue.RepeatOne
	}
	queueMu.Lock()
	engineQ.SetRepeat(r)
	queueMu.Unlock()
	// The upcoming track changed (repeat-one replays the CURRENT track): a
	// preload armed for the old linear next must not gapless-promote.
	clearEnginePreload()
	notifyControlState()
}

func engineSetShuffle(on bool) {
	queueMu.Lock()
	engineQ.SetShuffle(on)
	queueMu.Unlock()
	// The upcoming track changed (re-shuffled order): a preload armed for the
	// old linear next must not gapless-promote.
	clearEnginePreload()
	notifyControlState()
}

// engineCycleRepeat advances repeat off->all->one->off on the engine queue,
// applying the same side effects as engineSetRepeat. Repeat is NOT forwarded to
// a routed remote (B2): the controller keeps repeat local so the host's single-
// track queue (we send it one track at a time) never loops the current track.
func engineCycleRepeat() {
	queueMu.Lock()
	engineQ.CycleRepeat()
	queueMu.Unlock()
	// Repeat-one now replays the CURRENT track and a cleared cycle changes the
	// upcoming one: a preload armed for the old linear next must not promote.
	clearEnginePreload()
	notifyControlState()
}

// engineToggleShuffle flips the engine queue's shuffle flag (applying
// engineSetShuffle's side effects) and forwards the resulting value to a routed
// remote. Unlike repeat, shuffle still forwards (B2): the remote's next-track
// selection honors it.
func engineToggleShuffle() {
	queueMu.Lock()
	on := engineQ.ToggleShuffle()
	queueMu.Unlock()
	clearEnginePreload()
	notifyControlState()
	if rc := routedRemote(); rc != nil {
		if st, err := rc.SetShuffle(on); err == nil {
			setRemoteState(st)
		}
	}
}

// ---- network helper (cover art) ----

// Fetch downloads raw bytes (e.g. cover art) using a browser User-Agent.
func Fetch(url string) []byte {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 OpenDeezer")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return b
}

// ---- engine-hosted services (control API + discovery) ----

var (
	servicesOnce  sync.Once
	ctrlSrv       *control.Server
	ctrlCfg       control.Config       // config ctrlSrv was created with (for account-switch rebuilds)
	ctrlSrvClient *deezer.Client       // client snapshotted into ctrlSrv by control.New
	hostAdv       *discovery.Responder // mDNS advertiser while Connect host is enabled
	clientID      = runtime.GOOS       // "android"
	deviceLabel   = "OpenDeezer (Android)"
)

// engineEQ bridges the control API's /eq endpoint to the engine player.
func engineEQ() *control.EQ {
	return control.PlayerEQ(func() control.EQController {
		if p := curPlayer(); p != nil {
			return p
		}
		return nil
	}, audio.EQPresetNames)
}

func startServices(c *deezer.Client) {
	servicesOnce.Do(func() {
		cfg := config.LoadControl()
		if !cfg.Enabled {
			return
		}
		ccfg := control.Config{Addr: cfg.Addr, Token: cfg.Token, SameAccountOnly: cfg.SameAccount}
		startControlServer(ccfg, c, !config.IsLoopbackAddr(cfg.Addr))
	})
}

// startControlServer builds a control server for ccfg, starts it, and ONLY on a
// successful Start publishes it (and, when advertise is set, a retained mDNS
// advertiser) to the shared globals under mu. On a bind failure nothing is
// published — ctrlSrv stays whatever it was (nil at first start), so a dead
// listener is never handed out as live (B11). The discovery.Responder is kept in
// hostAdv so it can be closed on logout / rebuild instead of leaking (B10).
// Returns the started server, or nil on failure.
func startControlServer(ccfg control.Config, c *deezer.Client, advertise bool) *control.Server {
	s := control.New(ccfg, engineState, engineAccount, engineCommands(), c)
	s.SetVersion(Version)
	s.SetClientInfo(clientID, deviceLabel)
	s.SetEQ(engineEQ())
	if err := s.Start(); err != nil {
		odlog.Warn("control api: %v", err)
		return nil // do NOT publish a server with no listener (B11)
	}
	var adv *discovery.Responder
	if advertise {
		if _, port, e := net.SplitHostPort(s.Addr()); e == nil {
			if p, e2 := strconv.Atoi(port); e2 == nil {
				adv, _ = discovery.Advertise(advertInfo, p)
			}
		}
	}
	mu.Lock()
	ctrlSrv, ctrlCfg, ctrlSrvClient = s, ccfg, c
	if adv != nil {
		if hostAdv != nil {
			hostAdv.Close() // replace a stale advertiser rather than leak it (B10)
		}
		hostAdv = adv
	}
	mu.Unlock()
	return s
}

// refreshControlServer rebuilds the control server around the current client
// after a re-login: control.New snapshots the *deezer.Client for the browse
// endpoints (/playlists, /search), so without this an account switch keeps
// serving the previous account's library on its stale session. Pairing
// sessions are dropped deliberately — a phone paired under the old account
// must re-pair. No-op when there is no server or it already holds c.
func refreshControlServer(c *deezer.Client) {
	mu.Lock()
	srv, cfg := ctrlSrv, ctrlCfg
	stale := srv != nil && ctrlSrvClient != c
	mu.Unlock()
	if !stale {
		return
	}
	cfg.Addr = srv.Addr() // keep the actual bound host:port (cfg may have said :0)
	pairing := srv.PairingActive()
	srv.Close()
	s := control.New(cfg, engineState, engineAccount, engineCommands(), c)
	s.SetVersion(Version)
	s.SetClientInfo(clientID, deviceLabel)
	s.SetEQ(engineEQ())
	if err := s.Start(); err != nil {
		odlog.Warn("control api restart: %v", err)
		mu.Lock()
		if ctrlSrv == srv {
			ctrlSrv = nil
		}
		adv := hostAdv
		hostAdv = nil
		mu.Unlock()
		if adv != nil {
			adv.Close() // the advertised port is gone
		}
		return
	}
	if pairing {
		s.EnablePairing() // fresh code; old sessions must not carry across accounts
	}
	mu.Lock()
	ctrlSrv, ctrlSrvClient = s, c
	mu.Unlock()
}

func advertInfo() discovery.Info {
	return discovery.Info{Name: engineAccount().Name, Client: clientID, Version: Version}
}
func engineAccount() control.Account {
	c := curClient()
	if c == nil {
		return control.Account{}
	}
	a := c.Account()
	return control.Account{UserID: a.UserID, Name: a.Name, Offer: a.Offer}
}
func engineState() control.State {
	st := control.State{State: "stopped"}
	// Queue + repeat + shuffle come from the engine queue, so /status reports
	// the real values once the app has synced them (SetQueueJSON / SetRepeat /
	// SetShuffle). An app that never syncs keeps the old wire shape: no queue,
	// repeat "off", shuffle false.
	queueMu.Lock()
	st.Repeat = engineQ.Repeat().String()
	st.Shuffle = engineQ.Shuffle()
	if ts := engineQ.Tracks(); len(ts) > 0 {
		qt := make([]control.Track, len(ts))
		for i, t := range ts {
			qt[i] = toControlTrack(t)
		}
		st.Queue = qt
	}
	queueMu.Unlock()
	p := curPlayer()
	if p == nil {
		return st
	}
	st.PositionMS, st.DurationMS = p.PositionMS(), p.DurationMS()
	st.Volume, st.Format = p.Volume(), p.Format()
	st.SleepActive, st.SleepEndOfTrack = p.SleepActive(), p.SleepEndOfTrack()
	st.SleepRemainingMS = p.SleepRemainingMS()
	switch p.State() {
	case audio.Playing:
		st.State = "playing"
	case audio.Paused:
		st.State = "paused"
	case audio.Loading:
		st.State = "loading"
	default:
		st.State = "stopped"
	}
	if cur := currentTrack(); cur.ID != "" {
		ct := toControlTrack(cur)
		st.Track = &ct
	}
	return st
}

// toControlTrack converts a Deezer track to the control API wire shape.
func toControlTrack(t deezer.Track) control.Track {
	ct := control.Track{
		ID: t.ID, Title: t.Name, Artist: t.ArtistLine(),
		Album: t.AlbumName, Explicit: t.Explicit, DurationMS: t.DurationMS,
		ArtworkURL: t.ArtworkURL,
	}
	if len(t.Artists) > 0 {
		ct.ArtistID = t.Artists[0].ID
	}
	return ct
}

// enginePlayResolved resolves a stream for t and starts it on the player,
// recording it as the current track. Blocking (network round-trip).
func enginePlayResolved(c *deezer.Client, p *audio.Player, t deezer.Track) bool {
	plan, err := c.PrepareStream(t.ID)
	if err != nil {
		return false
	}
	noteTrackTransition(p) // record the outgoing listen before the swap
	if p.Play(plan, t.DurationMS) != nil {
		return false
	}
	clearPreloadedTrack() // p.Play discarded any pending preload; drop its stash too
	setCurrentTrack(t)
	return true
}

// engineLoadAndPlay replaces the engine queue with ts (cursor at start) and
// plays the current track. The app detects a remotely loaded queue via
// QueueVersion/QueueJSON and resyncs its own list; natural-finish advance
// stays app-driven (FinishedCount), unchanged.
func engineLoadAndPlay(c *deezer.Client, p *audio.Player, ts []deezer.Track, start int) bool {
	queueMu.Lock()
	engineQ.Set(ts, start)
	t, ok := engineQ.Current()
	queueMu.Unlock()
	notifyControlState() // queue replaced even if the play below fails
	if !ok {
		return false
	}
	return enginePlayResolved(c, p, t)
}

// engineNext / enginePrev back the control API's /next + /prev. When routed to
// a Connect device they forward to it; otherwise they move the synced engine
// queue (no-op while it is empty) and play the new current track locally. The
// app can resync its own cursor afterwards via QueueIndex.
func engineNext() {
	if rc := routedRemote(); rc != nil {
		if st, err := rc.Next(); err == nil {
			setRemoteState(st)
		}
		return
	}
	queueMu.Lock()
	moved := engineQ.Next()
	t, ok := engineQ.Current()
	queueMu.Unlock()
	if !moved || !ok {
		return
	}
	if c, p := curClient(), curPlayer(); c != nil && p != nil {
		enginePlayResolved(c, p, t)
	}
}

func enginePrev() {
	if rc := routedRemote(); rc != nil {
		if st, err := rc.Prev(); err == nil {
			setRemoteState(st)
		}
		return
	}
	queueMu.Lock()
	moved := engineQ.Prev()
	t, ok := engineQ.Current()
	queueMu.Unlock()
	if !moved || !ok {
		return
	}
	if c, p := curClient(), curPlayer(); c != nil && p != nil {
		enginePlayResolved(c, p, t)
	}
}

func engineCommands() control.Commands {
	return control.Commands{
		PlayPause: func() { withPlayerNotify(func(p *audio.Player) { p.TogglePause() }) },
		Next:      engineNext,
		Prev:      enginePrev,
		// Both the cycle/toggle (legacy, no param) and set (explicit mode/on) verbs
		// are wired so /repeat and /shuffle work either way. Repeat derives from the
		// engine queue and is never forwarded to a routed remote (B2); shuffle
		// forwards.
		CycleRepeat:   engineCycleRepeat,
		ToggleShuffle: engineToggleShuffle,
		SetRepeat:     func(mode string) { engineSetRepeat(mode) },
		SetShuffle: func(on bool) {
			engineSetShuffle(on)
			if rc := routedRemote(); rc != nil {
				if st, err := rc.SetShuffle(on); err == nil {
					setRemoteState(st)
				}
			}
		},
		// Record the in-progress listen before halting: the next Play sees the
		// player Stopped and skips recording, so this is the only chance.
		Stop:      func() { withPlayerNotify(func(p *audio.Player) { noteTrackTransition(p); p.Stop() }) },
		Restart:   func() { withPlayerNotify(func(p *audio.Player) { p.SeekMS(0) }) },
		Seek:      func(ms int64) { withPlayerNotify(func(p *audio.Player) { p.SeekMS(ms) }) },
		SetVolume: func(v float64) { withPlayerNotify(func(p *audio.Player) { p.SetVolume(v) }) },
		PlayTrack: func(id string) {
			// Fetch the real duration first (mirrors corelib enginePlayTrack):
			// playing with 0 leaves /status durationMs at 0, and controllers'
			// end-of-track detectors require DurationMS > 0 — auto-advance
			// would stall after the first track.
			var durationMS int64
			if c := curClient(); c != nil {
				if t, err := c.Track(id); err == nil {
					durationMS = t.DurationMS
				}
			}
			Play(id, durationMS)
		},
		PlayPlaylist:  func(id string) {},
		QueueAdd:      engineQueueAdd,
		QueueJump:     engineQueueJump,
		QueueRemove:   engineQueueRemove,
		QueueMove:     engineQueueMove,
		PlayAlbum:     enginePlayAlbum,
		PlayMixTrack:  enginePlayMixTrack,
		PlayMixArtist: enginePlayMixArtist,
		HistoryRecent: engineHistoryRecent,
		SetSleepTimer: func(minutes int, eot bool) {
			withPlayerNotify(func(p *audio.Player) {
				p.SetSleepTimer(time.Duration(minutes)*time.Minute, eot)
			})
		},
		CancelSleepTimer: func() { withPlayerNotify(func(p *audio.Player) { p.CancelSleepTimer() }) },
	}
}

// ---- extended control commands: queue edits, album/mix play, history ----
//
// These back the control API's /queue/{add,jump,remove,move}, /play/album,
// /play/mix/{track,artist} and /history/recent endpoints. Like Next/Prev they
// forward to the routed Connect
// device when one is selected (this instance is then a controller, so queue
// edits belong to the remote's queue); HistoryRecent stays local — the
// listening history never leaves the machine that did the listening.

// engineQueueInsert is the pure queue mutation behind QueueAdd: insert t right
// after the current track (next=true) or append it to the end. A preloaded
// next stream is discarded — the upcoming track may have changed.
func engineQueueInsert(t deezer.Track, next bool) {
	queueMu.Lock()
	if next {
		engineQ.InsertAfterCurrent(t)
	} else {
		engineQ.Append(t)
	}
	queueMu.Unlock()
	clearEnginePreload()
	notifyControlState()
}

// engineQueueAdd backs POST /queue/add: resolve full track metadata (so
// /status shows title/artist/duration, and end-of-track detection works), then
// insert or append it.
func engineQueueAdd(id string, next bool) error {
	if rc := routedRemote(); rc != nil {
		st, err := rc.QueueAdd(id, next)
		if err == nil {
			setRemoteState(st)
		}
		return err
	}
	c := curClient()
	if c == nil {
		return fmt.Errorf("not logged in")
	}
	t, err := c.Track(id)
	if err != nil {
		return err
	}
	if t.ID == "" {
		return fmt.Errorf("track %s not found", id)
	}
	engineQueueInsert(t, next)
	return nil
}

// engineQueueSelect is the pure cursor move behind QueueJump: validate index,
// move the cursor (recording history so Prev retraces) and return the now-
// current track.
func engineQueueSelect(index int) (deezer.Track, error) {
	queueMu.Lock()
	defer queueMu.Unlock()
	if index < 0 || index >= engineQ.Len() {
		return deezer.Track{}, fmt.Errorf("index %d out of range (queue has %d tracks)", index, engineQ.Len())
	}
	engineQ.SetIndex(index)
	t, _ := engineQ.Current()
	return t, nil
}

// engineQueueJump backs POST /queue/jump: move the cursor to index and play
// that row through the normal engine play path. The app resyncs its own cursor
// via QueueIndex, exactly as after a remote /next|/prev.
func engineQueueJump(index int) error {
	if rc := routedRemote(); rc != nil {
		st, err := rc.QueueJump(index)
		if err == nil {
			setRemoteState(st)
		}
		return err
	}
	c, p := curClient(), curPlayer()
	if c == nil || p == nil {
		return fmt.Errorf("engine not ready")
	}
	t, err := engineQueueSelect(index)
	if err != nil {
		return err
	}
	if !enginePlayResolved(c, p, t) {
		return fmt.Errorf("could not start track %s", t.ID)
	}
	return nil
}

// engineQueueRemove backs POST /queue/remove. The playing row can't be removed
// (same guard as the TUI): the audio would keep playing a track that no longer
// exists in the queue, desyncing every controller.
func engineQueueRemove(index int) error {
	if rc := routedRemote(); rc != nil {
		st, err := rc.QueueRemove(index)
		if err == nil {
			setRemoteState(st)
		}
		return err
	}
	queueMu.Lock()
	if index < 0 || index >= engineQ.Len() {
		queueMu.Unlock()
		return fmt.Errorf("index %d out of range (queue has %d tracks)", index, engineQ.Len())
	}
	if index == engineQ.Index() {
		queueMu.Unlock()
		return fmt.Errorf("cannot remove the playing track")
	}
	engineQ.Remove(index)
	queueMu.Unlock()
	clearEnginePreload()
	notifyControlState()
	return nil
}

// engineQueueMove backs POST /queue/move; the cursor, history and shuffle
// cycle all follow the moved tracks (queue.Move), so reordering never changes
// what's audible. from == to is a no-op success.
func engineQueueMove(from, to int) error {
	if rc := routedRemote(); rc != nil {
		st, err := rc.QueueMove(from, to)
		if err == nil {
			setRemoteState(st)
		}
		return err
	}
	queueMu.Lock()
	if n := engineQ.Len(); from < 0 || from >= n || to < 0 || to >= n {
		queueMu.Unlock()
		return fmt.Errorf("from/to out of range (queue has %d tracks)", n)
	}
	moved := from != to && engineQ.Move(from, to)
	queueMu.Unlock()
	if moved {
		clearEnginePreload()
		notifyControlState()
	}
	return nil
}

// enginePlayFetched loads a fetched track list into the engine queue and plays
// the first track (shared tail of PlayAlbum / PlayMixTrack / PlayMixArtist).
func enginePlayFetched(what string, ts []deezer.Track, err error) error {
	if err != nil {
		return err
	}
	if len(ts) == 0 {
		return fmt.Errorf("%s has no playable tracks", what)
	}
	c, p := curClient(), curPlayer()
	if c == nil || p == nil {
		return fmt.Errorf("engine not ready")
	}
	if !engineLoadAndPlay(c, p, ts, 0) {
		return fmt.Errorf("could not start %s playback", what)
	}
	return nil
}

// enginePlayAlbum backs POST /play/album: replace the engine queue with the
// album's tracks and play the first.
func enginePlayAlbum(id string) error {
	if rc := routedRemote(); rc != nil {
		st, err := rc.PlayAlbum(id)
		if err == nil {
			setRemoteState(st)
		}
		return err
	}
	c := curClient()
	if c == nil {
		return fmt.Errorf("not logged in")
	}
	ts, err := c.AlbumTracks(id)
	return enginePlayFetched("album", ts, err)
}

// enginePlayMixTrack backs POST /play/mix/track ("song radio", seed kept as
// the first entry).
func enginePlayMixTrack(id string) error {
	if rc := routedRemote(); rc != nil {
		st, err := rc.PlayMixTrack(id)
		if err == nil {
			setRemoteState(st)
		}
		return err
	}
	c := curClient()
	if c == nil {
		return fmt.Errorf("not logged in")
	}
	ts, err := c.TrackMix(id)
	return enginePlayFetched("mix", ts, err)
}

// enginePlayMixArtist backs POST /play/mix/artist ("artist radio").
func enginePlayMixArtist(id string) error {
	if rc := routedRemote(); rc != nil {
		st, err := rc.PlayMixArtist(id)
		if err == nil {
			setRemoteState(st)
		}
		return err
	}
	c := curClient()
	if c == nil {
		return fmt.Errorf("not logged in")
	}
	ts, err := c.ArtistMix(id)
	return enginePlayFetched("mix", ts, err)
}

// engineHistoryRecent backs GET /history/recent with the machine-local
// listening log (newest first; history.Store.Recent never returns nil, so an
// empty log marshals to []).
func engineHistoryRecent(n int) (json.RawMessage, error) {
	st := historyStore()
	if st == nil {
		return nil, fmt.Errorf("history unavailable")
	}
	entries, err := st.Recent(n)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

// ---- local listening history recording ----

var (
	histMu     sync.Mutex
	histStore  *history.Store
	histInited bool
)

// historyStore lazily opens the shared local listening history at
// <config-dir>/history.jsonl (nil when no config dir is available). The store
// serializes its own file access; this only guards the lazy init.
func historyStore() *history.Store {
	histMu.Lock()
	defer histMu.Unlock()
	if !histInited {
		histInited = true
		if s, err := history.Default(); err == nil {
			histStore = s
		}
	}
	return histStore
}

// setHistoryStore overrides the history store (tests only).
func setHistoryStore(s *history.Store) {
	histMu.Lock()
	histStore, histInited = s, true
	histMu.Unlock()
}

// HistoryRecentJSON returns the newest n entries of the machine-local listening
// history as a JSON array (newest first, the same stable shape the control API's
// /history/recent serves); n <= 0 returns all. Empty/unavailable history yields
// "[]".
//
// Each entry is {trackId,title,artist,album?,kind?,startedAt,durationPlayedSec}.
// "kind" is omitted or "track" for a song and "episode" for a podcast episode —
// an app routes replay on it: episode -> PlayEpisodeMS(trackId), else PlayTrackMS.
func HistoryRecentJSON(n int) string {
	st := historyStore()
	if st == nil {
		return "[]"
	}
	entries, err := st.Recent(n)
	if err != nil {
		return "[]"
	}
	return jstr(entries, nil)
}

// HistoryStatsJSON returns local listening stats over the last sinceDays
// (sinceDays <= 0 = all history) as
// {topTracks:[{trackId,title,artist,plays,totalSec}],
//
//	topArtists:[{artist,plays,totalSec}], totalSeconds:N}. Empty/unavailable
//
// history yields the same shape with empty arrays and totalSeconds:0. topTracks
// and topArtists are music-only (podcast episodes excluded); totalSeconds counts
// all listening time.
func HistoryStatsJSON(sinceDays int) string {
	return jstr(historyStats(sinceDays), nil)
}

// jTrackStat / jArtistStat are the stable wire shapes for HistoryStatsJSON
// (history.TrackStat/ArtistStat carry no JSON tags, so we don't marshal them
// directly).
type jTrackStat struct {
	TrackID  string `json:"trackId"`
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	Plays    int    `json:"plays"`
	TotalSec int64  `json:"totalSec"`
}

type jArtistStat struct {
	Artist   string `json:"artist"`
	Plays    int    `json:"plays"`
	TotalSec int64  `json:"totalSec"`
}

// historyStats computes the listening-stats summary backing HistoryStatsJSON:
// {topTracks:[...], topArtists:[...], totalSeconds:N} over the last sinceDays
// (sinceDays <= 0 = all history). Always returns the full shape (empty arrays +
// 0) when the store is unavailable or empty.
func historyStats(sinceDays int) map[string]any {
	topTracks := []jTrackStat{}
	topArtists := []jArtistStat{}
	var total int64
	if st := historyStore(); st != nil {
		var since time.Time // zero = all history
		if sinceDays > 0 {
			since = time.Now().AddDate(0, 0, -sinceDays)
		}
		if tt, err := st.TopTracks(since, 50); err == nil {
			topTracks = make([]jTrackStat, len(tt))
			for i, t := range tt {
				topTracks[i] = jTrackStat{TrackID: t.TrackID, Title: t.Title, Artist: t.Artist, Plays: t.Plays, TotalSec: t.TotalSec}
			}
		}
		if ta, err := st.TopArtists(since, 50); err == nil {
			topArtists = make([]jArtistStat, len(ta))
			for i, a := range ta {
				topArtists[i] = jArtistStat{Artist: a.Artist, Plays: a.Plays, TotalSec: a.TotalSec}
			}
		}
		total, _ = st.TotalListenedSec(since)
	}
	return map[string]any{"topTracks": topTracks, "topArtists": topArtists, "totalSeconds": total}
}

// listenEntry converts an outgoing track + its media kind ("" = song,
// history.KindEpisode = podcast) + listened milliseconds into a history entry.
// ok=false when it isn't worth recording: no track id, or under a second of
// listening (start-skips, double-taps).
func listenEntry(prev deezer.Track, kind string, playedMS int64) (history.Entry, bool) {
	if prev.ID == "" || playedMS < 1000 {
		return history.Entry{}, false
	}
	if prev.DurationMS > 0 && playedMS > prev.DurationMS {
		playedMS = prev.DurationMS
	}
	return history.Entry{
		TrackID:           prev.ID,
		Title:             prev.Name,
		Artist:            prev.ArtistLine(),
		Album:             prev.AlbumName,
		Kind:              kind,
		StartedAt:         time.Now().Unix() - playedMS/1000,
		DurationPlayedSec: playedMS / 1000,
	}, true
}

// recordListen appends the outgoing track to the local listening history,
// tagged with its media kind so replay can route (song -> PlayTrackMS, episode
// -> PlayEpisodeMS). The file write (append + fsync) runs on its own goroutine
// so callers on the player's manage/callback path never block on disk I/O.
func recordListen(prev deezer.Track, kind string, playedMS int64) {
	e, ok := listenEntry(prev, kind, playedMS)
	if !ok {
		return
	}
	go func() {
		if st := historyStore(); st != nil {
			if err := st.Record(e); err != nil {
				odlog.Debug("history: %v", err)
			}
		}
	}()
}

// noteTrackTransition records the outgoing track when a new stream is about to
// replace one the user was actually listening to (manual skip / pick / new
// selection). Call BEFORE p.Play. A Stopped/Errored player means the previous
// track either ended naturally — already recorded by noteTrackFinished — or
// stopped long ago; neither may be re-recorded here, which also keeps
// pause/resume free of duplicates (they never reach this path at all).
func noteTrackTransition(p *audio.Player) {
	if p == nil {
		return
	}
	recordTransition(p.State(), p.PositionMS())
}

// recordTransition is noteTrackTransition's pure core, split out so tests can
// drive the Playing/Paused duplicate guard without a live audio device (a real
// player can't reach Playing in tests). A Stopped/Errored state records
// nothing — that's what makes a second Stop (or a Play after a natural finish)
// duplicate-free.
func recordTransition(state audio.State, positionMS int64) {
	switch state {
	case audio.Playing, audio.Paused:
		t, kind := currentTrackSnapshot()
		recordListen(t, kind, positionMS)
	}
}

// noteTrackFinished records the track that just ended naturally. It runs from
// the player's onFinish callback (the manage goroutine): for a real finish the
// player retains the end position; for a gapless/crossfade promote the player
// is already Playing the NEXT track, so the outgoing one played its full
// duration. An errored finish (B4) records nothing — it never played.
func noteTrackFinished() {
	p := curPlayer()
	if erroredFinish(p) {
		return
	}
	prev, kind := currentTrackSnapshot()
	playedMS := prev.DurationMS
	if p != nil && p.State() != audio.Playing {
		if pos := p.PositionMS(); pos > 0 {
			playedMS = pos
		}
	}
	recordListen(prev, kind, playedMS)
}

// erroredFinish reports whether the player's most recent finish was a failure
// rather than a natural end. The audio layer flips to Errored on device loss and
// stores the source's decode/stream error on LastError before firing onFinish;
// in both cases essentially nothing played (a successful Play clears LastError
// and the position, so a clean finish and a gapless promote both report an empty
// LastError). Such a finish must not be recorded as a listen or advance the
// queue (B4). A mid-track error that already played real audio (position past a
// second) is still treated as a genuine listen.
func erroredFinish(p *audio.Player) bool {
	if p == nil {
		return false
	}
	return isErroredFinish(p.State(), p.LastError(), p.PositionMS())
}

// isErroredFinish is erroredFinish's pure core, split out so tests can drive the
// errored-vs-natural decision without a live audio device (a real player can't
// be constructed headless). Both onFinish and noteTrackFinished gate on it.
func isErroredFinish(state audio.State, lastErr string, positionMS int64) bool {
	if state != audio.Errored && lastErr == "" {
		return false // a clean finish / gapless promote clears LastError
	}
	return positionMS < 1000 // errored with ~0 played: not a real listen
}

// ---- OpenDeezer Connect (controller side) ----

var (
	remoteCli  *control.Client
	remoteSt   control.State
	remoteAddr string
	remoteStop chan struct{}
)

func routedRemote() *control.Client { mu.Lock(); defer mu.Unlock(); return remoteCli }
func remoteSnapshot() control.State { mu.Lock(); defer mu.Unlock(); return remoteSt }
func setRemoteState(st control.State) {
	mu.Lock()
	if remoteCli != nil {
		// Remote track ended (the remote renders single tracks we send it; it has
		// no queue of its own) -> bump finished so the app's auto-advance fires
		// the next track, honoring repeat/shuffle. A track end shows up as
		// playing -> stopped near the end; the position guard keeps a user stop
		// from auto-advancing.
		if remoteSt.State == "playing" && st.State == "stopped" &&
			remoteSt.DurationMS > 0 && remoteSt.PositionMS >= remoteSt.DurationMS-2000 {
			finished++
		}
		remoteSt = st
	}
	mu.Unlock()
}
func remoteStateInt(s string) int {
	switch s {
	case "playing":
		return int(audio.Playing)
	case "paused":
		return int(audio.Paused)
	case "loading":
		return int(audio.Loading)
	default:
		return int(audio.Stopped)
	}
}

// SetClientInfo overrides the advertised client id + device label (before Init).
func SetClientInfo(clientName, device string) {
	mu.Lock()
	if clientName != "" {
		clientID = clientName
	}
	if device != "" {
		deviceLabel = device
	}
	mu.Unlock()
}

// DiscoverDevices returns LAN + configured Connect devices as a JSON array.
func DiscoverDevices(timeoutMS int) string {
	if timeoutMS <= 0 {
		timeoutMS = 700
	}
	// Read the shared control server under mu (all other accessors do); an unlocked
	// read races the web-remote/Connect-host toggles that (re)create it.
	mu.Lock()
	srv := ctrlSrv
	mu.Unlock()
	self := 0
	if srv != nil {
		if _, port, err := net.SplitHostPort(srv.Addr()); err == nil {
			self, _ = strconv.Atoi(port)
		}
	}
	devs, _ := discovery.Discover(time.Duration(timeoutMS)*time.Millisecond, self, config.PeerHostPorts()...)
	if devs == nil {
		devs = []discovery.Device{}
	}
	devs = mergeConfiguredPeers(devs)
	return jstr(devs, nil)
}

func mergeConfiguredPeers(devs []discovery.Device) []discovery.Device {
	peers := config.LoadPeers()
	if len(peers) == 0 {
		return devs
	}
	seen := map[string]bool{}
	for _, d := range devs {
		seen[d.Addr] = true
	}
	uid := UserID()
	for _, p := range peers {
		base, hp := config.NormalizePeer(p)
		if base == "" || seen[hp] {
			continue
		}
		seen[hp] = true
		name, cl, ver := hp, "", ""
		if who, err := control.NewClient(base, "", uid).Whoami(); err == nil {
			if who.Name != "" {
				name = who.Name
			}
			cl, ver = who.Client, who.Version
		}
		devs = append(devs, discovery.Device{Name: name, Addr: hp, Client: cl, Version: ver})
	}
	return devs
}

// ConnectDevice routes playback to the device at addr (host:port). Stops local
// playback (audio moves to the device). Returns true on success.
func ConnectDevice(addr string) bool {
	base, hp := config.NormalizePeer(addr)
	c := curClient()
	if base == "" || c == nil {
		return false
	}
	rc := control.NewClient(base, "", c.UserID())
	// /whoami is served unauthenticated by design, so it can't prove the peer is
	// controllable. Require an authed /status to succeed BEFORE stopping local
	// playback: a token-protected or different-account peer would otherwise 401
	// every command, leaving the user with dead audio and a "connected" UI.
	st, err := rc.Status()
	if err != nil {
		return false
	}
	if p := curPlayer(); p != nil {
		p.Stop()
	}

	// Sync the engine's current-track with what's playing on the remote,
	// so now-playing / lyrics reflect the remote immediately.
	if st.Track != nil {
		setCurrentTrack(deezer.Track{
			ID: st.Track.ID, Name: st.Track.Title, DurationMS: st.Track.DurationMS,
			Artists:   []deezer.Artist{{ID: st.Track.ArtistID, Name: st.Track.Artist}},
			AlbumName: st.Track.Album, Explicit: st.Track.Explicit,
			ArtworkURL: st.Track.ArtworkURL,
		})
	}

	mu.Lock()
	if remoteStop != nil {
		close(remoteStop)
	}
	remoteStop = make(chan struct{})
	stop := remoteStop
	remoteCli = rc
	remoteSt = st
	remoteAddr = hp
	mu.Unlock()
	// Normalize the host's repeat to off once (B2): we drive it a single track at
	// a time, so a lingering repeat-all/one on the host would loop that one track
	// forever. Repeat is kept local on this controller from here on (SetRepeat no
	// longer forwards). setRemoteState only applies while remoteCli is set, so run
	// it after publishing the connection above.
	if strp, err := rc.SetRepeat("off"); err == nil {
		setRemoteState(strp)
	}
	go remotePoller(rc, stop)
	return true
}

// DisconnectDevice returns control to local playback. Stops the remote device
// (so it doesn't keep playing unattended) before clearing the connection.
func DisconnectDevice() {
	mu.Lock()
	rc := remoteCli // capture before clearing; Stop is a network call — done outside lock
	if remoteStop != nil {
		close(remoteStop)
		remoteStop = nil
	}
	remoteCli = nil
	remoteSt = control.State{}
	remoteAddr = ""
	mu.Unlock()
	if rc != nil {
		_, _ = rc.Stop() // halt the remote; ignore error (fire-and-forget)
	}
}

// SetRepeat sets the repeat mode (mode: 0=off, 1=all, 2=one). The mode is
// recorded on the engine-side queue — /status reports it and the engine queue
// honors it when it advances. It is NOT forwarded to a connected remote device
// (B2): while casting, the controller keeps repeat local so the host's single-
// track queue never loops the current track. Read the host's own mode with
// GetRepeat.
func SetRepeat(mode int) {
	m := "off"
	switch mode {
	case 1:
		m = "all"
	case 2:
		m = "one"
	}
	engineSetRepeat(m)
}

// GetRepeat returns the current repeat mode ("off"|"all"|"one"). When routed to
// a Connect device it returns the remote host's mode (the routed snapshot), so
// the app renders the host's real mode while casting; otherwise it returns the
// engine queue's mode.
func GetRepeat() string {
	if routedRemote() != nil {
		if r := remoteSnapshot().Repeat; r != "" {
			return r
		}
	}
	queueMu.Lock()
	defer queueMu.Unlock()
	return engineQ.Repeat().String()
}

// GetShuffle reports whether shuffle is on. When routed to a Connect device it
// returns the remote host's flag (the routed snapshot), so the app renders the
// host's real mode while casting; otherwise the engine queue's flag.
func GetShuffle() bool {
	if routedRemote() != nil {
		return remoteSnapshot().Shuffle
	}
	queueMu.Lock()
	defer queueMu.Unlock()
	return engineQ.Shuffle()
}

// SetShuffle sets shuffle on (non-zero) or off (0). The flag is always recorded
// on the engine-side queue — /status reports it and the engine queue honors it
// when it advances — and it is forwarded to the connected remote device when
// one is selected.
func SetShuffle(on int) {
	engineSetShuffle(on != 0)
	if rc := routedRemote(); rc != nil {
		if st, err := rc.SetShuffle(on != 0); err == nil {
			setRemoteState(st)
		}
	}
}

// ConnectedDevice returns the connected device address ("" if local).
func ConnectedDevice() string {
	mu.Lock()
	defer mu.Unlock()
	return remoteAddr
}

func remotePoller(rc *control.Client, stop chan struct{}) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			st, err := rc.Status()
			if err != nil {
				continue
			}
			mu.Lock()
			active := remoteCli == rc
			if active {
				// A remote track ending shows up only in this passive poll (no
				// command is issued during normal playback), so detect the
				// playing -> stopped transition near the end here and bump finished
				// to fire the app's auto-advance — mirroring setRemoteState, which
				// only the command path reaches. Without this, remote playback halts
				// after each track. The position guard ignores user-initiated stops.
				if remoteSt.State == "playing" && st.State == "stopped" &&
					remoteSt.DurationMS > 0 && remoteSt.PositionMS >= remoteSt.DurationMS-2000 {
					finished++
				}
				remoteSt = st
			}
			mu.Unlock()
			// Sync current-track outside the lock (setCurrentTrack uses its own mutex).
			if active && st.Track != nil {
				setCurrentTrack(deezer.Track{
					ID: st.Track.ID, Name: st.Track.Title, DurationMS: st.Track.DurationMS,
					Artists:   []deezer.Artist{{ID: st.Track.ArtistID, Name: st.Track.Artist}},
					AlbumName: st.Track.Album, Explicit: st.Track.Explicit,
					ArtworkURL: st.Track.ArtworkURL,
				})
			}
		}
	}
}

// ---- Web remote (pairing-based phone remote) ----

// WebRemoteSetEnabled enables (on!=0) or disables (on==0) the web remote. When
// enabling, the control server is started on a LAN-reachable address if it is
// not already, and pairing is activated so a phone can scan the QR and connect.
func WebRemoteSetEnabled(on int) {
	if on != 0 {
		mobileEnsureWebRemoteServer()
	} else {
		mu.Lock()
		srv := ctrlSrv
		mu.Unlock()
		if srv != nil {
			srv.DisablePairing()
		}
	}
}

// WebRemoteInfo returns a JSON string:
// {"enabled":bool,"code":"123456","url":"http://<lanip>:<port>/remote","port":<int>}.
// code and url are empty when the remote is disabled.
func WebRemoteInfo() string {
	mu.Lock()
	srv := ctrlSrv
	mu.Unlock()
	if srv == nil || !srv.PairingActive() {
		b, _ := json.Marshal(map[string]any{"enabled": false, "code": "", "url": "", "port": 0})
		return string(b)
	}
	port := mobileSrvPort(srv)
	url := fmt.Sprintf("http://%s:%d/remote", mobileLANIPv4(), port)
	b, _ := json.Marshal(map[string]any{
		"enabled": true,
		"code":    srv.PairingCode(),
		"url":     url,
		"port":    port,
	})
	return string(b)
}

// WebRemoteQRPNG returns a PNG-encoded QR code for the web remote URL, or nil
// when the remote is disabled. Free-able by the caller (Go GC manages it).
func WebRemoteQRPNG() []byte {
	mu.Lock()
	srv := ctrlSrv
	mu.Unlock()
	if srv == nil || !srv.PairingActive() {
		return nil
	}
	port := mobileSrvPort(srv)
	url := fmt.Sprintf("http://%s:%d/remote", mobileLANIPv4(), port)
	png, err := qrcode.Encode(url, qrcode.Medium, 512)
	if err != nil {
		return nil
	}
	return png
}

// mobileEnsureWebRemoteServer ensures the control server is running on a
// LAN-reachable address and has pairing active.
func mobileEnsureWebRemoteServer() {
	if srv := mobileEnsureLANServer(); srv != nil {
		srv.EnablePairing()
	}
}

// mobileStartServer creates + starts a control server on addr with same-account
// auth, so same-account OpenDeezer devices can control it (Connect host) while
// the browser phone remote can still pair on top (a valid pairing session is
// checked before same-account auth). Returns nil on bind failure.
func mobileStartServer(addr string) *control.Server {
	// Carry the configured control token into the LAN rebind (B9): without it a
	// token-protected control API would be silently downgraded to token-less
	// (same-account only) auth when the phone remote / Connect host rebinds it onto
	// all interfaces. SameAccountOnly stays on so same-account devices still pair.
	cfg := control.Config{Addr: addr, Token: config.LoadControl().Token, SameAccountOnly: true}
	c := curClient()
	s := control.New(cfg, engineState, engineAccount, engineCommands(), c)
	s.SetVersion(Version)
	s.SetClientInfo(clientID, deviceLabel)
	s.SetEQ(engineEQ())
	if err := s.Start(); err != nil {
		return nil
	}
	mu.Lock()
	ctrlCfg, ctrlSrvClient = cfg, c
	mu.Unlock()
	return s
}

// mobileEnsureLANServer returns the control server bound to a LAN-reachable
// address, starting it (or rebinding a loopback-only server onto all
// interfaces) as needed. Shared by the phone remote (pairing) and the Connect
// host (mDNS discovery). A non-loopback server that already exists (e.g. from a
// config-driven Init) is reused as-is so any configured token/bind is preserved.
func mobileEnsureLANServer() *control.Server {
	mu.Lock()
	srv := ctrlSrv
	mu.Unlock()

	// Reuse an existing non-loopback server only when it is genuinely listening
	// (B11): a published-but-dead handle (its listener was closed, or an earlier
	// Start left a non-nil global with no listener) must be rebuilt, not handed
	// back as if live.
	if srv != nil && !mobileIsLoopback(srv.Addr()) && mobileServerListening(srv) {
		return srv
	}

	port := "7654"
	if srv != nil {
		if _, p, err := net.SplitHostPort(srv.Addr()); err == nil && p != "0" {
			port = p
		}
		srv.Close()
	}
	// The old server (and any port advertised for it) is gone: drop a stale
	// Connect-host advertiser so we never announce a dead port (B10). Re-advertise
	// on the fresh port below if it was active.
	mu.Lock()
	staleAdv := hostAdv
	hostAdv = nil
	mu.Unlock()
	wasAdvertising := staleAdv != nil
	if staleAdv != nil {
		staleAdv.Close()
	}

	newSrv := mobileStartServer("0.0.0.0:" + port)
	if newSrv == nil {
		newSrv = mobileStartServer("0.0.0.0:0")
	}
	if newSrv == nil {
		return nil
	}
	mu.Lock()
	ctrlSrv = newSrv
	mu.Unlock()
	if wasAdvertising {
		if a, err := discovery.Advertise(advertInfo, mobileSrvPort(newSrv)); err == nil {
			mu.Lock()
			hostAdv = a
			mu.Unlock()
		}
	}
	return newSrv
}

// mobileServerListening reports whether srv has a live TCP listener by dialing
// its bound port on loopback (mobileStartServer binds 0.0.0.0, reachable there).
// A published-but-dead server — Start failed, or its listener was closed — fails
// the dial, so mobileEnsureLANServer rebuilds instead of trusting a non-nil
// handle (B11).
func mobileServerListening(srv *control.Server) bool {
	if srv == nil {
		return false
	}
	_, port, err := net.SplitHostPort(srv.Addr())
	if err != nil || port == "" || port == "0" {
		return false
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// ---- OpenDeezer Connect host (make this device controllable) ----

// ConnectHostSetEnabled makes this device a discoverable OpenDeezer Connect
// target on the LAN (on!=0), so other devices signed into the same Deezer
// account can find it in their device picker and drive its playback; or stops
// advertising it (on==0). The control server runs on a LAN address with
// same-account auth. Idempotent.
func ConnectHostSetEnabled(on int) {
	if on == 0 {
		mu.Lock()
		adv := hostAdv
		hostAdv = nil
		mu.Unlock()
		if adv != nil {
			adv.Close()
		}
		return
	}

	srv := mobileEnsureLANServer()
	if srv == nil {
		return
	}
	mu.Lock()
	already := hostAdv != nil
	mu.Unlock()
	if already {
		return // already advertising
	}
	adv, err := discovery.Advertise(advertInfo, mobileSrvPort(srv))
	if err != nil {
		return
	}
	mu.Lock()
	hostAdv = adv
	mu.Unlock()
}

// ConnectHostInfo returns a JSON string:
// {"enabled":bool,"addr":"<lanip>:<port>","port":<int>,"name":"<account>"}.
// addr/name are empty when the Connect host is disabled.
func ConnectHostInfo() string {
	mu.Lock()
	srv := ctrlSrv
	enabled := hostAdv != nil
	mu.Unlock()
	if !enabled || srv == nil {
		b, _ := json.Marshal(map[string]any{"enabled": false, "addr": "", "port": 0, "name": ""})
		return string(b)
	}
	port := mobileSrvPort(srv)
	b, _ := json.Marshal(map[string]any{
		"enabled": true,
		"addr":    fmt.Sprintf("%s:%d", mobileLANIPv4(), port),
		"port":    port,
		"name":    engineAccount().Name,
	})
	return string(b)
}

func mobileSrvPort(srv *control.Server) int {
	_, port, err := net.SplitHostPort(srv.Addr())
	if err != nil {
		return 7654
	}
	p, _ := strconv.Atoi(port)
	return p
}

// mobileLANIPv4 returns the primary non-loopback IPv4 of this device.
func mobileLANIPv4() string {
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip4 := ip.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}
	return "127.0.0.1"
}

func mobileIsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	switch host {
	case "", "0.0.0.0", "::":
		return false
	case "localhost":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// ---- equalizer + mono downmix (v1.7) ----

// EQJSON returns the equalizer state:
// {enabled,mono,preampDb,gainsDb:[10],preset,bands:[10],presets:[...]}.
// Same wire shape as corelib's DZEQJSON so every client renders the same UI.
func EQJSON() string {
	p := curPlayer()
	if p == nil {
		return jstr(nil, fmt.Errorf("engine not ready"))
	}
	return jstr(map[string]any{
		"enabled":  p.EQEnabled(),
		"mono":     p.MonoDownmix(),
		"preampDb": p.EQPreampDB(),
		"gainsDb":  p.EQGains(),
		"preset":   p.EQPreset(),
		"bands":    p.EQBands(),
		"presets":  audio.EQPresetNames,
	}, nil)
}

// SetEQJSON applies a partial EQ update. Recognized keys (all optional):
// enabled (bool), mono (bool), preampDb (number), gainsDb ([10]number),
// preset (string), band ({"index":N,"gainDb":X}). Returns false if any present
// key failed to apply.
func SetEQJSON(js string) bool {
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
	if err := json.Unmarshal([]byte(js), &req); err != nil {
		return false
	}
	p := curPlayer()
	if p == nil {
		return false
	}
	ok := true
	if req.Enabled != nil {
		p.SetEQEnabled(*req.Enabled)
	}
	if req.Mono != nil {
		p.SetMonoDownmix(*req.Mono)
	}
	if req.PreampDB != nil {
		p.SetEQPreamp(*req.PreampDB)
	}
	if req.Preset != nil && p.SetEQPreset(*req.Preset) != nil {
		ok = false
	}
	if req.GainsDB != nil && p.SetEQGains(*req.GainsDB) != nil {
		ok = false
	}
	if req.Band != nil && p.SetEQGain(req.Band.Index, req.Band.GainDB) != nil {
		ok = false
	}
	return ok
}

// ---- logout ----

// Logout tears the engine session down: stops playback, closes the control
// server (dropping web-remote pairing sessions and the old account's
// same-account auth), stops Connect-host advertising, and forgets the Deezer
// client. A later Init starts services fresh for the new account.
func Logout() {
	// Clear ALL remote-routing state first (B8): remoteCli/remoteSt/remoteAddr/
	// remoteStop must not survive the session wipe, or commands would keep routing
	// to the old device under the next account. DisconnectDevice also halts the
	// remote device (it shouldn't keep playing unattended after logout).
	DisconnectDevice()
	if p := curPlayer(); p != nil {
		p.Stop()
	}
	mu.Lock()
	srv := ctrlSrv
	adv := hostAdv
	ctrlSrv, ctrlSrvClient, hostAdv = nil, nil, nil
	ctrlCfg = control.Config{}
	client = nil
	servicesOnce = sync.Once{} // allow startServices on the next Init
	mu.Unlock()
	if adv != nil {
		adv.Close()
	}
	if srv != nil {
		srv.Close()
	}
}
