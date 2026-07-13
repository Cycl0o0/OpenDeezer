package main

// Engine-hosted services shared by every native GUI: Discord Rich Presence and
// the control API (remote control + MCP). They run inside the c-archive so the
// GUIs get them with no native code — the GUI just plays tracks via DZPlay and
// the engine tracks the current one for now-playing + remote status.
//
// Engine-side control covers the player-level actions (play/pause, stop, seek,
// volume, restart) plus play-by-id. next/prev/shuffle/repeat walk the engine
// queue (engineQ); a GUI can mirror its own queue into it via DZQueueSet /
// DZQueueSetIndex so remote controllers see — and drive — the real queue.

import (
	"encoding/json"
	"fmt"
	"net"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/bridge"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/config"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/control"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/discord"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/discovery"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/history"
	odlog "github.com/Cycl0o0/OpenDeezer/v2/internal/log"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/mediacache"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/queue"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/version"
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

	// advertiser is the LAN discovery responder for OpenDeezer Connect (nil when
	// the control server is loopback-only or disabled). It advertises the control
	// port so other devices can find us; it is retained here — not discarded — so
	// it can be Closed + re-pointed whenever the control server is rebuilt on a
	// new port or torn down, instead of leaving a ghost advertising a dead port.
	// Guarded by mu (mirrors the TUI's model.advertiser). (B10)
	advertiser *discovery.Responder

	// mediaCache is the on-disk raw-stream cache attached to the player in DZInit
	// (nil when disabled via media.json mediaCacheMB<=0). Held here — not only on
	// the player — so preparePlan can prefer a zero-network cache-sourced plan and
	// DZDownloadForOffline can pre-fetch ciphertext + persist meta into it.
	// Guarded by mu (set once under DZInit's lock, read via curMediaCache).
	mediaCache *mediacache.Cache

	curMu    sync.Mutex
	curTrack deezer.Track
	// curKind is the media kind of curTrack for history recording: "" = song
	// (set by setCurrentTrack, the common path) and history.KindEpisode =
	// podcast (set by setCurrentEpisode). Guarded by curMu alongside curTrack so
	// the recorder never pairs a track with a stale kind.
	curKind string

	// preloadedTrack remembers the identity (id + duration) of the stream last
	// armed on the player via DZPreload. The player's pending next source is set
	// ONLY by DZPreload, so on a gapless/crossfade promote this is exactly the
	// just-promoted track — it lets engineAdvanceOnFinish move now-playing
	// forward even when the GUI never synced its queue into engineQ. Cleared
	// whenever the player's preload is dropped (DZClearPreload, queue edits,
	// repeat/shuffle toggles, and a fresh Play — audio.Player.Play discards any
	// pending preload). Guarded by preloadMu.
	preloadMu      sync.Mutex
	preloadedTrack deezer.Track

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
	curKind = "" // a song; episodes go through setCurrentEpisode
	curMu.Unlock()
	// Every track change (play, auto-advance, gapless promote, async metadata
	// enrichment, remote sync) is a state change controllers care about.
	notifyControlState()
}

