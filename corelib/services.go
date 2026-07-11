package main

// Engine-hosted services shared by every native GUI: Discord Rich Presence and
// the control API (remote control + MCP). They run inside the c-archive so the
// GUIs get them with no native code — the GUI just plays tracks via DZPlay and
// the engine tracks the current one for now-playing + remote status.
//
// Engine-side control covers the player-level actions (play/pause, stop, seek,
// volume, restart) plus play-by-id; next/prev/shuffle/repeat live in the GUI's
// own queue and are not exposed here.

import (
	"net"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/Cycl0o0/OpenDeezer/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/internal/config"
	"github.com/Cycl0o0/OpenDeezer/internal/control"
	"github.com/Cycl0o0/OpenDeezer/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/internal/discord"
	"github.com/Cycl0o0/OpenDeezer/internal/discovery"
	odlog "github.com/Cycl0o0/OpenDeezer/internal/log"
	"github.com/Cycl0o0/OpenDeezer/internal/queue"
	"github.com/Cycl0o0/OpenDeezer/internal/version"
)

var (
	servicesOnce sync.Once
	dp           discord.Presence
	ctrlSrv      *control.Server
	coreVersion  = version.Number

	// ctrlCfg + ctrlSrvUserID remember how ctrlSrv was built so
	// refreshControlServer can rebuild it around a new client after an account
	// switch (re-login), instead of the sync.Once keeping the old account's
	// session alive forever. We track the account UserID (not the *deezer.Client
	// pointer) because DZInit allocates a fresh client on every call, so a
	// same-account re-login must compare equal and stay a no-op.
	ctrlCfg       control.Config
	ctrlSrvUserID string

	curMu    sync.Mutex
	curTrack deezer.Track

	// engineQ is the engine-side playback queue. enginePlayPlaylist /
	// enginePlayTrack load the full track list into it and play the current track;
	// when a track ends naturally the player's onFinish callback advances this
	// queue and starts the next track, so remote/control-driven playlist playback
	// walks every track instead of stopping after the first. Guarded by queueMu.
	queueMu sync.Mutex
	engineQ = queue.New()
)

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

// curPlayer reads the live player global. (curClient is defined in deezercore.go.)
func curPlayer() *audio.Player {
	mu.Lock()
	defer mu.Unlock()
	return player
}

// fetchTrackMeta fills in the full metadata for the current track (title/artist/
// album), so Discord + remote status show more than an id. Best-effort.
func fetchTrackMeta(c *deezer.Client, id string) {
	if c == nil || id == "" {
		return
	}
	if t, err := c.Track(id); err == nil && t.ID != "" {
		// Only keep it if the user hasn't moved on to another track meanwhile.
		if currentTrack().ID == id {
			setCurrentTrack(t)
		}
	}
}

// startServices starts Discord RP + the control API once, after a successful
// login. The just-logged-in client is passed in; closures read globals lazily.
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
		appID := config.LoadDiscordAppID()
		dp = discord.New(appID)
		if appID == "" {
			odlog.Info("discord: rich presence disabled (no app id; set discord-app-id.txt)")
		} else {
			odlog.Info("discord: rich presence enabled (app %s)", appID)
		}

		if cfg := config.LoadControl(); cfg.Enabled {
			id, dev := clientInfo()
			// Build + Start on a local var, then publish under mu: the exported
			// control funcs read/write ctrlSrv on the UI thread while this runs on
			// a DZInit worker, and a reader must never see a not-yet-listening srv.
			ccfg := control.Config{Addr: cfg.Addr, Token: cfg.Token, SameAccountOnly: cfg.SameAccount}
			srv := control.New(
				ccfg,
				engineState,
				engineAccount,
				engineCommands(),
				c,
			)
			// Set identity BEFORE Start so the serving goroutine never races these.
			srv.SetVersion(coreVersion)
			srv.SetClientInfo(id, dev)
			srv.SetEQ(engineEQ())
			if cfg.SameAccount && cfg.Token == "" {
				odlog.Warn("control api: LAN-exposed with same-account auth only; the Deezer " +
					"user id is not a strong secret. Set OPENDEEZER_CONTROL_TOKEN for a real " +
					"credential on untrusted networks.")
			}
			if err := srv.Start(); err != nil {
				odlog.Warn("control api: %v", err)
			} else {
				addr := srv.Addr()
				mu.Lock()
				ctrlSrv = srv
				ctrlCfg, ctrlSrvUserID = ccfg, c.Account().UserID
				mu.Unlock()
				odlog.Info("control api on %s", addr)
				// Advertise on the LAN (OpenDeezer Connect) only when bound to a
				// reachable (non-loopback) address.
				if !config.IsLoopbackAddr(cfg.Addr) {
					if _, port, err := net.SplitHostPort(addr); err == nil {
						if p, e := strconv.Atoi(port); e == nil {
							if _, e := discovery.Advertise(advertInfo, p); e == nil {
								odlog.Info("discovery advertising control port %d", p)
							}
						}
					}
				}
			}
		}

		go serviceTicker()
	})
}

