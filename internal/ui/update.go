package ui

import (
	"errors"
	"time"

	"github.com/Cycl0o0/OpenDeezer/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/internal/i18n"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

const footerHeight = 4

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
		m.list.SetSize(msg.Width, max(1, msg.Height-footerHeight))
		m.ready = true
		m.refreshCover()
		return m, nil

	case loginDoneMsg:
		m.loading = false
		if msg.err != nil {
			if errors.Is(msg.err, deezer.ErrARLExpired) {
				m.status = i18n.T("ARL expired or invalid — refresh the 'arl' cookie from deezer.com, then `opendeezer -save-arl <arl>`")
			} else {
				m.status = i18n.Tf("Login failed (network?): %s", msg.err.Error())
			}
			return m, nil
		}
		m.acct = m.client.Account()
		m.publishAccount() // identity snapshot for the control API (race-free read)
		// Free accounts can't stream on-demand — gate the whole app behind a
		// message (Premium required).
		if !m.acct.Premium {
			m.screen = screenBlocked
			m.status = ""
			return m, nil
		}
		m.screen = screenMenu
		if m.acct.Name != "" {
			m.status = i18n.Tf("Logged in as %s · %s", m.acct.Name, m.acct.Offer)
		} else {
			m.status = i18n.Tf("Logged in · %s", m.acct.Offer)
		}
		// Warn if the chosen quality exceeds the plan's entitlement.
		if q := m.client.Quality(); (q == 2 && !m.acct.CanHiFi) || (q == 1 && !m.acct.CanHQ) {
			m.status += "  " + i18n.T("(plan can't stream that quality — will fall back)")
		}
		m.list.Title = "OpenDeezer"
		m.list.SetItems(m.menuRows())
		return m, nil

	case tracksMsg:
		m.loading = false
		items := make([]list.Item, len(msg.tracks))
		for i, t := range msg.tracks {
			items[i] = trackRow(t)
		}
		// Browsing must not touch the live play queue — hold the tracks aside and
		// only commit to the queue on an explicit play (see activate).
		m.browse = msg.tracks
		m.list.Title = msg.title
		m.list.SetItems(items)
		m.list.ResetSelected()
		m.screen = screenList
		m.status = ""
		return m, nil

	case podcastsMsg:
		m.loading = false
		items := make([]list.Item, len(msg.podcasts))
		for i, p := range msg.podcasts {
			items[i] = podcastRow(p)
		}
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

	case playlistsMsg:
		m.loading = false
		items := make([]list.Item, len(msg.playlists))
		for i, p := range msg.playlists {
			items[i] = playlistRow(p)
		}
		m.list.Title = msg.title
		m.list.SetItems(items)
		m.list.ResetSelected()
		m.screen = screenList
		m.status = ""
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
		// Preload the next track for a gapless/crossfaded transition.
		return m, tea.Batch(cmd, m.preloadNextCmd())

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
			m.q.AdvanceLinear()
			m.playing = true
			if t, ok := m.q.Current(); ok {
				return m, tea.Batch(m.onTrackChanged(t), m.preloadNextCmd(), m.waitFinish())
			}
			return m, m.waitFinish()
		case m.player.State() == audio.Stopped:
			// Track ended with no preload and nothing new started: advance + play.
			return m, tea.Batch(m.advance(), m.waitFinish())
		default:
			// A user selection is already loading/playing — don't advance over it.
			return m, m.waitFinish()
		}

	case errMsg:
		m.loading = false
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
		// The end-of-track sleep timer stops the player without firing onFinish (so
		// the queue can't auto-advance), leaving m.playing stale. Reconcile it so the
		// footer stops showing "playing" and quit won't persist a bogus end-of-track
		// resume point.
		if m.playing && m.player.State() == audio.Stopped {
			m.playing = false
		}
		m.publishMedia()
		m.publishControl()
		// Poll the peer on the remote-control screen and while viewing its lyrics
		// (so synced lyrics scroll with the peer's position).
		if m.remote != nil && (m.screen == screenRemoteCtl || m.screen == screenLyrics) {
			return m, tea.Batch(tickCmd(), remotePollCmd(m.remote))
		}
		return m, tickCmd()

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
			m.player.Stop()
			m.playing = false
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
			m.player.Stop()
			m.playing = false
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
		m.list.Title = i18n.T("Now Playing")
		m.list.SetItems(items)
		m.list.ResetSelected()
		m.screen = screenList
		return m, m.playCurrent()

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

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
	// Free-account block: only quit is allowed.
	if m.screen == screenBlocked {
		switch msg.String() {
		case "q", "ctrl+c", "esc":
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

	// Search input captures most keys; handle it first.
	if m.screen == screenSearch {
		switch msg.String() {
		case "esc":
			m.screen = screenMenu
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

	// Let the list own keys while filtering (so typing works).
	if m.list.FilterState() == list.Filtering {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "ctrl+c", "q":
		m.shutdown()
		return m, tea.Quit
	case " ":
		m.player.TogglePause()
		return m, nil
	case "n":
		return m, m.next()
	case "p":
		return m, m.prev()
	case "f":
		// Like the current track.
		if t, ok := m.q.Current(); ok && !m.episodeMode {
			m.status = i18n.T("Liking…")
			return m, m.likeCurrentCmd(t)
		}
		return m, nil
	case "r":
		m.status = i18n.Tf("Repeat: %s", i18n.T(m.q.CycleRepeat().String()))
		m.publishControl()
		if m.remote != nil {
			return m, remoteCmd(m.remote.CycleRepeat)
		}
		// The upcoming track may have changed; drop the stale linear preload and
		// re-preload for the new mode (no-op preload when now non-deterministic).
		m.player.ClearPreload()
		return m, m.preloadNextCmd()
	case "z":
		if m.q.ToggleShuffle() {
			m.status = i18n.T("Shuffle on")
		} else {
			m.status = i18n.T("Shuffle off")
		}
		m.publishControl()
		if m.remote != nil {
			return m, remoteCmd(m.remote.ToggleShuffle)
		}
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
		m.status = volStatus(m.player.AddVolume(0.1))
		return m, nil
	case "-", "_":
		m.status = volStatus(m.player.AddVolume(-0.1))
		return m, nil
	case "left":
		m.player.SeekMS(m.player.PositionMS() - 10000)
		return m, nil
	case "right":
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
	case "s":
		m.player.Stop()
		m.playing = false
		return m, nil
	case "/":
		m.search.SetValue("")
		m.search.Focus()
		m.screen = screenSearch
		return m, nil
	case "c":
		m.toggleScreen(screenNowPlaying)
		return m, nil
	case "?":
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
		// Dismiss the footer's "update available" notice for this session.
		m.updateDismissed = true
		return m, nil
	case "esc", "backspace":
		switch m.screen {
		case screenNowPlaying, screenCredits, screenQueue, screenLyrics, screenHelp:
			m.screen = m.prevScreen
		case screenDevices:
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
			m.searchPodcast = false
			m.search.SetValue("")
			m.search.Focus()
			m.screen = screenSearch
			return m, nil
		case actPodcasts:
			m.searchPodcast = true
			m.search.SetValue("")
			m.search.Focus()
			m.screen = screenSearch
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
		}
	case rowTrack:
		// Explicit play: commit the browsed list to the play queue at this row.
		// This is the only place browsing turns into the live queue.
		m.playBrowsed(it.track.ID, false)
		return m, m.playCurrent()
	case rowArtist:
		m.status = i18n.T("Loading…")
		m.loading = true
		return m, m.artistTopCmd(it.artist)
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
	return nil
}

// onTrackChanged refreshes now-playing state (media, lyrics, cover) for a newly
// active track and returns a cover-fetch command if applicable.
func (m *Model) onTrackChanged(t deezer.Track) tea.Cmd {
	m.status = ""
	m.publishMedia()
	m.publishControl()
	m.lyrics = nil
	m.lyricsTrack = ""
	m.curImg = nil
	m.curCover = ""
	m.curImgTrack = t.ID
	if artworkSupported() && t.ArtworkURL != "" {
		return m.coverCmd(t.ID, t.ArtworkURL)
	}
	return nil
}

// toggleScreen flips to dst (remembering the screen to return to) or back.
func (m *Model) toggleScreen(dst screen) {
	if m.screen == dst {
		m.screen = m.prevScreen
		return
	}
	if m.screen == screenDevices {
		m.restoreList() // leaving the picker for an overlay; put the borrowed list back
	}
	// Don't stack overlay-on-overlay (incl. the device picker) as the return target.
	switch m.screen {
	case screenNowPlaying, screenCredits, screenQueue, screenLyrics, screenHelp, screenDevices:
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
