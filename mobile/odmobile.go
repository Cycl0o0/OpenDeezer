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
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/Cycl0o0/OpenDeezer/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/internal/config"
	"github.com/Cycl0o0/OpenDeezer/internal/control"
	"github.com/Cycl0o0/OpenDeezer/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/internal/discovery"
	odlog "github.com/Cycl0o0/OpenDeezer/internal/log"
	"github.com/Cycl0o0/OpenDeezer/internal/update"
	"github.com/Cycl0o0/OpenDeezer/internal/version"
)

// Version is the engine/app version (single source: internal/version).
const Version = version.Number

var (
	mu       sync.Mutex
	client   *deezer.Client
	player   *audio.Player
	finished int

	curMu    sync.Mutex
	curTrack deezer.Track
	curGen   uint64 // bumped by setCurrentTrack; lets async meta fetches detect a newer track
)

// ---- JSON DTOs (same wire shape as the desktop GUIs) ----

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
	ArtistID   string    `json:"artistId,omitempty"` // primary artist id (convenience field)
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
type jArtistInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ArtworkURL string `json:"artworkUrl"`
	NbFans     int    `json:"nbFans"`
}

func toJTrack(t deezer.Track) jTrack {
	as := make([]jArtist, len(t.Artists))
	for i, a := range t.Artists {
		as[i] = jArtist{ID: a.ID, Name: a.Name}
	}
	artistID := ""
	if len(t.Artists) > 0 {
		artistID = t.Artists[0].ID
	}
	return jTrack{
		ID: t.ID, Name: t.Name, DurationMS: t.DurationMS, Artists: as,
		ArtistLine: t.ArtistLine(), ArtistID: artistID, AlbumName: t.AlbumName,
		ArtworkURL: t.ArtworkURL, Explicit: t.Explicit,
	}
}
func toJTracks(ts []deezer.Track) []jTrack {
	out := make([]jTrack, len(ts))
	for i, t := range ts {
		out[i] = toJTrack(t)
	}
	return out
}
func toJArtistInfos(as []deezer.ArtistInfo) []jArtistInfo {
	out := make([]jArtistInfo, len(as))
	for i, a := range as {
		out[i] = jArtistInfo{ID: a.ID, Name: a.Name, ArtworkURL: a.ArtworkURL, NbFans: a.NbFans}
	}
	return out
}

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
	defer curMu.Unlock()
	curGen++
	curTrack = t
	return curGen
}

// setCurrentTrackAt applies an async metadata enrichment only if no newer
// setCurrentTrack landed since gen (a bare ID re-check would race a concurrent
// Play and pin the previous track's metadata for the whole next track).
func setCurrentTrackAt(gen uint64, t deezer.Track) {
	curMu.Lock()
	if curGen == gen {
		curTrack = t
	}
	curMu.Unlock()
}
func currentTrack() deezer.Track {
	curMu.Lock()
	defer curMu.Unlock()
	return curTrack
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
		player.SetOnFinish(func() {
			mu.Lock()
			finished++
			mu.Unlock()
		})
	}
	mu.Unlock()

	c := deezer.New(arl)
	if err := c.Login(); err != nil {
		odlog.Warn("login failed: %v", err)
		return false
	}
	mu.Lock()
	client = c
	mu.Unlock()
	startServices(c)
	refreshControlServer(c)
	return true
}

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
	if p := curPlayer(); p != nil {
		p.SetSleepTimer(time.Duration(minutes)*time.Minute, endOfTrack != 0)
	}
}

// CancelSleepTimer disarms the sleep timer.
func CancelSleepTimer() {
	if p := curPlayer(); p != nil {
		p.CancelSleepTimer()
	}
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
		return map[string]any{"tracks": toJTracks(ts)}, err
	})
}
func Playlists() string {
	return withClient(func(c *deezer.Client) (any, error) {
		ps, err := c.Playlists()
		out := make([]jPlaylist, len(ps))
		for i, p := range ps {
			out[i] = jPlaylist{ID: p.ID, Name: p.Name, Owner: p.Owner, TrackCount: p.TrackCount, ArtworkURL: p.ArtworkURL}
		}
		return map[string]any{"playlists": out}, err
	})
}
func PlaylistTracks(id string) string {
	return withClient(func(c *deezer.Client) (any, error) {
		ts, err := c.PlaylistTracks(id)
		return map[string]any{"tracks": toJTracks(ts)}, err
	})
}
func AlbumTracks(id string) string {
	return withClient(func(c *deezer.Client) (any, error) {
		ts, err := c.AlbumTracks(id)
		return map[string]any{"tracks": toJTracks(ts)}, err
	})
}
func Flow() string {
	return withClient(func(c *deezer.Client) (any, error) {
		ts, err := c.Flow()
		return map[string]any{"tracks": toJTracks(ts)}, err
	})
}
func ArtistTop(id string) string {
	return withClient(func(c *deezer.Client) (any, error) {
		ts, err := c.ArtistTop(id)
		return map[string]any{"tracks": toJTracks(ts)}, err
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
		al := make([]jAlbum, len(albums))
		for i, a := range albums {
			as := make([]jArtist, len(a.Artists))
			for j, ar := range a.Artists {
				as[j] = jArtist{ID: ar.ID, Name: ar.Name}
			}
			al[i] = jAlbum{ID: a.ID, Name: a.Name, Artists: as, ArtworkURL: a.ArtworkURL}
		}
		ps, _ := c.Playlists()
		pl := make([]jPlaylist, len(ps))
		for i, p := range ps {
			pl[i] = jPlaylist{ID: p.ID, Name: p.Name, Owner: p.Owner, TrackCount: p.TrackCount, ArtworkURL: p.ArtworkURL}
		}
		return map[string]any{"topTracks": toJTracks(tracks), "topAlbums": al, "playlists": pl}, nil
	})
}

