package ui

import (
	"errors"
	"strings"
	"time"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/config"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/i18n"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// menuRows is the home screen. A "Resume" row is prepended when a saved
// playback position exists.
func (m *Model) menuRows() []list.Item {
	var rows []list.Item
	if r := LoadResume(); r != nil {
		rows = append(rows, row{
			kind: rowMenu, action: actResume,
			title: "▶  " + i18n.T("Resume") + " — " + r.Name,
			desc:  r.ArtistLine + " · " + fmtMS(r.PositionMS) + " / " + fmtMS(r.DurationMS),
		})
	}
	rows = append(rows,
		row{kind: rowMenu, title: "❤  " + i18n.T("Liked Songs"), desc: i18n.T("favorites"), action: actLiked},
		row{kind: rowMenu, title: "≡  " + i18n.T("My Playlists"), desc: i18n.T("your playlists"), action: actPlaylists},
		row{kind: rowMenu, title: "⚡ Flow", desc: i18n.T("non-stop mix"), action: actFlow},
		row{kind: rowMenu, title: "📈 " + i18n.T("Charts"), desc: i18n.T("top tracks, albums & artists"), action: actCharts},
		row{kind: rowMenu, title: "🎙 " + i18n.T("Podcasts"), desc: i18n.T("search shows & episodes"), action: actPodcasts},
		row{kind: rowMenu, title: "🔍 " + i18n.T("Search"), desc: i18n.T("tracks, albums, artists, playlists"), action: actSearch},
		row{kind: rowMenu, title: "📊 " + i18n.T("Stats"), desc: i18n.T("recently played & top tracks"), action: actStats},
		row{kind: rowMenu, title: "📡 " + i18n.T("Remote control"), desc: i18n.T("drive another OpenDeezer client"), action: actRemote},
		row{kind: rowMenu, title: "📱 " + i18n.T("Web Remote"), desc: i18n.T("control from your phone over Wi-Fi"), action: actWebRemote},
		row{kind: rowMenu, title: "🌐 " + i18n.T("Language") + ": " + currentLanguageName(), desc: i18n.T("interface language"), action: actLanguage},
	)
	return rows
}

// Update handles all messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// View sizes the list against the footer it actually renders. Give it a
		// usable interim size here so resize messages never leave a zero viewport.
		m.list.SetSize(max(1, msg.Width), max(1, msg.Height))
		m.ready = true
		m.refreshCover()
		return m, nil

	case loginDoneMsg:
		m.loading = false
		if msg.err != nil {
			switch {
			case errors.Is(msg.err, deezer.ErrNoNetwork):
				// Connectivity loss, not an auth problem — show the No-Internet
				// screen with a retry rather than telling the user to re-login.
				m.screen = screenNoInternet
				m.status = ""
			case errors.Is(msg.err, deezer.ErrARLExpired):
				m.status = i18n.T("ARL expired or invalid — refresh the 'arl' cookie from deezer.com, then `opendeezer -save-arl <arl>`")
			default:
				m.status = i18n.Tf("Login failed: %s", msg.err.Error())
			}
			return m, nil
		}
		m.acct = m.client.Account()
		m.publishAccount()                                // identity snapshot for the control API (race-free read)
		m.client.SetAdsDisabled(config.LoadAdsDisabled()) // free-tier ads opt-out
		m.screen = screenMenu
		if m.acct.Name != "" {
			m.status = i18n.Tf("Logged in as %s · %s", m.acct.Name, m.acct.Offer)
		} else {
			m.status = i18n.Tf("Logged in · %s", m.acct.Offer)
		}
		switch {
		case !m.acct.Premium:
			// Free account: streams full tracks at 128 kbps (ad-supported, like
			// Deezer's web player); 320/FLAC and downloads need a paid plan. A track
			// with no full-length source falls back to a 30-second preview.
			m.status += "  " + i18n.T("(free account · 128 kbps; downloads need a paid plan)")
		case (m.client.Quality() == 2 && !m.acct.CanHiFi) || (m.client.Quality() == 1 && !m.acct.CanHQ):
			// Warn if the chosen quality exceeds the plan's entitlement.
			m.status += "  " + i18n.T("(plan can't stream that quality — will fall back)")
		}
		m.list.Title = "OpenDeezer"
		m.list.SetItems(m.menuRows())
		// Seed the liked-ids cache in the background so the 'f' toggle knows the
		// current state before the Liked Songs view is ever opened.
		return m, m.likedIDsCmd()

	case likedIDsMsg:
		m.likedIDs = msg.ids
		return m, nil

	case tracksMsg:
		m.loading = false
		items := make([]list.Item, len(msg.tracks))
		for i, t := range msg.tracks {
			items[i] = trackRow(t)
		}
		if msg.favorites {
			// The Liked Songs list is the authoritative liked set — refresh the cache.
			m.likedIDs = likedSet(msg.tracks)
		}
		// Browsing must not touch the live play queue — hold the tracks aside and
		// only commit to the queue on an explicit play (see activate).
		m.browse = msg.tracks
		m.ownPlaylists = false
		m.pageArtist = deezer.ArtistInfo{}
		m.list.Title = msg.title
		m.list.SetItems(items)
		m.list.ResetSelected()
		m.screen = screenList
		m.status = ""
		return m, nil

	case artistPageMsg:
		m.loading = false
		p := msg.page
		var items []list.Item
		if len(p.Top) > 0 {
			items = append(items, sectionRow(i18n.T("Top tracks")))
			for _, t := range p.Top {
				items = append(items, trackRow(t))
			}
		}
		if len(p.Albums) > 0 {
			items = append(items, sectionRow(i18n.T("Albums")))
			for _, a := range p.Albums {
				items = append(items, albumRow(a))
			}
		}
		if len(p.Related) > 0 {
			items = append(items, sectionRow(i18n.T("Related artists")))
			for _, a := range p.Related {
				items = append(items, artistRow(a))
			}
		}
		// Only the top tracks are the playable context (playBrowsed maps rows to
		// the browse slice by id, so header/album/artist rows never shift plays).
		m.browse = p.Top
		m.ownPlaylists = false
		m.pageArtist = p.Artist // 'm' with no seed row starts this artist's radio
		m.list.Title = "♪ " + p.Artist.Name
		m.list.SetItems(items)
		m.list.ResetSelected()
		if len(items) > 1 {
			m.list.Select(1) // start on the first entry, not the section header
		}
		m.screen = screenList
		m.status = ""
		return m, nil

	case podcastsMsg:
		m.loading = false
		items := make([]list.Item, len(msg.podcasts))
		for i, p := range msg.podcasts {
			items[i] = podcastRow(p)
		}
		m.ownPlaylists = false
		m.pageArtist = deezer.ArtistInfo{}
		m.list.Title = msg.title
		m.list.SetItems(items)
		m.list.ResetSelected()
		m.screen = screenList
		m.status = ""
		return m, nil

	case episodesMsg:
		m.loading = false
		items := make([]list.Item, len(msg.episodes))
		browse := make([]deezer.Track, len(msg.episodes))
		for i, e := range msg.episodes {
			items[i] = episodeRow(e)
			browse[i] = e.AsTrack()
		}
		// Held aside until the user plays a row — see tracksMsg.
		m.browse = browse
		m.ownPlaylists = false
		m.pageArtist = deezer.ArtistInfo{}
		m.list.Title = msg.title
		m.list.SetItems(items)
		m.list.ResetSelected()
		m.screen = screenList
		m.status = ""
		return m, nil

	case statusMsg:
		m.loading = false
		m.status = msg.text
		return m, nil

	case downloadDoneMsg:
		m.downloading = false
		switch {
		case msg.err == nil:
			m.status = i18n.Tf("Saved to %s", msg.path)
		case deezer.IsPremiumRequired(msg.err):
			m.status = i18n.T("Downloads need a paid Deezer plan")
		default:
			m.status = i18n.Tf("Download failed: %s", msg.err.Error())
		}
		return m, nil

	case playlistsMsg:
		m.loading = false
		items := make([]list.Item, len(msg.playlists))
		for i, p := range msg.playlists {
			items[i] = playlistRow(p)
		}
		// This list is the user's own playlists (the only producer is
		// playlistsCmd), so the N/R/X playlist write ops apply here.
		m.ownPlaylists = true
		m.pageArtist = deezer.ArtistInfo{}
		m.list.Title = msg.title
		m.list.SetItems(items)
		m.list.ResetSelected()
		m.screen = screenList
		m.status = ""
		return m, nil

	case favToggleMsg:
		m.loading = false
		if msg.err != nil {
			// Roll back the optimistic liked-set flip.
			if m.likedIDs != nil {
				if msg.liked {
					delete(m.likedIDs, msg.id)
				} else {
					m.likedIDs[msg.id] = true
				}
			}
			m.status = i18n.Tf("Error: %s", msg.err.Error())
			return m, nil
		}
		if msg.liked {
			m.status = "❤ " + i18n.Tf("Liked: %s", msg.name)
		} else {
			m.status = i18n.Tf("Unliked: %s", msg.name)
		}
		return m, nil

	case playlistPickMsg:
		// Snapshot the list the picker replaces (it borrows m.list), exactly like
		// the device picker — see devicesMsg for the prevScreen rationale.
		if m.screen != screenPlaylistPick {
			m.devSavedItems = m.list.Items()
			m.devSavedTitle = m.list.Title
			m.devSavedIndex = m.list.Index()
			switch m.screen {
			case screenNowPlaying, screenCredits, screenQueue, screenLyrics, screenHelp:
			default:
				m.prevScreen = m.screen
			}
		}
		m.pickTrack = msg.track
		items := []list.Item{row{
			kind: rowMenu, action: actNewPlaylist,
			title: "✚  " + i18n.T("New playlist…"), desc: i18n.T("create a playlist with this track"),
		}}
		for _, p := range msg.playlists {
			items = append(items, playlistRow(p))
		}
		m.list.Title = "➕ " + i18n.T("Add to playlist")
		m.list.SetItems(items)
		m.list.ResetSelected()
		m.screen = screenPlaylistPick
		m.loading = false
		m.status = ""
		return m, nil

	case playlistOpMsg:
		m.loading = false
		if msg.err != nil {
			m.status = i18n.Tf("Error: %s", msg.err.Error())
			return m, nil
		}
		m.status = msg.status
		// Reload the playlists list so the op is visible immediately, but only
		// when it is what's on screen (create-from-picker returns to a browse list).
		if msg.refresh && m.screen == screenList && m.ownPlaylists {
			m.loading = true
			return m, m.playlistsCmd()
		}
		return m, nil

	case searchMsg:
		m.loading = false
		var items []list.Item
		for _, t := range msg.results.Tracks {
			items = append(items, trackRow(t))
		}
		for _, a := range msg.results.Artists {
			items = append(items, artistRow(a))
		}
		for _, a := range msg.results.Albums {
			items = append(items, albumRow(a))
		}
		for _, p := range msg.results.Playlists {
			items = append(items, playlistRow(p))
		}
		// Tracks are the playable context, held aside until an explicit play.
		m.browse = msg.results.Tracks
		m.ownPlaylists = false
		m.pageArtist = deezer.ArtistInfo{}
		m.list.Title = i18n.T("Results")
		m.list.SetItems(items)
		m.list.ResetSelected()
		m.screen = screenList
		m.status = ""
		return m, nil

	case streamReadyMsg:
		m.loading = false
		// Drop a stale resolve: if the user has since selected a different track,
		// the queue's current no longer matches — don't override the newer choice.
		if t, ok := m.q.Current(); !ok || t.ID != msg.track.ID {
			return m, nil
		}
		if err := m.player.Play(msg.plan, msg.track.DurationMS); err != nil {
			m.status = i18n.Tf("Playback error: %s", err.Error())
			return m, nil
		}
		m.playing = true
		m.status = ""
		// Resume: seek to the saved position once the stream is live.
		if m.pendingSeek > 0 {
			m.player.SeekMS(m.pendingSeek)
			m.pendingSeek = 0
		}
		cmd := m.onTrackChanged(msg.track)
		// Preload the next track for a gapless/crossfaded transition, and report the
		// play to Deezer (log.listen) unless the user disabled ads/reporting.
		return m, tea.Batch(cmd, m.preloadNextCmd(), m.nowPlayingCmd(msg.track.ID))

	case preloadMsg:
		if msg.plan == nil {
			return m, nil
		}
		// Drop a stale preload: if repeat/shuffle changed the upcoming track (or made
		// it non-deterministic) while this resolve was in flight, don't install it —
		// otherwise it would defeat the ClearPreload() done on those toggles.
		if t, ok := m.q.PeekNext(); !ok || t.ID != msg.trackID {
			return m, nil
		}
		m.player.Preload(msg.plan, msg.dur)
		return m, nil

	case devicesMsg:
		// Snapshot the list the picker is about to replace (it reuses m.list) and
		// remember the return screen — but only when not already on the picker, so
		// re-opening 'd' from itself can't overwrite either with device rows. Skip
		// recording an overlay as prevScreen (mirrors toggleScreen) to avoid an
		// esc self-loop when the picker is opened from another overlay.
		if m.screen != screenDevices {
			m.devSavedItems = m.list.Items()
			m.devSavedTitle = m.list.Title
			m.devSavedIndex = m.list.Index()
			switch m.screen {
			case screenNowPlaying, screenCredits, screenQueue, screenLyrics, screenHelp:
			default:
				m.prevScreen = m.screen
			}
		}
		items := make([]list.Item, len(msg.devices))
		cur := m.player.CurrentDevice()
		for i, d := range msg.devices {
			items[i] = deviceRow(d.ID, d.Name, d.ID == cur)
		}
		m.list.Title = i18n.T("Output device")
		m.list.SetItems(items)
		m.list.ResetSelected()
		m.screen = screenDevices
		m.loading = false
		m.status = ""
		return m, nil

	case lyricsMsg:
		m.loading = false
		if msg.err != nil {
			m.status = i18n.Tf("Lyrics: %s", msg.err.Error())
			return m, nil
		}
		if msg.trackID == m.lyricsTrack {
			m.lyrics = msg.lyrics
		}
		return m, nil

	case artMsg:
		if msg.img != nil && msg.trackID == m.curImgTrack {
			m.curImg = msg.img
			m.refreshCover()
		}
		return m, nil

	case trackFinishedMsg:
		// Branch on msg.swapped (captured at the finish signal), not the live player
		// state, which a pause/stop/selection racing the signal could have changed.
		switch {
		case msg.swapped && m.player.State() != audio.Stopped:
			// The player gaplessly swapped in the preloaded next, which is always
			// the deterministic linear next (PeekNext). Advance the cursor to that
			// same track — NOT via Next(), which would re-evaluate shuffle/repeat at
			// finish time and could jump to a different track than the audio. No
			// re-Play(), so a user pause survives the transition.
			m.histMarkFull() // the outgoing track played to its natural end
			m.q.AdvanceLinear()
			m.playing = true
			if t, ok := m.q.Current(); ok {
				return m, tea.Batch(m.onTrackChanged(t), m.preloadNextCmd(), m.waitFinish())
			}
			return m, tea.Batch(m.historyFlushCmd(), m.waitFinish())
		case m.player.State() == audio.Stopped:
			// Track ended with no preload and nothing new started: advance + play.
			m.histMarkFull() // natural end; advance() flushes if playback stops here
			return m, tea.Batch(m.advance(), m.waitFinish())
		default:
			// A user selection is already loading/playing — don't advance over it.
			return m, m.waitFinish()
		}

	case errMsg:
		m.loading = false
		// A browse/stream call failed because the network dropped: take over with
		// the No-Internet screen (remembering where to return) instead of leaving a
		// cryptic error in the footer. The session stays valid, so retry can just
		// go back once connectivity returns.
		if errors.Is(msg.err, deezer.ErrNoNetwork) && m.screen != screenNoInternet {
			m.prevScreen = m.screen
			m.screen = screenNoInternet
			m.status = ""
			return m, nil
		}
		m.status = i18n.Tf("Error: %s", msg.err.Error())
		return m, nil

	case updateCheckMsg:
		// Capture whether this reply answers a manual re-check before clearing the
		// flag: the status line is now localized, so it can't be compared against a
		// fixed English "Checking…" literal to tell manual from silent checks.
		wasManual := m.updateChecking
		m.updateChecking = false
		m.updateChecked = true
		if msg.err == nil {
			m.updateInfo = msg.info
		}
		// The silent startup check never touches m.status — the footer notice (see
		// footer()) is enough when an update exists. Only a manual re-check (About
		// screen) surfaces feedback here.
		if wasManual {
			switch {
			case msg.err != nil:
				m.status = i18n.T("Update check failed (network?)")
			case msg.info.HasUpdate:
				m.status = ""
			default:
				m.status = i18n.T("You're on the latest version.")
			}
		}
		return m, nil

	case tickMsg:
		var histFlush tea.Cmd
		// The end-of-track sleep timer stops the player without firing onFinish (so
		// the queue can't auto-advance), leaving m.playing stale. Reconcile it so the
		// footer stops showing "playing" and quit won't persist a bogus end-of-track
		// resume point.
		if m.playing && m.player.State() == audio.Stopped {
			m.playing = false
			// The player stopped without a track change (end-of-track sleep timer):
			// close the listening session at the last observed position.
			histFlush = m.historyFlushCmd()
		}
		m.histSyncPos() // keep the session's listened-position sample fresh
		m.publishMedia()
		m.publishControl()
		// Poll the peer on the remote-control screen and while viewing its lyrics
		// (so synced lyrics scroll with the peer's position).
		if m.remote != nil && (m.screen == screenRemoteCtl || m.screen == screenLyrics) {
			return m, tea.Batch(tickCmd(), remotePollCmd(m.remote), histFlush)
		}
		return m, tea.Batch(tickCmd(), histFlush)

	case remoteConnMsg:
		m.loading = false
		if msg.err != nil {
			m.status = i18n.Tf("Remote: %s", msg.err.Error())
			return m, nil
		}
		m.remote = msg.client
		m.remoteAddr = msg.addr
		m.remoteName = msg.name
		m.remoteClient = msg.clientType
		m.remoteVersion = msg.version
		m.remoteState = msg.state
		m.screen = screenRemoteCtl
		name := msg.name
		if name == "" {
			name = msg.addr
		}
		m.status = i18n.Tf("Connected to %s", name)
		_ = SaveLastPeer(msg.addr)
		return m, nil

	case remoteStateMsg:
		if msg.err != nil {
			m.status = i18n.Tf("Remote: %s", msg.err.Error())
			return m, nil
		}
		m.remoteState = msg.state
		return m, nil

	case webRemoteMsg:
		m.loading = false
		if msg.errStr != "" {
			m.status = i18n.Tf("Web Remote: %s", msg.errStr)
			return m, nil
		}
		// When the cmd had to close the old loopback server and rebind on LAN,
		// replace m.ctrl with the new server so publishControl etc. reach it.
		if msg.replacedCtrl && msg.srv != nil {
			m.ctrl = msg.srv
		}
		if msg.srv != nil {
			m.webRemoteSrv = msg.srv
		}
		m.webRemoteActive = msg.enabled
		m.webRemoteCode = msg.code
		m.webRemoteURL = msg.url
		m.webRemoteQR = msg.qr
		if msg.enabled {
			m.status = ""
		} else {
			m.status = i18n.T("Web remote disabled")
		}
		return m, nil

	case devicesDiscoveredMsg:
		m.loading = false
		items := []list.Item{row{
			kind: rowMenu, action: actRemoteManual,
			title: "✎  " + i18n.T("Enter address…"), desc: i18n.T("type a host:port manually"),
		}}
		for _, p := range msg.peers {
			items = append(items, peerRow(p))
		}
		m.list.Title = i18n.T("Connect to a device")
		m.list.SetItems(items)
		m.list.ResetSelected()
		m.screen = screenRemote
		if len(msg.peers) == 0 {
			m.status = i18n.T("No devices found — enable OPENDEEZER_CONTROL=:7654 on the target.")
		} else {
			m.status = ""
		}
		return m, nil

	case mediaCmdMsg:
		switch msg.kind {
		case "playpause":
			m.player.TogglePause()
		case "next":
			return m, tea.Batch(m.next(), nil)
		case "prev":
			return m, m.prev()
		case "stop":
			m.histSyncPos() // sample the position before the player forgets it
			m.player.Stop()
			m.playing = false
			m.publishMedia()
			return m, m.historyFlushCmd()
		case "seek":
			m.player.SeekMS(m.player.PositionMS() + msg.arg/1000)
		case "setpos":
			m.player.SeekMS(msg.arg / 1000)
		}
		m.publishMedia()
		return m, nil

	case controlCmdMsg:
		var cmd tea.Cmd
		switch msg.kind {
		case "playpause":
			m.player.TogglePause()
		case "next":
			cmd = m.next()
		case "prev":
			cmd = m.prev()
		case "stop":
			m.histSyncPos() // sample the position before the player forgets it
			m.player.Stop()
			m.playing = false
			cmd = m.historyFlushCmd()
		case "restart":
			m.player.SeekMS(0)
		case "repeat":
			if m.remote != nil {
				m.publishMedia()
				m.publishControl()
				return m, remoteCmd(m.remote.CycleRepeat)
			}
			m.q.CycleRepeat()
			m.player.ClearPreload() // upcoming track may differ under the new mode
			cmd = m.preloadNextCmd()
		case "shuffle":
			if m.remote != nil {
				m.publishMedia()
				m.publishControl()
				return m, remoteCmd(m.remote.ToggleShuffle)
			}
			m.q.ToggleShuffle()
			m.player.ClearPreload()
			cmd = m.preloadNextCmd()
		case "repeat-set":
			// SET variant: absolute repeat mode from a GUI/web/SDK controller.
			if m.remote != nil {
				m.publishMedia()
				m.publishControl()
				return m, remoteSetRepeatCmd(m.remote, msg.mode)
			}
			m.q.SetRepeat(parseRepeatMode(msg.mode))
			cmd = m.invalidatePreload() // upcoming track may differ under the new mode
		case "shuffle-set":
			// SET variant: absolute shuffle state from a GUI/web/SDK controller.
			if m.remote != nil {
				m.publishMedia()
				m.publishControl()
				return m, remoteSetShuffleCmd(m.remote, msg.on)
			}
			m.q.SetShuffle(msg.on)
			cmd = m.invalidatePreload()
		case "seek":
			m.player.SeekMS(msg.ms)
		case "volume":
			m.player.SetVolume(msg.vol)
		case "playtrack":
			cmd = m.playTrackByIDCmd(msg.id)
		case "playplaylist":
			cmd = m.playPlaylistByIDCmd(msg.id)
		case "sleep":
			m.player.SetSleepTimer(time.Duration(msg.ms)*time.Minute, msg.eot)
			m.status = sleepStatus(m.player)
		case "sleepcancel":
			m.player.CancelSleepTimer()
			m.status = i18n.T("Sleep timer off")
		}
		m.publishMedia()
		m.publishControl()
		return m, cmd

	case playNowMsg:
		if len(msg.tracks) == 0 {
			return m, nil
		}
		items := make([]list.Item, len(msg.tracks))
		for i, t := range msg.tracks {
			items[i] = trackRow(t)
		}
		m.q.Set(msg.tracks, 0)
		m.browse = msg.tracks // keep row activation consistent with the shown list
		m.episodeMode = msg.episodes
		m.ownPlaylists = false
		m.pageArtist = deezer.ArtistInfo{}
		m.list.Title = i18n.T("Now Playing")
		m.list.SetItems(items)
		m.list.ResetSelected()
		m.screen = screenList
		return m, m.playCurrent()

	case statsMsg:
		m.loading = false
		m.browse = nil // stats rows play by id, not through the browse slice
		m.ownPlaylists = false
		m.pageArtist = deezer.ArtistInfo{}
		m.list.Title = "📊 " + i18n.T("Stats")
		m.list.SetItems(statsItems(msg))
		m.list.ResetSelected()
		m.screen = screenList
		m.status = ""
		return m, nil

	case controlQueueEditMsg:
		return m.handleControlQueueEdit(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	// Delegate to the active sub-component.
	return m.delegate(msg)
}

func (m *Model) delegate(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if m.screen == screenSearch {
		m.search, cmd = m.search.Update(msg)
		return m, cmd
	}
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// No-Internet screen: retry re-establishes the session (a fresh login round-
	// trip doubles as a connectivity probe); on success loginDoneMsg lands us back
	// in the app. If the session is still valid (in-session drop), retry still just
	// re-logs in — cheap and it confirms the network is back.
	if m.screen == screenNoInternet {
		switch msg.String() {
		case "r", "enter":
			if m.loading {
				return m, nil
			}
			m.loading = true
			m.status = ""
			return m, m.loginCmd()
		case "q", "ctrl+c":
			m.shutdown()
			return m, tea.Quit
		}
		return m, nil
	}

	// Remote-control screens own their keys.
	if m.screen == screenRemoteCtl {
		return m.handleRemoteKey(msg)
	}
	if m.screen == screenRemoteInput {
		switch msg.String() {
		case "esc":
			m.screen = screenMenu
			m.list.Title = "OpenDeezer"
			m.list.SetItems(m.menuRows())
			m.list.ResetSelected()
			m.status = ""
			return m, nil
		case "enter":
			if m.search.Value() == "" {
				return m, nil
			}
			m.loading = true
			m.status = i18n.T("Connecting…")
			return m, m.remoteConnectCmd(m.search.Value(), true) // manual: trusted, may use token
		}
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		return m, cmd
	}

	if m.screen == screenWebRemote {
		return m.handleWebRemoteKey(msg)
	}

	// Playlist title prompt (create / rename) — a text input like remote entry.
	if m.screen == screenPlaylistPrompt {
		switch msg.String() {
		case "esc":
			m.search.Blur()
			m.plPrompt = plNone
			m.screen = m.plReturn
			m.status = ""
			return m, nil
		case "enter":
			title := strings.TrimSpace(m.search.Value())
			if title == "" {
				return m, nil
			}
			m.search.Blur()
			prompt := m.plPrompt
			m.plPrompt = plNone
			m.screen = m.plReturn
			m.loading = true
			m.status = i18n.T("Loading…")
			switch prompt {
			case plCreateWithTrack:
				return m, m.createPlaylistCmd(title, []string{m.pickTrack.ID})
			case plCreateEmpty:
				return m, m.createPlaylistCmd(title, nil)
			case plRename:
				return m, m.renamePlaylistCmd(m.plTarget, title)
			}
			m.loading = false
			return m, nil
		}
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		return m, cmd
	}

	// Search input captures most keys; handle it first.
	if m.screen == screenSearch {
		switch msg.String() {
		case "esc":
			m.search.Blur()
			m.screen = m.searchReturn
			return m, nil
		case "enter":
			q := m.search.Value()
			if q == "" {
				return m, nil
			}
			m.loading = true
			if m.searchPodcast {
				m.status = i18n.T("Searching…")
				return m, m.podcastSearchCmd(q)
			}
			m.status = i18n.T("Searching…")
			return m, m.searchCmd(q)
		}
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		return m, cmd
	}

	// Help is taller than many terminals. Scroll it directly instead of moving
	// the hidden list underneath the overlay.
	if m.screen == screenHelp {
		switch msg.String() {
		case "down", "j":
			m.helpOffset++
			return m, nil
		case "up", "k":
			m.helpOffset = max(0, m.helpOffset-1)
			return m, nil
		case "pgdown":
			m.helpOffset += max(1, m.height/2)
			return m, nil
		case "pgup":
			m.helpOffset = max(0, m.helpOffset-max(1, m.height/2))
			return m, nil
		case "home", "g":
			m.helpOffset = 0
			return m, nil
		case "end", "G":
			m.helpOffset = 1 << 30 // clamped to the last page by helpView
			return m, nil
		}
	}

	// Interactive queue view: cursor navigation + in-place editing. Unhandled
	// keys fall through to the global bindings (space, n/p, u to close, …).
	if m.screen == screenQueue {
		n := m.q.Len()
		switch msg.String() {
		case "down", "j":
			if m.queueSel < n-1 {
				m.queueSel++
			}
			return m, nil
		case "up", "k":
			if m.queueSel > 0 {
				m.queueSel--
			}
			return m, nil
		case "pgdown":
			m.queueSel = min(max(0, n-1), m.queueSel+max(1, m.height/2))
			return m, nil
		case "pgup":
			m.queueSel = max(0, m.queueSel-max(1, m.height/2))
			return m, nil
		case "g", "home":
			m.queueSel = 0
			return m, nil
		case "G", "end":
			m.queueSel = max(0, n-1)
			return m, nil
		case "enter":
			return m, m.queueJump(m.queueSel)
		case "x":
			return m, m.queueRemove(m.queueSel)
		case "J":
			return m, m.queueMove(m.queueSel, m.queueSel+1)
		case "K":
			return m, m.queueMove(m.queueSel, m.queueSel-1)
		}
	}

	// Let the list own keys while filtering (so typing works).
	if m.list.FilterState() == list.Filtering {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}

	// A playlist delete is awaiting confirmation: y deletes, n/esc cancels,
	// everything else is swallowed so stray keys can't act while it's pending.
	if m.plConfirm {
		switch msg.String() {
		case "y", "Y":
			m.plConfirm = false
			m.loading = true
			m.status = i18n.T("Deleting…")
			return m, m.deletePlaylistCmd(m.plTarget)
		case "n", "N", "esc":
			m.plConfirm = false
			m.status = ""
		}
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c", "q":
		m.shutdown()
		return m, tea.Quit
	case " ":
		// Driving a peer: transport keys target the peer, not the local player.
		if m.remote != nil {
			return m, remoteCmd(m.remote.PlayPause)
		}
		m.player.TogglePause()
		return m, nil
	case "n":
		// On a browse list with a track row selected, n queues it to play next
		// (see also e). Everywhere else it stays "next track".
		if t, ok := m.selectedBrowseTrack(); ok {
			m.q.InsertAfterCurrent(t)
			m.publishControl()
			m.status = i18n.Tf("Playing next: %s", t.Name)
			return m, m.invalidatePreload()
		}
		// Transport "next": drive the peer when connected (e.g. on the remote
		// lyrics/now-playing screens), otherwise the local queue.
		if m.remote != nil {
			return m, remoteCmd(m.remote.Next)
		}
		return m, m.next()
	case "e":
		// Add the selected browse track to the end of the queue.
		if t, ok := m.selectedBrowseTrack(); ok {
			m.q.Append(t)
			m.publishControl()
			m.status = i18n.Tf("Added to queue: %s", t.Name)
			return m, m.invalidatePreload()
		}
		return m, nil
	case "p":
		if m.remote != nil {
			return m, remoteCmd(m.remote.Prev)
		}
		return m, m.prev()
	case "m":
		// Start radio: a mix seeded from the selected track/artist (browse lists,
		// queue view) or the current track (now playing) replaces the queue.
		return m.startRadio()
	case "f":
		// Toggle like on the highlighted track row, else the current track. The
		// cached liked set flips optimistically; favToggleMsg rolls back on error.
		var t deezer.Track
		if it, ok := m.list.SelectedItem().(row); ok && it.kind == rowTrack && m.screen == screenList {
			t = it.track
		} else if ct, ok := m.q.Current(); ok && !m.episodeMode {
			t = ct
		} else {
			return m, nil
		}
		like := !m.likedIDs[t.ID]
		if m.likedIDs == nil {
			m.likedIDs = map[string]bool{}
		}
		if like {
			m.likedIDs[t.ID] = true
			m.status = i18n.T("Liking…")
		} else {
			delete(m.likedIDs, t.ID)
			m.status = i18n.T("Unliking…")
		}
		return m, m.favToggleCmd(t, like)
	case "a":
		// Add the highlighted track row to a playlist (opens the picker).
		if it, ok := m.list.SelectedItem().(row); ok && it.kind == rowTrack && m.screen == screenList {
			m.loading = true
			m.status = i18n.T("Loading…")
			return m, m.playlistPickCmd(it.track)
		}
		return m, nil
	case "r":
		// Driving a peer: forward the repeat cycle and let the peer's reported
		// state drive the display — do NOT mutate the local queue (it would drift
		// from the peer). Mirrors controlCmdMsg's remote handling.
		if m.remote != nil {
			return m, remoteCmd(m.remote.CycleRepeat)
		}
		m.status = i18n.Tf("Repeat: %s", i18n.T(m.q.CycleRepeat().String()))
		m.publishControl()
		// The upcoming track may have changed; drop the stale linear preload and
		// re-preload for the new mode (no-op preload when now non-deterministic).
		m.player.ClearPreload()
		return m, m.preloadNextCmd()
	case "z":
		// Driving a peer: forward the shuffle toggle without mutating local state.
		if m.remote != nil {
			return m, remoteCmd(m.remote.ToggleShuffle)
		}
		if m.q.ToggleShuffle() {
			m.status = i18n.T("Shuffle on")
		} else {
			m.status = i18n.T("Shuffle off")
		}
		m.publishControl()
		m.player.ClearPreload()
		return m, m.preloadNextCmd()
	case "g":
		m.list.Select(0)
		return m, nil
	case "G":
		if n := len(m.list.Items()); n > 0 {
			m.list.Select(n - 1)
		}
		return m, nil
	case "u":
		// On the About screen, "u" is a manual "check for updates" instead
		// of the usual queue shortcut (pointless there anyway).
		if m.screen == screenCredits {
			if m.updateChecking {
				return m, nil
			}
			m.updateChecking = true
			m.status = i18n.T("Checking for updates…")
			return m, m.updateCheckCmd()
		}
		m.toggleScreen(screenQueue)
		if m.screen == screenQueue {
			m.queueSel = max(0, m.q.Index()) // open with the cursor on the playing track
		}
		return m, nil
	case "t":
		m.status = i18n.Tf("Theme: %s", m.cycleTheme())
		return m, nil
	case "T":
		// Cycle the sleep timer: off → 15 → 30 → 45 → 60 min → end-of-track → off.
		m.cycleSleepTimer()
		m.status = sleepStatus(m.player)
		return m, nil
	case "R":
		// On the playlists screen, R renames the highlighted playlist.
		if p, ok := m.selectedOwnPlaylist(); ok {
			m.plPrompt = plRename
			m.plTarget = p
			m.plReturn = screenList
			m.search.SetValue(p.Name)
			m.search.Focus()
			m.screen = screenPlaylistPrompt
			m.status = ""
			return m, nil
		}
		on := !m.player.ReplayGain()
		m.player.SetReplayGain(on)
		_ = SaveReplayGain(on)
		if on {
			m.status = i18n.T("ReplayGain on (loudness normalization)")
		} else {
			m.status = i18n.T("ReplayGain off")
		}
		return m, nil
	case "E":
		on := !m.player.EQEnabled()
		m.player.SetEQEnabled(on)
		if on {
			m.status = i18n.Tf("Equalizer on (%s)", m.player.EQPreset())
		} else {
			m.status = i18n.T("Equalizer off")
		}
		return m, nil
	case "ctrl+e":
		// Cycle the EQ preset; wraps to the first after the last (a "custom"
		// state restarts the cycle).
		names := audio.EQPresetNames
		cur := m.player.EQPreset()
		next := names[0]
		for i, n := range names {
			if n == cur {
				next = names[(i+1)%len(names)]
				break
			}
		}
		_ = m.player.SetEQPreset(next)
		if m.player.EQEnabled() {
			m.status = i18n.Tf("EQ preset: %s", next)
		} else {
			m.status = i18n.Tf("EQ preset: %s (equalizer off — E to enable)", next)
		}
		return m, nil
	case "M":
		on := !m.player.MonoDownmix()
		m.player.SetMonoDownmix(on)
		if on {
			m.status = i18n.T("Mono downmix on")
		} else {
			m.status = i18n.T("Mono downmix off")
		}
		return m, nil
	case "d":
		// Output device picker.
		m.loading = true
		m.status = i18n.T("Loading…")
		return m, m.devicesCmd()
	case "x":
		// Cycle crossfade: 0 → 3s → 6s → 12s → 0.
		next := map[int]int{0: 3000, 3000: 6000, 6000: 12000, 12000: 0}[m.player.CrossfadeMS()]
		m.player.SetCrossfadeMS(next)
		_ = SaveCrossfadeMS(next)
		if next == 0 {
			m.status = i18n.T("Crossfade off")
		} else {
			m.status = i18n.Tf("Crossfade %ds", next/1000)
		}
		return m, nil
	case "ctrl+g":
		on := !m.player.Gapless()
		m.player.SetGapless(on)
		_ = SaveGapless(on)
		if on {
			m.status = i18n.T("Gapless on")
		} else {
			m.status = i18n.T("Gapless off")
		}
		return m, nil
	case "l":
		// Show synced lyrics for the current track. When a remote device is
		// connected, use the remote's now-playing track instead of the local queue.
		var t deezer.Track
		if m.remote != nil && m.remoteState.Track != nil {
			t = remoteTrack(m.remoteState.Track)
		} else if ct, ok := m.q.Current(); ok {
			t = ct
		} else {
			return m, nil
		}
		m.toggleScreen(screenLyrics)
		if m.screen == screenLyrics && (m.lyrics == nil || m.lyricsTrack != t.ID) {
			m.lyricsTrack = t.ID
			m.loading = true
			m.status = i18n.T("Loading…")
			return m, m.lyricsCmd(t)
		}
		return m, nil
	case "+", "=":
		// Driving a peer: adjust the peer's volume (from its reported state).
		if m.remote != nil {
			return m, remoteVolumeCmd(m.remote, m.remoteState.Volume+0.1)
		}
		m.status = volStatus(m.player.AddVolume(0.1))
		return m, nil
	case "-", "_":
		if m.remote != nil {
			return m, remoteVolumeCmd(m.remote, m.remoteState.Volume-0.1)
		}
		m.status = volStatus(m.player.AddVolume(-0.1))
		return m, nil
	case "left":
		// Driving a peer: seek the peer relative to its reported position.
		if m.remote != nil {
			return m, remoteSeekCmd(m.remote, m.remoteState.PositionMS-10000)
		}
		m.player.SeekMS(m.player.PositionMS() - 10000)
		return m, nil
	case "right":
		if m.remote != nil {
			return m, remoteSeekCmd(m.remote, m.remoteState.PositionMS+10000)
		}
		m.player.SeekMS(m.player.PositionMS() + 10000)
		return m, nil
	case "h":
		q := (m.client.Quality() + 1) % 3
		m.client.SetQuality(q)
		_ = SaveQuality(q)
		switch q {
		case 2:
			m.status = i18n.T("Audio quality: HiFi (FLAC)")
		case 1:
			m.status = i18n.T("Audio quality: High (MP3 320)")
		default:
			m.status = i18n.T("Audio quality: Normal (MP3 128)")
		}
		return m, nil
	case "D":
		// Download a full track to disk (premium-only). Prefer the highlighted
		// track row; otherwise fall back to the current track (not podcasts).
		var t deezer.Track
		if it, ok := m.list.SelectedItem().(row); ok && it.kind == rowTrack {
			t = it.track
		} else if ct, ok := m.q.Current(); ok && !m.episodeMode {
			t = ct
		} else {
			return m, nil
		}
		if !m.acct.Premium {
			m.status = i18n.T("Downloads need a paid Deezer plan")
			return m, nil
		}
		if m.downloading {
			m.status = i18n.T("A download is already in progress…")
			return m, nil
		}
		m.downloading = true
		m.status = i18n.Tf("Downloading %s…", t.Name)
		return m, m.downloadCmd(t)
	case "A":
		// Toggle Deezer Free's play-reporting/ads (free accounts only). Paid plans
		// have no ads, so it's a no-op there.
		if m.acct.Premium {
			m.status = i18n.T("Your plan has no ads")
			return m, nil
		}
		off := !m.client.AdsDisabled()
		m.client.SetAdsDisabled(off)
		_ = config.SaveAdsDisabled(off)
		if off {
			m.status = i18n.T("Ads off — plays no longer reported (at your own risk: breaks Deezer's terms, denies artists their play count)")
		} else {
			m.status = i18n.T("Ads on — plays reported to Deezer (supports artists)")
		}
		return m, nil
	case "s":
		// Driving a peer: stop the peer's playback, not the local player.
		if m.remote != nil {
			return m, remoteCmd(m.remote.Stop)
		}
		m.histSyncPos() // sample the position before the player forgets it
		m.player.Stop()
		m.playing = false
		return m, m.historyFlushCmd()
	case "/":
		m.openSearch(false)
		return m, nil
	case "c":
		m.toggleScreen(screenNowPlaying)
		return m, nil
	case "?":
		if m.screen != screenHelp {
			m.helpOffset = 0
		}
		m.toggleScreen(screenHelp)
		return m, nil
	case "i":
		m.toggleScreen(screenCredits)
		return m, nil
	case "U":
		// Open the available update's release page. Never downloads or
		// installs anything itself.
		if m.updateInfo.HasUpdate && m.updateInfo.URL != "" {
			openInBrowser(m.updateInfo.URL)
			m.updateDismissed = true
		}
		return m, nil
	case "X":
		// On the playlists screen, X deletes the highlighted playlist (after a
		// y/n confirm). Elsewhere it dismisses the update notice.
		if p, ok := m.selectedOwnPlaylist(); ok {
			m.plConfirm = true
			m.plTarget = p
			m.status = i18n.Tf("Delete playlist %s? (y/n)", p.Name)
			return m, nil
		}
		// Dismiss the footer's "update available" notice for this session.
		m.updateDismissed = true
		return m, nil
	case "N":
		// New empty playlist, from the playlists screen.
		if m.screen == screenList && m.ownPlaylists {
			m.plPrompt = plCreateEmpty
			m.plReturn = screenList
			m.search.SetValue("")
			m.search.Focus()
			m.screen = screenPlaylistPrompt
			m.status = ""
			return m, nil
		}
		return m, nil
	case "ctrl+f":
		// Start the local list filter (the list's Filter binding is ctrl+f, "/"
		// being Deezer search) — but only where the list is the visible body, so
		// an overlay can't put the hidden list into filtering mode.
		switch m.screen {
		case screenMenu, screenList, screenDevices, screenRemote, screenPlaylistPick:
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			return m, cmd
		}
		return m, nil
	case "esc", "backspace":
		// An applied local filter clears first; a second esc then navigates.
		// (While typing the filter, the FilterState()==Filtering block above owns
		// esc via the list's cancel binding.)
		if m.list.FilterState() == list.FilterApplied {
			switch m.screen {
			case screenMenu, screenList, screenDevices, screenRemote, screenPlaylistPick:
				m.list.ResetFilter()
				return m, nil
			}
		}
		switch m.screen {
		case screenNowPlaying, screenCredits, screenQueue, screenLyrics, screenHelp:
			m.screen = m.prevScreen
		case screenDevices, screenPlaylistPick:
			m.restoreList() // the picker replaced m.list; put the browse list back
			m.screen = m.prevScreen
		case screenList, screenRemote:
			m.screen = screenMenu
			m.list.Title = "OpenDeezer"
			m.list.SetItems(m.menuRows())
			m.list.ResetSelected()
		}
		return m, nil
	case "enter":
		return m.activate()
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// startRadio handles the 'm' key: pick a mix seed from context and start it.
// On a browse list the selected row decides (track row → song radio, artist
// row → artist radio, stats row → song radio); with no seed row on an artist
// page, the page's artist seeds. On the queue view the cursor row seeds; on
// now-playing (and anywhere else) the current track does. Podcast episodes
// have no mixes.
func (m *Model) startRadio() (tea.Model, tea.Cmd) {
	radio := func(cmd tea.Cmd) (tea.Model, tea.Cmd) {
		m.loading = true
		m.status = i18n.T("Starting radio…")
		return m, cmd
	}
	if m.screen == screenList {
		if it, ok := m.list.SelectedItem().(row); ok {
			switch {
			case it.kind == rowTrack:
				return radio(m.trackMixCmd(it.track.ID))
			case it.kind == rowArtist:
				return radio(m.artistMixCmd(it.artist.ID))
			case it.kind == rowHistory && it.histID != "":
				return radio(m.trackMixCmd(it.histID))
			}
		}
		if m.pageArtist.ID != "" {
			return radio(m.artistMixCmd(m.pageArtist.ID))
		}
		return m, nil
	}
	if m.screen == screenQueue && !m.episodeMode {
		if ts := m.q.Tracks(); m.queueSel >= 0 && m.queueSel < len(ts) {
			return radio(m.trackMixCmd(ts[m.queueSel].ID))
		}
		return m, nil
	}
	if t, ok := m.q.Current(); ok && !m.episodeMode {
		return radio(m.trackMixCmd(t.ID))
	}
	return m, nil
}

// openSearch records where the search overlay was opened and resets its mode.
// Keeping a dedicated return screen avoids corrupting prevScreen, which belongs
// to now-playing/help/lyrics overlays that may themselves open search.
func (m *Model) openSearch(podcasts bool) {
	m.searchReturn = m.screen
	m.searchPodcast = podcasts
	m.search.SetValue("")
	m.search.Focus()
	m.status = ""
	m.screen = screenSearch
}

// activate handles Enter on the selected row.
func (m *Model) activate() (tea.Model, tea.Cmd) {
	it, ok := m.list.SelectedItem().(row)
	if !ok {
		return m, nil
	}
	switch it.kind {
	case rowMenu:
		switch it.action {
		case actLiked:
			m.status = i18n.T("Loading…")
			m.loading = true
			return m, m.favoritesCmd()
		case actPlaylists:
			m.status = i18n.T("Loading…")
			m.loading = true
			return m, m.playlistsCmd()
		case actCharts:
			m.status = i18n.T("Loading…")
			m.loading = true
			return m, m.chartsCmd()
		case actFlow:
			m.status = i18n.T("Loading…")
			m.loading = true
			return m, m.flowCmd()
		case actSearch:
			m.openSearch(false)
			return m, nil
		case actStats:
			m.status = i18n.T("Loading…")
			m.loading = true
			return m, m.statsCmd()
		case actPodcasts:
			m.openSearch(true)
			return m, nil
		case actRemote:
			m.loading = true
			m.status = i18n.T("Scanning…")
			return m, m.discoverDevicesCmd()
		case actWebRemote:
			m.screen = screenWebRemote
			m.status = ""
			return m, nil
		case actLanguage:
			// Cycle the UI language and rebuild the home menu so every row
			// re-renders in the new language (the list holds a static snapshot of
			// its items; overlays/footer re-read T() every frame on their own).
			name := m.cycleLanguage()
			idx := m.list.Index()
			m.list.SetItems(m.menuRows())
			if idx >= 0 && idx < len(m.list.Items()) {
				m.list.Select(idx)
			}
			m.status = i18n.Tf("Language: %s", name)
			return m, nil
		case actRemoteManual:
			m.search.SetValue(LoadLastPeer())
			m.search.Focus()
			m.screen = screenRemoteInput
			m.status = ""
			return m, nil
		case actResume:
			if r := LoadResume(); r != nil {
				m.q.Set([]deezer.Track{r.Track()}, 0)
				m.episodeMode = false
				m.pendingSeek = r.PositionMS
				return m, m.playCurrent()
			}
			return m, nil
		case actNewPlaylist:
			// "New playlist…" inside the add-to-playlist picker: put the browsed
			// list back and prompt for a title; the new playlist is seeded with
			// the picked track.
			m.plPrompt = plCreateWithTrack
			m.plReturn = m.prevScreen
			m.restoreList()
			m.search.SetValue("")
			m.search.Focus()
			m.screen = screenPlaylistPrompt
			m.status = ""
			return m, nil
		}
	case rowTrack:
		// Explicit play: commit the browsed list to the play queue at this row.
		// This is the only place browsing turns into the live queue.
		m.playBrowsed(it.track.ID, false)
		return m, m.playCurrent()
	case rowHistory:
		// Stats rows carry only the track id — fetch the full track and play it
		// (same path as the control API's "play track <id>").
		if it.histID == "" {
			return m, nil
		}
		m.status = i18n.T("Loading…")
		m.loading = true
		return m, m.playTrackByIDCmd(it.histID)
	case rowArtist:
		m.status = i18n.T("Loading…")
		m.loading = true
		return m, m.artistPageCmd(it.artist)
	case rowPodcast:
		m.status = i18n.T("Loading…")
		m.loading = true
		return m, m.episodesCmd(it.podcast)
	case rowEpisode:
		// Explicit play: commit the browsed episodes to the play queue at this row.
		m.playBrowsed(it.episode.ID, true)
		return m, m.playCurrent()
	case rowDevice:
		if err := m.player.SetDevice(it.deviceID); err != nil {
			m.status = i18n.Tf("Device error: %s", err.Error())
		} else {
			_ = SaveAudioDevice(it.deviceID)
			m.status = i18n.Tf("Output: %s", it.title)
		}
		m.restoreList() // the picker replaced m.list; put the browse list back
		m.screen = m.prevScreen
		return m, nil
	case rowPlaylist:
		// Inside the add-to-playlist picker, enter adds the picked track here
		// instead of opening the playlist.
		if m.screen == screenPlaylistPick {
			t := m.pickTrack
			m.restoreList()
			m.screen = m.prevScreen
			m.loading = true
			m.status = i18n.T("Loading…")
			return m, m.addToPlaylistCmd(it.playlist, t)
		}
		m.status = i18n.T("Loading…")
		m.loading = true
		return m, m.playlistTracksCmd(it.playlist)
	case rowAlbum:
		m.status = i18n.T("Loading…")
		m.loading = true
		return m, m.albumTracksCmd(it.album)
	case rowPeer:
		m.loading = true
		m.status = i18n.Tf("Connecting to %s…", it.title)
		return m, m.remoteConnectCmd(it.peerAddr, false) // discovered: account-only
	}
	return m, nil
}

// playBrowsed replaces the play queue with the browsed list, positioned at the
// activated row (mapped by the visible index, falling back to an ID search when
// filtering or non-track rows shift it), and sets the episode mode. Called only
// on an explicit play so browsing never clobbers the live queue.
func (m *Model) playBrowsed(id string, episodes bool) {
	idx := m.list.Index()
	if idx < 0 || idx >= len(m.browse) || m.browse[idx].ID != id {
		idx = m.findInBrowse(id)
	}
	m.q.Set(m.browse, idx)
	m.episodeMode = episodes
}

func (m *Model) findInBrowse(id string) int {
	for i, t := range m.browse {
		if t.ID == id {
			return i
		}
	}
	return 0
}

// restoreList puts back the list the device picker temporarily replaced.
func (m *Model) restoreList() {
	if m.devSavedItems == nil {
		return
	}
	m.list.SetItems(m.devSavedItems)
	m.list.Title = m.devSavedTitle
	if m.devSavedIndex >= 0 && m.devSavedIndex < len(m.devSavedItems) {
		m.list.Select(m.devSavedIndex)
	}
	m.devSavedItems = nil
}

// playCurrent resolves + plays the queue's current track.
func (m *Model) playCurrent() tea.Cmd {
	// The player is still on the outgoing track here (the queue cursor already
	// moved): capture its final position for the listening-history session,
	// which onTrackChanged flushes once the new stream is live.
	m.histSyncPos()
	t, ok := m.q.Current()
	if !ok {
		return nil
	}
	m.status = i18n.Tf("Loading: %s", t.Name)
	m.loading = true
	if m.episodeMode {
		return m.episodeStreamCmd(t)
	}
	return m.streamCmd(t)
}

// playTrackByIDCmd fetches a track by id and plays it (control API).
func (m *Model) playTrackByIDCmd(id string) tea.Cmd {
	return func() tea.Msg {
		t, err := m.client.Track(id)
		if err != nil {
			return errMsg{err}
		}
		return playNowMsg{tracks: []deezer.Track{t}}
	}
}

// playPlaylistByIDCmd loads a playlist by id and plays it from the top.
func (m *Model) playPlaylistByIDCmd(id string) tea.Cmd {
	return func() tea.Msg {
		ts, err := m.client.PlaylistTracks(id)
		if err != nil {
			return errMsg{err}
		}
		return playNowMsg{tracks: ts}
	}
}

// selectedBrowseTrack returns the highlighted track row on a browse list, for
// the n/e queue-building keys. Episode queues are excluded — mixing a music
// track into a plain-stream episode queue would break playback resolution.
func (m *Model) selectedBrowseTrack() (deezer.Track, bool) {
	if m.screen != screenList || m.episodeMode {
		return deezer.Track{}, false
	}
	it, ok := m.list.SelectedItem().(row)
	if !ok || it.kind != rowTrack {
		return deezer.Track{}, false
	}
	return it.track, true
}

// selectedOwnPlaylist returns the highlighted playlist when the user's own
// playlists screen is showing — the gate for the N/R/X write ops.
func (m *Model) selectedOwnPlaylist() (deezer.Playlist, bool) {
	if m.screen != screenList || !m.ownPlaylists {
		return deezer.Playlist{}, false
	}
	it, ok := m.list.SelectedItem().(row)
	if !ok || it.kind != rowPlaylist {
		return deezer.Playlist{}, false
	}
	return it.playlist, true
}

// invalidatePreload drops a possibly-stale gapless preload after the upcoming
// track changed (queue edit, jump), mirroring what the repeat/shuffle toggles
// do, and re-preloads for the new order. Nil-player safe so pure-UI tests can
// exercise queue editing without an audio device.
func (m *Model) invalidatePreload() tea.Cmd {
	if m.player == nil {
		return nil
	}
	m.player.ClearPreload()
	return m.preloadNextCmd()
}

// queueJump plays the queue entry under the queue-view cursor.
func (m *Model) queueJump(i int) tea.Cmd {
	if i < 0 || i >= m.q.Len() || i == m.q.Index() {
		return nil
	}
	m.q.SetIndex(i)
	m.publishControl()
	// The upcoming track changed with the cursor; drop the stale preload (the
	// stream-ready handler re-preloads once the picked track is playing).
	if m.player != nil {
		m.player.ClearPreload()
	}
	return m.playCurrent()
}

// queueRemove deletes the queue entry under the queue-view cursor. The playing
// track can't be removed — the queue cursor must keep matching the audio.
func (m *Model) queueRemove(i int) tea.Cmd {
	if i < 0 || i >= m.q.Len() {
		return nil
	}
	if i == m.q.Index() {
		m.status = i18n.T("Can't remove the playing track")
		return nil
	}
	name := m.q.Tracks()[i].Name
	if !m.q.Remove(i) {
		return nil
	}
	if m.queueSel >= m.q.Len() {
		m.queueSel = max(0, m.q.Len()-1)
	}
	m.publishControl()
	m.status = i18n.Tf("Removed from queue: %s", name)
	return m.invalidatePreload()
}

// queueMove moves the queue entry under the cursor to j (J/K), cursor following.
func (m *Model) queueMove(i, j int) tea.Cmd {
	if !m.q.Move(i, j) {
		return nil
	}
	m.queueSel = j
	m.publishControl()
	return m.invalidatePreload()
}

func (m *Model) next() tea.Cmd {
	if m.q.Next() {
		return m.playCurrent()
	}
	return nil
}

func (m *Model) prev() tea.Cmd {
	if m.q.Prev() {
		return m.playCurrent()
	}
	return nil
}

// advance is called when a track finishes naturally.
func (m *Model) advance() tea.Cmd {
	if m.q.AdvanceAuto() {
		return m.playCurrent()
	}
	m.playing = false
	m.saveResume()
	// End of the queue: nothing new will flush the session — record it now
	// (histMarkFull already ran on the finish message).
	return m.historyFlushCmd()
}

// onTrackChanged refreshes now-playing state (media, lyrics, cover) for a newly
// active track, records the outgoing track's listening-history session, and
// returns the follow-up commands (history write + cover fetch) if applicable.
func (m *Model) onTrackChanged(t deezer.Track) tea.Cmd {
	flush := m.historyFlushCmd() // record the outgoing track (if any)
	m.histStartSession(t)
	m.status = ""
	m.publishMedia()
	m.publishControl()
	m.lyrics = nil
	m.lyricsTrack = ""
	m.lyricsOffset = 0
	m.curImg = nil
	m.curCover = ""
	m.curImgTrack = t.ID
	if artworkSupported() && t.ArtworkURL != "" {
		cover := m.coverCmd(t.ID, t.ArtworkURL)
		if flush != nil {
			return tea.Batch(flush, cover)
		}
		return cover
	}
	return flush
}

// toggleScreen flips to dst (remembering the screen to return to) or back.
func (m *Model) toggleScreen(dst screen) {
	if m.screen == dst {
		m.screen = m.prevScreen
		return
	}
	if m.screen == screenDevices || m.screen == screenPlaylistPick {
		m.restoreList() // leaving a picker for an overlay; put the borrowed list back
	}
	// Don't stack overlay-on-overlay (incl. the pickers) as the return target.
	switch m.screen {
	case screenNowPlaying, screenCredits, screenQueue, screenLyrics, screenHelp,
		screenDevices, screenPlaylistPick:
	default:
		m.prevScreen = m.screen
	}
	m.screen = dst
}

// saveResume persists the current track + position so it can be resumed later.
func (m *Model) saveResume() {
	if t, ok := m.q.Current(); ok && m.playing {
		_ = SaveResume(t, m.player.PositionMS())
	}
}

// shutdown persists resume state and closes every background service. Shared by
// all quit paths so they can't drift (missing a saveResume or a Close leaks or
// loses state).
func (m *Model) shutdown() {
	m.histSyncPos()
	m.historyFlushNow() // synchronous: a goroutine could be killed mid-write on quit
	m.saveResume()
	m.player.Stop()
	if m.media != nil {
		m.media.Close()
	}
	if m.discord != nil {
		m.discord.Close()
	}
	if m.ctrl != nil {
		m.ctrl.Close()
	}
	if m.webRemoteSrv != nil && m.webRemoteSrv != m.ctrl {
		m.webRemoteSrv.Close()
	}
	if m.advertiser != nil {
		m.advertiser.Close()
	}
}

func volStatus(v float64) string {
	return i18n.Tf("Volume %d%%", int(v*100+0.5))
}
