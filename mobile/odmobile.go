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

	"github.com/Cycl0o0/OpenDeezer/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/internal/config"
	"github.com/Cycl0o0/OpenDeezer/internal/control"
	"github.com/Cycl0o0/OpenDeezer/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/internal/discovery"
	odlog "github.com/Cycl0o0/OpenDeezer/internal/log"
)

// Version is the engine/app version.
const Version = "1.0.0"

var (
	mu       sync.Mutex
	client   *deezer.Client
	player   *audio.Player
	finished int

	curMu    sync.Mutex
	curTrack deezer.Track
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
func setCurrentTrack(t deezer.Track) {
	curMu.Lock()
	curTrack = t
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
	return true
}

// LoggedIn reports whether Init succeeded.
func LoggedIn() bool { c := curClient(); return c != nil && c.LoggedIn() }

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

func SearchPodcasts(q string) string {
	return withClient(func(c *deezer.Client) (any, error) {
		ps, err := c.SearchPodcasts(q)
		return map[string]any{"podcasts": ps}, err
	})
}
func PodcastEpisodes(id string) string {
	return withClient(func(c *deezer.Client) (any, error) {
		es, err := c.PodcastEpisodes(id)
		return map[string]any{"episodes": es}, err
	})
}

// PlayEpisode resolves + plays a podcast episode (plain stream).
func PlayEpisode(id string) bool {
	c, p := curClient(), curPlayer()
	if c == nil || p == nil {
		return false
	}
	plan, err := c.PodcastEpisodeStream(id)
	if err != nil {
		odlog.Warn("episode %s: %v", id, err)
		return false
	}
	if err := p.Play(plan, 0); err != nil {
		return false
	}
	return true
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
	setCurrentTrack(deezer.Track{ID: trackID, DurationMS: durationMS})
	go fetchTrackMeta(c, trackID)
	return true
}

func fetchTrackMeta(c *deezer.Client, id string) {
	if t, err := c.Track(id); err == nil && t.ID != "" && currentTrack().ID == id {
		setCurrentTrack(t)
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
				ID: t.ID, Name: t.Title, ArtistLine: t.Artist, AlbumName: t.Album,
				Explicit: t.Explicit, DurationMS: t.DurationMS,
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
	servicesOnce sync.Once
	ctrlSrv      *control.Server
	clientID     = runtime.GOOS // "android"
	deviceLabel  = "OpenDeezer (Android)"
)

func startServices(c *deezer.Client) {
	servicesOnce.Do(func() {
		if cfg := config.LoadControl(); cfg.Enabled {
			ctrlSrv = control.New(
				control.Config{Addr: cfg.Addr, Token: cfg.Token, SameAccountOnly: cfg.SameAccount},
				engineState, engineAccount, engineCommands(), c,
			)
			ctrlSrv.SetVersion(Version)
			ctrlSrv.SetClientInfo(clientID, deviceLabel)
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
		st.Track = &control.Track{
			ID: cur.ID, Title: cur.Name, Artist: cur.ArtistLine(),
			Album: cur.AlbumName, Explicit: cur.Explicit, DurationMS: cur.DurationMS,
		}
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
		PlayTrack:    func(id string) { Play(id, 0) },
		PlayPlaylist: func(id string) {},
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
	self := 0
	if ctrlSrv != nil {
		if _, port, err := net.SplitHostPort(ctrlSrv.Addr()); err == nil {
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
	if _, err := rc.Whoami(); err != nil {
		return false
	}
	if p := curPlayer(); p != nil {
		p.Stop()
	}
	st, _ := rc.Status()
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

// DisconnectDevice returns control to local playback.
func DisconnectDevice() {
	mu.Lock()
	if remoteStop != nil {
		close(remoteStop)
		remoteStop = nil
	}
	remoteCli = nil
	remoteSt = control.State{}
	remoteAddr = ""
	mu.Unlock()
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
			if st, err := rc.Status(); err == nil {
				mu.Lock()
				if remoteCli == rc {
					remoteSt = st
				}
				mu.Unlock()
			}
		}
	}
}