// setCurrentEpisode is setCurrentTrack for a podcast episode: it records the
// now-playing exactly like setCurrentTrack but tags the media kind so the
// history recorder stamps Kind="episode". Native history screens then replay the
// entry through DZPlayEpisode instead of the music-track resolver (DZPlay).
func setCurrentEpisode(t deezer.Track) {
	curMu.Lock()
	curTrack = t
	curKind = history.KindEpisode
	curMu.Unlock()
	notifyControlState()
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
// loop. Use it instead of withPlayer for pause/stop/seek/volume-style
// mutations (read-only accessors keep plain withPlayer).
func withPlayerNotify(fn func(*audio.Player)) {
	withPlayer(fn)
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

// curPlayer reads the live player global. (curClient is defined in deezercore.go.)
func curPlayer() *audio.Player {
	mu.Lock()
	defer mu.Unlock()
	return player
}

// setPreloadedTrack stashes the identity of the stream just armed on the
// player via DZPreload (see the preloadedTrack doc).
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
	withPlayer(func(p *audio.Player) { p.ClearPreload() })
	clearPreloadedTrack()
}

// ---- offline media cache (zero-network cached playback + explicit download) ----

// curMediaCache reads the attached on-disk raw-stream cache (nil when disabled).
func curMediaCache() *mediacache.Cache {
	mu.Lock()
	defer mu.Unlock()
	return mediaCache
}

// getCtrlSrv reads the shared control server under mu (nil when disabled), so
// callers off the UI thread — e.g. the player's onFinish callback — can publish
// a finished edge without racing the settings-toggle writers of ctrlSrv.
func getCtrlSrv() *control.Server {
	mu.Lock()
	defer mu.Unlock()
	return ctrlSrv
}

// pendingMeta stashes the StreamMeta of freshly-resolved encrypted full tracks
// (keyed by track id), so a following NATURAL finish — by which point the audio
// layer has teed the whole ciphertext into the media cache — can persist the
// meta and make the track fully cache-playable (zero network) next time.
// Cache-sourced plans (CDNURL=="") and previews/plain streams are never stashed.
var (
	pendingMetaMu sync.Mutex
	pendingMeta   = map[string]mediacache.StreamMeta{}
)

// storePendingMeta records the plan's StreamMeta for id when it is a freshly
// resolved, cacheable stream (encrypted, non-preview, real CDN URL). Called by
// preparePlan; consumed by persistFinishedMeta on a natural finish.
func storePendingMeta(id string, plan *deezer.StreamPlan) {
	if plan == nil || id == "" || plan.CDNURL == "" || !plan.Encrypted || plan.Preview {
		return
	}
	pendingMetaMu.Lock()
	pendingMeta[id] = mediacache.StreamMeta{
		Format:    plan.Format,
		Encrypted: plan.Encrypted,
		GainDB:    plan.GainDB,
		Preview:   plan.Preview,
	}
	pendingMetaMu.Unlock()
}

// persistFinishedMeta writes the stashed StreamMeta for id into the media cache
// after a natural finish, so PrepareStreamCached later serves the track with no
// network. No-op when the cache is off or no meta was stashed for id (cached,
// preview or plain streams). Consumes the pending entry.
func persistFinishedMeta(id string) {
	if id == "" {
		return
	}
	pendingMetaMu.Lock()
	m, ok := pendingMeta[id]
	if ok {
		delete(pendingMeta, id)
	}
	pendingMetaMu.Unlock()
	if !ok {
		return
	}
	if mc := curMediaCache(); mc != nil {
		_ = mc.PutMeta(id+"."+m.Format, m)
	}
}

// preparePlan resolves a playable stream plan for id, preferring the on-disk
// media cache when one is attached: a fully-cached track (its StreamMeta was
// persisted on a prior natural finish) yields a plan with CDNURL=="" that the
// audio layer serves with zero network. On a cache miss it falls back to the
// normal PrepareStream network path. Either way it stashes the freshly-resolved
// plan's meta (encrypted full tracks only) so a following natural finish makes
// the track cache-playable. Every local play path (DZPlay/DZPreload/
// enginePlayResolved) goes through here for consistent offline behavior.
func preparePlan(c *deezer.Client, id string) (*deezer.StreamPlan, error) {
	var (
		plan *deezer.StreamPlan
		err  error
	)
	if mc := curMediaCache(); mc != nil {
		plan, err = c.PrepareStreamCached(id, mc)
	} else {
		plan, err = c.PrepareStream(id)
	}
	if err != nil {
		return nil, err
	}
	storePendingMeta(id, plan)
	return plan, nil
}

// onNaturalFinish is the player's onFinish callback, factored out of DZInit so
// it is unit-testable. An errored finish (CDN/decode failure, ~0 played) is not
// a real end: refresh state and return. Otherwise: capture the finishing track
// id (before any advance moves currentTrack on), record the listen, persist its
// stream meta into the media cache (offline: PutMeta after the successful first
// stream), auto-advance the engine queue (or bump the GUI finished-counter so
// exactly one queue mechanism advances), publish the natural end-of-track edge
// to /events subscribers, and push a fresh state snapshot.
func onNaturalFinish() {
	if erroredFinish() {
		notifyControlState()
		return
	}
	tid := currentTrack().ID
	noteTrackFinished()
	persistFinishedMeta(tid)
	if !engineAdvanceOnFinish() {
		mu.Lock()
		finished++
		mu.Unlock()
	}
	if srv := getCtrlSrv(); srv != nil && tid != "" {
		srv.NotifyFinished(tid)
	}
	notifyControlState()
}

// tracksFromInsertJSON decodes DZQueueInsertNext's payload, which may be either
// a single wire track object {...} or a JSON array [...] of them. Reuses the
// shared queue decoders (queueTracksFromJSON/trackFromWire). Entries without an
// id are dropped; a bad payload yields an empty slice (a no-op insert).
func tracksFromInsertJSON(js string) []deezer.Track {
	js = strings.TrimSpace(js)
	if js == "" {
		return nil
	}
	if strings.HasPrefix(js, "[") {
		ts, _ := queueTracksFromJSON(js)
		return ts
	}
	var w bridge.Track
	if err := json.Unmarshal([]byte(js), &w); err != nil || w.ID == "" {
		return nil
	}
	return []deezer.Track{trackFromWire(w)}
}

// engineQueueInsertNext splices ts (in order) right after the current track,
// discarding a preloaded next (the upcoming track changed) and bumping the
// version + notifying so a GUI/controller resyncs. Local-only mutation of
// engineQ — the GUI Up-Next editor owns this queue.
func engineQueueInsertNext(ts []deezer.Track) {
	if len(ts) == 0 {
		return
	}
	queueMu.Lock()
	engineQ.InsertAfterCurrent(ts...)
	queueMu.Unlock()
	clearEnginePreload()
	notifyControlState()
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
				// Advertise on the LAN (OpenDeezer Connect) when bound to a
				// reachable (non-loopback) address, retaining the responder so a
				// later rebuild/disable can Close + re-point it. (B10)
				refreshAdvertiser()
			}
		}

		go serviceTicker()
	})
}