// refreshControlServer rebuilds the control server around the current client
// after a re-login: control.New snapshots the *deezer.Client for the browse
// endpoints (/playlists, /search), so without this an account switch (a second
// DZInit with a new ARL) keeps serving the previous account's library on its
// stale session — startServices runs under a sync.Once and never re-runs.
// Pairing sessions are dropped deliberately: a phone paired under the old
// account must re-pair. No-op when there is no server or it already holds c.
//
// The server rebinds to the same host:port, so the discovery responder started
// by startServices (which reads identity live via advertInfo and points at that
// unchanged port) keeps advertising correctly — no re-advertise needed.
func refreshControlServer(c *deezer.Client) {
	mu.Lock()
	srv, cfg := ctrlSrv, ctrlCfg
	stale := srv != nil && ctrlSrvUserID != c.Account().UserID
	mu.Unlock()
	if !stale {
		return
	}
	cfg.Addr = srv.Addr() // keep the actual bound host:port (cfg may have said :0)
	pairing := srv.PairingActive()
	srv.Close()
	id, dev := clientInfo()
	s := control.New(cfg, engineState, engineAccount, engineCommands(), c)
	s.SetVersion(coreVersion)
	s.SetClientInfo(id, dev)
	s.SetEQ(engineEQ())
	if err := s.Start(); err != nil {
		odlog.Warn("control api restart: %v", err)
		mu.Lock()
		if ctrlSrv == srv {
			// Reset the tracked identity too, so a later DZInit sees srv==nil is
			// stale-worthy again (srv!=nil is required to detect staleness) and the
			// control API isn't left permanently down.
			ctrlSrv, ctrlSrvUserID = nil, ""
		}
		mu.Unlock()
		return
	}
	if pairing {
		s.EnablePairing() // fresh code; old sessions must not carry across accounts
	}
	mu.Lock()
	// Only publish if nobody swapped ctrlSrv out from under us since we captured
	// srv; otherwise close the server we just started to avoid a leak.
	if ctrlSrv == srv {
		ctrlSrv, ctrlSrvUserID = s, c.Account().UserID
		mu.Unlock()
		odlog.Info("control api rebound on %s for new account", s.Addr())
	} else {
		mu.Unlock()
		s.Close()
	}
}

// serviceTicker pushes Discord presence once a second.
func serviceTicker() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		if p := curPlayer(); p != nil {
			publishDiscord(p)
		}
	}
}

