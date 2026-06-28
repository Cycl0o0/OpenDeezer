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
	"sync"
	"time"

	"github.com/Cycl0o0/OpenDeezer/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/internal/config"
	"github.com/Cycl0o0/OpenDeezer/internal/control"
	"github.com/Cycl0o0/OpenDeezer/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/internal/discord"
	odlog "github.com/Cycl0o0/OpenDeezer/internal/log"
)

var (
	servicesOnce sync.Once
	dp           discord.Presence
	ctrlSrv      *control.Server
	coreVersion  = "1.0.0"

	curMu    sync.Mutex
	curTrack deezer.Track
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
// login. Safe to call from DZInit (which holds mu): it only sets up + spawns
// goroutines and never locks mu synchronously (the just-logged-in client is
// passed in; the closures read the globals lazily, off the HTTP goroutine).
func startServices(c *deezer.Client) {
	servicesOnce.Do(func() {
		dp = discord.New(config.LoadDiscordAppID())

		if cfg := config.LoadControl(); cfg.Enabled {
			ctrlSrv = control.New(
				control.Config{Addr: cfg.Addr, Token: cfg.Token, SameAccountOnly: cfg.SameAccount},
				engineState,
				engineAccount,
				engineCommands(),
				c,
			)
			if err := ctrlSrv.Start(); err != nil {
				odlog.Warn("control api: %v", err)
				ctrlSrv = nil
			} else {
				ctrlSrv.SetVersion(coreVersion)
				odlog.Info("control api on %s", ctrlSrv.Addr())
			}
		}

		go serviceTicker()
	})
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
		st.Track = &control.Track{
			ID: cur.ID, Title: cur.Name, Artist: cur.ArtistLine(),
			Album: cur.AlbumName, Explicit: cur.Explicit, DurationMS: cur.DurationMS,
		}
	}
	return st
}

func engineAccount() control.Account {
	c := curClient()
	if c == nil {
		return control.Account{}
	}
	a := c.Account()
	return control.Account{UserID: a.UserID, Name: a.Name, Offer: a.Offer}
}

// engineCommands maps control commands to player-level actions. next/prev,
// shuffle and repeat depend on the GUI's queue and are intentionally omitted.
func engineCommands() control.Commands {
	return control.Commands{
		PlayPause:    func() { withPlayer(func(p *audio.Player) { p.TogglePause() }) },
		Stop:         func() { withPlayer(func(p *audio.Player) { p.Stop() }) },
		Restart:      func() { withPlayer(func(p *audio.Player) { p.SeekMS(0) }) },
		Seek:         func(ms int64) { withPlayer(func(p *audio.Player) { p.SeekMS(ms) }) },
		SetVolume:    func(v float64) { withPlayer(func(p *audio.Player) { p.SetVolume(v) }) },
		PlayTrack:    enginePlayTrack,
		PlayPlaylist: enginePlayPlaylist,
	}
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
	plan, err := c.PrepareStream(id)
	if err != nil {
		return
	}
	if p.Play(plan, t.DurationMS) == nil {
		setCurrentTrack(t)
	}
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
	t := ts[0]
	plan, err := c.PrepareStream(t.ID)
	if err != nil {
		return
	}
	if p.Play(plan, t.DurationMS) == nil {
		setCurrentTrack(t)
	}
}