func searchJSON(tracks []deezer.Track, albums []deezer.Album, artists []deezer.ArtistInfo, playlists []deezer.Playlist) string {
	al := make([]jAlbum, len(albums))
	for i, a := range albums {
		as := make([]jArtist, len(a.Artists))
		for j, ar := range a.Artists {
			as[j] = jArtist{ID: ar.ID, Name: ar.Name}
		}
		al[i] = jAlbum{ID: a.ID, Name: a.Name, Artists: as, ArtworkURL: a.ArtworkURL}
	}
	pl := make([]jPlaylist, len(playlists))
	for i, p := range playlists {
		pl[i] = jPlaylist{ID: p.ID, Name: p.Name, Owner: p.Owner, TrackCount: p.TrackCount, ArtworkURL: p.ArtworkURL}
	}
	return jstr(map[string]any{
		"tracks": toJTracks(tracks), "albums": al,
		"artists": toJArtistInfos(artists), "playlists": pl,
	}, nil)
}

// ---- podcasts ----

// jPodcast / jEpisode mirror corelib's DTOs: deezer.Podcast/Episode have no
// json tags, so marshaling them raw would break the lowercase wire contract.
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
	PodcastName string `json:"podcastName"`
}

func SearchPodcasts(q string) string {
	return withClient(func(c *deezer.Client) (any, error) {
		ps, err := c.SearchPodcasts(q)
		out := make([]jPodcast, len(ps))
		for i, p := range ps {
			out[i] = jPodcast{ID: p.ID, Name: p.Name, Description: p.Description, ArtworkURL: p.ArtworkURL, EpisodeCount: p.EpisodeCount}
		}
		return map[string]any{"podcasts": out}, err
	})
}
func PodcastEpisodes(id string) string {
	return withClient(func(c *deezer.Client) (any, error) {
		es, err := c.PodcastEpisodes(id)
		out := make([]jEpisode, len(es))
		for i, e := range es {
			out[i] = jEpisode{ID: e.ID, Title: e.Title, Description: e.Description, ArtworkURL: e.ArtworkURL, DurationMS: e.DurationMS, ReleaseDate: e.ReleaseDate, PodcastName: e.PodcastName}
		}
		return map[string]any{"episodes": out}, err
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
	if err := p.Play(plan, durationMS); err != nil {
		return false
	}
	gen := setCurrentTrack(deezer.Track{ID: id, DurationMS: durationMS})
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
	if err := p.Play(plan, durationMS); err != nil {
		return false
	}
	gen := setCurrentTrack(deezer.Track{ID: trackID, DurationMS: durationMS})
	go fetchTrackMeta(c, trackID, gen)
	return true
}

func fetchTrackMeta(c *deezer.Client, id string, gen uint64) {
	if t, err := c.Track(id); err == nil && t.ID != "" {
		setCurrentTrackAt(gen, t)
	}
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
	if p := curPlayer(); p != nil {
		p.Pause()
	}
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
	if p := curPlayer(); p != nil {
		p.Resume()
	}
}
func TogglePause() {
	if rc := routedRemote(); rc != nil {
		if st, err := rc.PlayPause(); err == nil {
			setRemoteState(st)
		}
		return
	}
	if p := curPlayer(); p != nil {
		p.TogglePause()
	}
}
func Stop() {
	if rc := routedRemote(); rc != nil {
		if st, err := rc.Stop(); err == nil {
			setRemoteState(st)
		}
		return
	}
	if p := curPlayer(); p != nil {
		p.Stop()
	}
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
	if p := curPlayer(); p != nil {
		p.SeekMS(ms)
	}
}
func SetVolume(v float64) {
	if rc := routedRemote(); rc != nil {
		if st, err := rc.SetVolume(v); err == nil {
			setRemoteState(st)
		}
		return
	}
	if p := curPlayer(); p != nil {
		p.SetVolume(v)
	}
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
			return jstr(jTrack{
				ID: t.ID, Name: t.Title, ArtistLine: t.Artist, ArtistID: t.ArtistID,
				AlbumName: t.Album, Explicit: t.Explicit, DurationMS: t.DurationMS,
				ArtworkURL: t.ArtworkURL,
			}, nil)
		}
		return jstr(map[string]any{}, nil)
	}
	if cur := currentTrack(); cur.ID != "" {
		return jstr(toJTrack(cur), nil)
	}
	return jstr(map[string]any{}, nil)
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
		if cfg := config.LoadControl(); cfg.Enabled {
			ccfg := control.Config{Addr: cfg.Addr, Token: cfg.Token, SameAccountOnly: cfg.SameAccount}
			ctrlSrv = control.New(ccfg, engineState, engineAccount, engineCommands(), c)
			ctrlCfg, ctrlSrvClient = ccfg, c
			ctrlSrv.SetVersion(Version)
			ctrlSrv.SetClientInfo(clientID, deviceLabel)
			ctrlSrv.SetEQ(engineEQ())
			if err := ctrlSrv.Start(); err == nil {
				if !config.IsLoopbackAddr(cfg.Addr) {
					if _, port, e := net.SplitHostPort(ctrlSrv.Addr()); e == nil {
						if p, e2 := strconv.Atoi(port); e2 == nil {
							_, _ = discovery.Advertise(advertInfo, p)
						}
					}
				}
			}
		}
	})
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
	p := curPlayer()
	if p == nil {
		return control.State{State: "stopped"}
	}
	cur := currentTrack()
	st := control.State{
		PositionMS: p.PositionMS(), DurationMS: p.DurationMS(),
		Volume: p.Volume(), Repeat: "off", Format: p.Format(),
		SleepActive: p.SleepActive(), SleepEndOfTrack: p.SleepEndOfTrack(),
		SleepRemainingMS: p.SleepRemainingMS(),
	}
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
	if cur.ID != "" {
		ct := &control.Track{
			ID: cur.ID, Title: cur.Name, Artist: cur.ArtistLine(),
			Album: cur.AlbumName, Explicit: cur.Explicit, DurationMS: cur.DurationMS,
			ArtworkURL: cur.ArtworkURL,
		}
		if len(cur.Artists) > 0 {
			ct.ArtistID = cur.Artists[0].ID
		}
		st.Track = ct
	}
	return st
}
func engineCommands() control.Commands {
	return control.Commands{
		PlayPause: func() {
			if p := curPlayer(); p != nil {
				p.TogglePause()
			}
		},
		Stop: func() {
			if p := curPlayer(); p != nil {
				p.Stop()
			}
		},
		Restart: func() {
			if p := curPlayer(); p != nil {
				p.SeekMS(0)
			}
		},
		Seek: func(ms int64) {
			if p := curPlayer(); p != nil {
				p.SeekMS(ms)
			}
		},
		SetVolume: func(v float64) {
			if p := curPlayer(); p != nil {
				p.SetVolume(v)
			}
		},
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
		PlayPlaylist: func(id string) {},
		SetSleepTimer: func(minutes int, eot bool) {
			if p := curPlayer(); p != nil {
				p.SetSleepTimer(time.Duration(minutes)*time.Minute, eot)
			}
		},
		CancelSleepTimer: func() {
			if p := curPlayer(); p != nil {
				p.CancelSleepTimer()
			}
		},
	}
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
	devs, _ := discovery.Discover(time.Duration(timeoutMS)*time.Millisecond, self)
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

// SetRepeat sets the repeat mode on the connected remote device
// (mode: 0=off, 1=all, 2=one). No-op when playing locally — GUIs own their queue.
func SetRepeat(mode int) {
	rc := routedRemote()
	if rc == nil {
		return
	}
	m := "off"
	switch mode {
	case 1:
		m = "all"
	case 2:
		m = "one"
	}
	if st, err := rc.SetRepeat(m); err == nil {
		setRemoteState(st)
	}
}

// SetShuffle sets shuffle on (non-zero) or off (0) on the connected remote device.
// No-op when playing locally — GUIs own their queue.
func SetShuffle(on int) {
	rc := routedRemote()
	if rc == nil {
		return
	}
	if st, err := rc.SetShuffle(on != 0); err == nil {
		setRemoteState(st)
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
	cfg := control.Config{Addr: addr, SameAccountOnly: true}
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

	if srv != nil && !mobileIsLoopback(srv.Addr()) {
		return srv
	}

	port := "7654"
	if srv != nil {
		if _, p, err := net.SplitHostPort(srv.Addr()); err == nil {
			port = p
		}
		srv.Close()
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
	return newSrv
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