func publishDiscord(p *audio.Player) {
	if dp == nil {
		return
	}
	// When routed to a remote device local playback is stopped, so the local
	// player would report "stopped" and Update() would clear the presence. Derive
	// it from the remote snapshot instead (matches the now-playing / lyrics sync
	// remotePoller already keeps) so RP reflects the remote track.
	if routedRemote() != nil {
		st := remoteSnapshot()
		ds := discord.State{PositionMS: st.PositionMS, DurationMS: st.DurationMS}
		if st.Track != nil {
			ds.Title, ds.Artist, ds.Album = st.Track.Title, st.Track.Artist, st.Track.Album
			if st.Track.DurationMS > 0 {
				ds.DurationMS = st.Track.DurationMS
			}
		}
		switch st.State {
		case "playing":
			ds.Status = "playing"
		case "paused":
			ds.Status = "paused"
		default:
			ds.Status = "stopped"
		}
		dp.Update(ds)
		return
	}
	cur := currentTrack()
	ds := discord.State{
		Title: cur.Name, Artist: cur.ArtistLine(), Album: cur.AlbumName,
		DurationMS: cur.DurationMS, PositionMS: p.PositionMS(),
	}
	switch p.State() {
	case audio.Playing:
		ds.Status = "playing"
	case audio.Paused:
		ds.Status = "paused"
	default:
		ds.Status = "stopped"
	}
	dp.Update(ds)
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
	case audio.Errored:
		st.State = "error"
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

// clientID / deviceLabel identify this GUI on the network. A GUI may override
// via DZSetClientInfo; otherwise they default to the platform.
var (
	clientID    = runtime.GOOS
	deviceLabel = "OpenDeezer (" + platformName(runtime.GOOS) + ")"
)

func platformName(goos string) string {
	switch goos {
	case "darwin":
		return "macOS"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	default:
		return goos
	}
}

// clientInfo returns the client id + device label (mu-guarded; safe off the
// responder goroutine).
func clientInfo() (string, string) {
	mu.Lock()
	defer mu.Unlock()
	return clientID, deviceLabel
}

// advertInfo is the identity broadcast over LAN discovery.
func advertInfo() discovery.Info {
	id, _ := clientInfo()
	return discovery.Info{Name: engineAccount().Name, Client: id, Version: coreVersion}
}

func engineAccount() control.Account {
	c := curClient()
	if c == nil {
		return control.Account{}
	}
	a := c.Account()
	return control.Account{UserID: a.UserID, Name: a.Name, Offer: a.Offer}
}

// engineCommands maps control commands to player-level actions. next/prev drive
// the engine playback queue (engineQ) when playing locally, or forward to the
// connected remote when a device is selected; shuffle/repeat are no-ops locally
// but forward to the connected remote (if any) so a controller can drive the
// remote's queue.
func engineCommands() control.Commands {
	return control.Commands{
		PlayPause:    func() { withPlayer(func(p *audio.Player) { p.TogglePause() }) },
		Next:         engineNext,
		Prev:         enginePrev,
		Stop:         func() { withPlayer(func(p *audio.Player) { p.Stop() }) },
		Restart:      func() { withPlayer(func(p *audio.Player) { p.SeekMS(0) }) },
		Seek:         func(ms int64) { withPlayer(func(p *audio.Player) { p.SeekMS(ms) }) },
		SetVolume:    func(v float64) { withPlayer(func(p *audio.Player) { p.SetVolume(v) }) },
		PlayTrack:    enginePlayTrack,
		PlayPlaylist: enginePlayPlaylist,
		SetRepeat: func(mode string) {
			if rc := routedRemote(); rc != nil {
				if st, err := rc.SetRepeat(mode); err == nil {
					setRemoteState(st)
				}
			}
		},
		SetShuffle: func(on bool) {
			if rc := routedRemote(); rc != nil {
				if st, err := rc.SetShuffle(on); err == nil {
					setRemoteState(st)
				}
			}
		},
		SetSleepTimer: func(minutes int, eot bool) {
			withPlayer(func(p *audio.Player) {
				p.SetSleepTimer(time.Duration(minutes)*time.Minute, eot)
			})
		},
		CancelSleepTimer: func() { withPlayer(func(p *audio.Player) { p.CancelSleepTimer() }) },
	}
}

// fetchEpisodeMeta enriches the current episode with title/podcast name/artwork
// by calling the REST /episode/{id} endpoint. Best-effort; matches DZPlay's
// fetchTrackMeta pattern. The episode's "artist line" is the podcast/show name.
func fetchEpisodeMeta(c *deezer.Client, id string) {
	if c == nil || id == "" {
		return
	}
	ep, err := c.EpisodeMeta(id)
	if err != nil || ep.ID == "" {
		return
	}
	// Only keep it if the user hasn't moved on to another episode meanwhile.
	if currentTrack().ID == id {
		setCurrentTrack(ep.AsTrack())
	}
}

// enginePlayResolved resolves a stream for t and starts it on the player,
// recording it as the current track. Blocking (network round-trip); callers must
// be off the realtime audio path.
func enginePlayResolved(c *deezer.Client, p *audio.Player, t deezer.Track) bool {
	plan, err := c.PrepareStream(t.ID)
	if err != nil {
		return false
	}
	if p.Play(plan, t.DurationMS) != nil {
		return false
	}
	setCurrentTrack(t)
	return true
}

// engineLoadAndPlay replaces the engine queue with ts (cursor at start) and
// plays the current track, so a natural finish auto-advances through the rest.
func engineLoadAndPlay(c *deezer.Client, p *audio.Player, ts []deezer.Track, start int) bool {
	queueMu.Lock()
	engineQ.Set(ts, start)
	t, ok := engineQ.Current()
	queueMu.Unlock()
	if !ok {
		return false
	}
	return enginePlayResolved(c, p, t)
}

func enginePlayTrack(id string) {
	c, p := curClient(), curPlayer()
	if c == nil || p == nil {
		return
	}
	t, err := c.Track(id)
	if err != nil {
		return
	}
	// A single-track "queue": nothing to auto-advance to, but loading it keeps the
	// engine queue in sync with what's actually playing, so a previously loaded
	// playlist can't auto-advance after this ad-hoc single track finishes.
	engineLoadAndPlay(c, p, []deezer.Track{t}, 0)
}

func enginePlayPlaylist(id string) {
	c, p := curClient(), curPlayer()
	if c == nil || p == nil {
		return
	}
	ts, err := c.PlaylistTracks(id)
	if err != nil || len(ts) == 0 {
		return
	}
	engineLoadAndPlay(c, p, ts, 0)
}

// engineQueueAdvance advances the engine queue when the track finishedID ends
// naturally and returns the next track to play. It advances only when finishedID
// is the queue's current track, so an ad-hoc DZPlay (which bypasses the engine
// queue) can't trigger a stale auto-advance into an unrelated playlist. Pure
// queue logic — the caller performs the network resolve + Play.
func engineQueueAdvance(finishedID string) (deezer.Track, bool) {
	queueMu.Lock()
	defer queueMu.Unlock()
	cur, ok := engineQ.Current()
	if !ok || cur.ID == "" || cur.ID != finishedID {
		return deezer.Track{}, false
	}
	if !engineQ.AdvanceAuto() {
		return deezer.Track{}, false
	}
	return engineQ.Current()
}

// engineAdvanceOnFinish is wired to the player's onFinish callback (DZInit) and
// runs on the player's manage() goroutine when a track ends naturally. It
// advances the engine queue and starts the next track on a FRESH goroutine, so
// the callback never blocks the manager on the network resolve + Play (which
// would stall prebuffer promotion + the sleep-timer fade).
//
// It returns true only when the engine queue actually drove this finish (the
// finished track was the engine queue's current track). The onFinish callback
// uses that to decide whether to also bump the GUI finished-counter, so the
// engine queue and a native GUI's own queue never both advance on one finish.
func engineAdvanceOnFinish() bool {
	next, ok := engineQueueAdvance(currentTrack().ID)
	if !ok {
		return false
	}
	go func() {
		c, p := curClient(), curPlayer()
		if c == nil || p == nil {
			return
		}
		enginePlayResolved(c, p, next)
	}()
	return true
}

// engineNext / enginePrev back the control API's /next + /prev. When a remote
// device is selected they forward to it (local playback is stopped); otherwise
// they move the engine queue and play the new current track locally.
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