// refreshAdvertiser Closes any existing LAN discovery responder and, when the
// control server is currently bound to a reachable (non-loopback) address,
// starts a fresh one advertising its port. Call it whenever the control server
// is (re)built on a possibly-different port or torn down, so a ghost responder
// never keeps advertising a dead port. Must be called WITHOUT mu held. (B10)
func refreshAdvertiser() {
	mu.Lock()
	srv := ctrlSrv
	old := advertiser
	advertiser = nil
	mu.Unlock()
	if old != nil {
		old.Close()
	}
	if srv == nil {
		return
	}
	addr := srv.Addr()
	if config.IsLoopbackAddr(addr) {
		return // loopback-only: not LAN-discoverable, nothing to advertise
	}
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		return
	}
	resp, err := discovery.Advertise(advertInfo, p)
	if err != nil {
		return
	}
	mu.Lock()
	// A concurrent teardown may have cleared/replaced ctrlSrv while we were
	// binding; only publish if this server is still current, else Close the
	// responder we just started so it doesn't advertise a stale port.
	if ctrlSrv == srv {
		advertiser = resp
		mu.Unlock()
		odlog.Info("discovery advertising control port %d", p)
	} else {
		mu.Unlock()
		resp.Close()
	}
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
	st := control.State{State: "stopped"}
	// Queue + repeat + shuffle come from the engine queue, so /status reports the
	// real values once a GUI has synced them (DZQueueSet / DZSetRepeat /
	// DZSetShuffle). A GUI that never syncs keeps the old wire shape: no queue,
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
	case audio.Errored:
		st.State = "error"
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

// ---- engine queue sync (GUI queue -> engineQ) ----

// queueTracksFromJSON decodes a GUI queue payload: a JSON array of tracks in
// the same wire shape every list/browse call returns (bridge.Track). Only "id"
// is required; "durationMs" should be set so controllers' end-of-track
// detection works without a metadata refetch. Entries without an id are
// dropped. "" and "null" decode to an empty queue (clear).
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

// engineQueueSet replaces the engine-side queue with the GUI's tracks (cursor
// at 0; follow with engineQueueSetIndex to align it with the playing row).
func engineQueueSet(ts []deezer.Track) {
	queueMu.Lock()
	engineQ.Set(ts, 0)
	queueMu.Unlock()
	notifyControlState()
}

// engineQueueSetIndex aligns the engine queue cursor to the row the GUI is
// already playing (clamped). It uses AlignIndex, not SetIndex, so this pure
// synchronisation doesn't push a synthetic history entry that would make a
// later remote Prev jump to a never-played track. Genuine remote jumps go
// through engineQueueSelect (SetIndex), which does record history.
func engineQueueSetIndex(i int) {
	queueMu.Lock()
	engineQ.AlignIndex(i)
	queueMu.Unlock()
	notifyControlState()
}

// engineQueueIndex returns the engine queue cursor (-1 when empty).
func engineQueueIndex() int {
	queueMu.Lock()
	defer queueMu.Unlock()
	return engineQ.Index()
}

// engineQueueVersion returns the queue's content-mutation counter (bumped by
// set/add/remove/move, NOT by cursor moves), so a GUI can poll it cheaply and
// pull DZQueueJSON only when a remote controller actually edited the queue.
func engineQueueVersion() uint64 {
	queueMu.Lock()
	defer queueMu.Unlock()
	return engineQ.Version()
}

// engineQueueSnapshot returns the engine queue's contents + cursor + version
// under one lock (backs DZQueueJSON).
func engineQueueSnapshot() (ts []deezer.Track, index int, version uint64) {
	queueMu.Lock()
	defer queueMu.Unlock()
	return append([]deezer.Track(nil), engineQ.Tracks()...), engineQ.Index(), engineQ.Version()
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
// connected remote when a device is selected; shuffle/repeat are recorded on
// the engine queue (so /status reports them and the queue honors them) and also
// forward to the connected remote (if any) so a controller can drive the
// remote's queue.
func engineCommands() control.Commands {
	return control.Commands{
		PlayPause: func() { withPlayerNotify(func(p *audio.Player) { p.TogglePause() }) },
		Next:      engineNext,
		Prev:      enginePrev,
		// Record the in-progress listen before halting: the next Play sees the
		// player Stopped and skips recording, so this is the only chance.
		Stop:         func() { withPlayerNotify(func(p *audio.Player) { noteTrackTransition(p); p.Stop() }) },
		Restart:      func() { withPlayerNotify(func(p *audio.Player) { p.SeekMS(0) }) },
		Seek:         func(ms int64) { withPlayerNotify(func(p *audio.Player) { p.SeekMS(ms) }) },
		SetVolume:    func(v float64) { withPlayerNotify(func(p *audio.Player) { p.SetVolume(v) }) },
		PlayTrack:    enginePlayTrack,
		PlayPlaylist: enginePlayPlaylist,
		// CycleRepeat / SetRepeat: record the mode locally only. Repeat is NOT
		// forwarded to a routed remote — this instance owns the queue and streams
		// the host one track at a time, so a Repeat-All forwarded to the host would
		// trap it looping its single-item queue. The engine's own auto-advance
		// honors repeat when it drives the remote. (B2)
		CycleRepeat: func() {
			queueMu.Lock()
			next := ((engineQ.Repeat() + 1) % 3).String()
			queueMu.Unlock()
			engineSetRepeat(next)
		},
		SetRepeat: func(mode string) {
			engineSetRepeat(mode)
		},
		// CycleShuffle equivalents: shuffle IS forwarded (the host has no queue of
		// its own, but a controller's shuffle intent is harmless there and keeps
		// /status consistent).
		ToggleShuffle: func() {
			queueMu.Lock()
			on := !engineQ.Shuffle()
			queueMu.Unlock()
			engineSetShuffle(on)
			if rc := routedRemote(); rc != nil {
				if st, err := rc.SetShuffle(on); err == nil {
					setRemoteState(rc, st)
				}
			}
		},
		SetShuffle: func(on bool) {
			engineSetShuffle(on)
			if rc := routedRemote(); rc != nil {
				if st, err := rc.SetShuffle(on); err == nil {
					setRemoteState(rc, st)
				}
			}
		},
		QueueAdd:  engineQueueAdd,
		QueueJump: engineQueueJump,
		// SetQueue lets a controller replace the engine queue wholesale (cursor at
		// index). Mirrors DZQueueSet + DZQueueSetIndex in one hop: parse, Set,
		// clear a now-stale preload, and notify. Nil-safe (parse error surfaced).
		SetQueue: func(tracksJSON string, index int) error {
			ts, err := queueTracksFromJSON(tracksJSON)
			if err != nil {
				return err
			}
			queueMu.Lock()
			engineQ.Set(ts, index)
			queueMu.Unlock()
			clearEnginePreload()
			notifyControlState()
			return nil
		},
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
		setCurrentEpisode(ep.AsTrack())
	}
}

// nextPlaySeq claims a fresh play-sequence token (see playSeq). Every launch of
// an async auto-advance resolve claims one; a subsequent user play bumps past
// it so the resolve drops its stale result. (B3)
func nextPlaySeq() uint64 { return playSeq.Add(1) }

// playSuperseded reports whether a newer play has been issued since seq was
// claimed, i.e. an in-flight auto-advance resolve should drop its result. (B3)
func playSuperseded(seq uint64) bool { return playSeq.Load() != seq }

// enginePlayResolved resolves a stream for t and starts it on the player,
// recording it as the current track. Blocking (network round-trip); callers must
// be off the realtime audio path.
func enginePlayResolved(c *deezer.Client, p *audio.Player, t deezer.Track) bool {
	plan, err := preparePlan(c, t.ID)
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

// enginePlayResolvedGuarded is enginePlayResolved for the ASYNC auto-advance
// path: it resolves the stream (a network round-trip) and then, just before
// starting playback, drops the result if a newer play (a user DZPlay /
// DZPlayEpisode, or a later auto-advance) bumped playSeq past the value captured
// at launch. Without the guard a slow auto-advance resolve could clobber the
// track the user started while it was resolving. (B3)
func enginePlayResolvedGuarded(c *deezer.Client, p *audio.Player, t deezer.Track, seq uint64) bool {
	plan, err := preparePlan(c, t.ID)
	if err != nil {
		return false
	}
	if playSuperseded(seq) {
		return false // superseded by a newer play while we were resolving
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
// plays the current track, so a natural finish auto-advances through the rest.
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
	// Gapless/crossfade promote: the player swapped the preloaded source in and
	// kept playing (a real finish passes through Stopped before onFinish fires).
	if p := curPlayer(); p != nil && p.State() == audio.Playing {
		return engineSyncOnGaplessPromote()
	}
	next, ok := engineQueueAdvance(currentTrack().ID)
	if !ok {
		return false
	}
	// Claim a seq for this auto-advance launch; a user DZPlay that races the
	// async resolve below bumps past it, so the resolver drops the stale result
	// instead of clobbering the user's track. (B3)
	seq := nextPlaySeq()
	go func() {
		c, p := curClient(), curPlayer()
		if c == nil || p == nil {
			return
		}
		enginePlayResolvedGuarded(c, p, next, seq)
	}()
	return true
}

// engineSyncOnGaplessPromote does the queue + now-playing bookkeeping after
// the player gaplessly promoted its preloaded source (it is still Playing
// then). Split from engineAdvanceOnFinish so tests can drive a promote without
// a live audio device. Returns whether the engine queue owned the finished
// track (the caller uses that to keep exactly one queue mechanism advancing).
//
// The preloaded track is always the deterministic linear next, so when the
// engine queue owned the finished track just walk the cursor + now-playing
// along with the audio — re-resolving + Play here would kill the promoted
// stream and restart the track over the network.
func engineSyncOnGaplessPromote() bool {
	queueMu.Lock()
	cur, ok := engineQ.Current()
	owned := ok && cur.ID != "" && cur.ID == currentTrack().ID && engineQ.AdvanceLinear()
	var next deezer.Track
	if owned {
		next, _ = engineQ.Current()
	}
	queueMu.Unlock()
	// Either way the player just consumed its armed preload: take the stashed
	// identity so a later finish can't reuse it.
	promoted, armed := takePreloadedTrack()
	if !owned {
		// Engine queue empty or misaligned: the GUI preloaded without mirroring
		// its queue via DZQueueSet (the shipped apps do exactly this). The
		// promoted stream can only be the armed preload — p.next is set solely
		// by DZPreload, which stashes it — so move now-playing onto it.
		// Otherwise /status would stay on the finished track, whose NEXT finish
		// would re-record it in history while the promoted track never gets
		// recorded. The outgoing listen itself was already recorded by
		// noteTrackFinished (it runs before this in the onFinish callback).
		if armed {
			setCurrentTrack(promoted)
			go fetchTrackMeta(curClient(), promoted.ID) // enrich id+duration, like DZPlay
		}
		return false // still not the engine queue's finish; the GUI's own queue advances
	}
	setCurrentTrack(next)
	return true
}

// engineNext / enginePrev back the control API's /next + /prev. When a remote
// device is selected they forward to it (local playback is stopped); otherwise
// they move the engine queue and play the new current track locally.
func engineNext() {
	if rc := routedRemote(); rc != nil {
		if st, err := rc.Next(); err == nil {
			setRemoteState(rc, st)
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
			setRemoteState(rc, st)
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

// ---- extended control commands: queue edits, album/mix play, history ----
//
// These back the control API's /queue/*, /play/album, /play/mix/* and
// /history/recent endpoints. Like Next/Prev they forward to the routed Connect
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
			setRemoteState(rc, st)
		}
		return err
	}
	c := curClient()
	if c == nil {
		return errNotReady
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
// that row through the normal engine play path.
func engineQueueJump(index int) error {
	if rc := routedRemote(); rc != nil {
		st, err := rc.QueueJump(index)
		if err == nil {
			setRemoteState(rc, st)
		}
		return err
	}
	c, p := curClient(), curPlayer()
	if c == nil || p == nil {
		return errNotReady
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
			setRemoteState(rc, st)
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
			setRemoteState(rc, st)
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
		return errNotReady
	}
	if !engineLoadAndPlay(c, p, ts, 0) {
		return fmt.Errorf("could not start %s playback", what)
	}
	return nil
}

// enginePlayAlbum backs POST /play/album: replace the engine queue with the
// album's tracks and play the first (natural finishes auto-advance).
func enginePlayAlbum(id string) error {
	if rc := routedRemote(); rc != nil {
		st, err := rc.PlayAlbum(id)
		if err == nil {
			setRemoteState(rc, st)
		}
		return err
	}
	c := curClient()
	if c == nil {
		return errNotReady
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
			setRemoteState(rc, st)
		}
		return err
	}
	c := curClient()
	if c == nil {
		return errNotReady
	}
	ts, err := c.TrackMix(id)
	return enginePlayFetched("mix", ts, err)
}

// enginePlayMixArtist backs POST /play/mix/artist ("artist radio").
func enginePlayMixArtist(id string) error {
	if rc := routedRemote(); rc != nil {
		st, err := rc.PlayMixArtist(id)
		if err == nil {
			setRemoteState(rc, st)
		}
		return err
	}
	c := curClient()
	if c == nil {
		return errNotReady
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

// jTrackStat / jArtistStat are the stable wire shapes for the history-stats
// exports (history.TrackStat/ArtistStat carry no JSON tags, so we don't marshal
// them directly).
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

// historyStats computes the listening-stats summary backing DZHistoryStatsJSON:
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
// tagged with its media kind so replay can route (song -> DZPlay, episode ->
// DZPlayEpisode). The file write (append + fsync) runs on its own goroutine so
// callers on the player's manage/callback path never block on disk I/O.
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

// erroredFinishMaxMS bounds how much audio can have played for a finish to be
// treated as an error (CDN/decode failure) rather than a listen. A track that
// dies near its start played ~0; a failure well into the track is a real
// (partial) listen and is recorded normally.
const erroredFinishMaxMS = 1000

// isErroredFinish is the pure predicate behind erroredFinish (testable without
// a live audio device): the just-finished track carried an error (the player is
// Errored, or LastError is non-empty because internal/audio stored the source's
// download/decode error) AND almost no audio played. (B4)
//
// A still-Playing state is never an errored finish: that is a gapless/crossfade
// promote (the next track is already playing successfully). manage() copies the
// OUTGOING track's error into LastError even on a promote, so without this guard
// a promote following an early-errored track would be misclassified and its
// now-playing/queue bookkeeping skipped.
func isErroredFinish(state audio.State, lastErr string, positionMS int64) bool {
	if state == audio.Playing {
		return false
	}
	errored := state == audio.Errored || lastErr != ""
	return errored && positionMS < erroredFinishMaxMS
}

// erroredFinish reports whether the finish now firing is a CDN/decode failure
// rather than a natural end, reading the live player. False when no player. (B4)
func erroredFinish() bool {
	p := curPlayer()
	if p == nil {
		return false
	}
	return isErroredFinish(p.State(), p.LastError(), p.PositionMS())
}

// noteTrackFinished records the track that just ended naturally. It runs from
// the player's onFinish callback (the manage goroutine): for a real finish the
// player retains the end position; for a gapless/crossfade promote the player
// is already Playing the NEXT track, so the outgoing one played its full
// duration.
func noteTrackFinished() {
	var state audio.State
	var lastErr string
	var posMS int64
	havePlayer := false
	if p := curPlayer(); p != nil {
		state, lastErr, posMS, havePlayer = p.State(), p.LastError(), p.PositionMS(), true
	}
	prev, kind := currentTrackSnapshot()
	recordFinished(prev, kind, state, lastErr, posMS, havePlayer)
}

// recordFinished is noteTrackFinished's pure core (testable without a live audio
// device). It drops an errored finish (a CDN/decode failure with ~0 played is
// not a real listen, B4) and otherwise records the outgoing listen using the
// track duration — or the player's actual end position when it retained one (a
// real finish, not a gapless promote where the player is already Playing next).
func recordFinished(prev deezer.Track, kind string, state audio.State, lastErr string, positionMS int64, havePlayer bool) {
	if havePlayer && isErroredFinish(state, lastErr, positionMS) {
		return
	}
	playedMS := prev.DurationMS
	if havePlayer && state != audio.Playing && positionMS > 0 {
		playedMS = positionMS
	}
	recordListen(prev, kind, playedMS)
}
