package ui

import (
	"errors"
	"strconv"

	"github.com/Cycl0o0/OpenDeezer/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/internal/deezer"
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
			title: "▶  Resume — " + r.Name,
			desc:  r.ArtistLine + " · " + fmtMS(r.PositionMS) + " / " + fmtMS(r.DurationMS),
		})
	}
	rows = append(rows,
		row{kind: rowMenu, title: "❤  Liked Songs", desc: "your favorite tracks", action: actLiked},
		row{kind: rowMenu, title: "≡  My Playlists", desc: "playlists you own", action: actPlaylists},
		row{kind: rowMenu, title: "⚡ Flow", desc: "your personalized stream", action: actFlow},
		row{kind: rowMenu, title: "📈 Charts", desc: "top tracks, albums & artists", action: actCharts},
		row{kind: rowMenu, title: "🎙 Podcasts", desc: "search shows & episodes", action: actPodcasts},
		row{kind: rowMenu, title: "🔍 Search", desc: "tracks, albums, artists, playlists", action: actSearch},
		row{kind: rowMenu, title: "📡 Remote control", desc: "drive another OpenDeezer client", action: actRemote},
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
				m.status = "ARL expired or invalid — refresh the 'arl' cookie from deezer.com, then `opendeezer -save-arl <arl>`"
			} else {
				m.status = "Login failed (network?): " + msg.err.Error()
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
			m.status = "Logged in as " + m.acct.Name + " · " + m.acct.Offer
		} else {
			m.status = "Logged in · " + m.acct.Offer
		}
		// Warn if the chosen quality exceeds the plan's entitlement.
		if q := m.client.Quality(); (q == 2 && !m.acct.CanHiFi) || (q == 1 && !m.acct.CanHQ) {
			m.status += "  (plan can't stream that quality — will fall back)"
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
		m.q.Set(msg.tracks, 0)
		m.episodeMode = false
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
		q := make([]deezer.Track, len(msg.episodes))
		for i, e := range msg.episodes {
			items[i] = episodeRow(e)
			q[i] = e.AsTrack()
		}
		m.q.Set(q, 0)
		m.episodeMode = true
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
		// Keep tracks as the playable queue context.
		m.q.Set(msg.results.Tracks, 0)
		m.episodeMode = false
		m.list.Title = "Results"
		m.list.SetItems(items)
		m.list.ResetSelected()
		m.screen = screenList
		m.status = ""
		return m, nil

	case streamReadyMsg:
		m.loading = false
		if err := m.player.Play(msg.plan, msg.track.DurationMS); err != nil {
			m.status = "Playback error: " + err.Error()
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
		if msg.plan != nil {
			m.player.Preload(msg.plan, msg.dur)
		}
		return m, nil

	case devicesMsg:
		items := make([]list.Item, len(msg.devices))
		cur := m.player.CurrentDevice()
		for i, d := range msg.devices {
			items[i] = deviceRow(d.ID, d.Name, d.ID == cur)
		}
		m.list.Title = "Output device"
		m.list.SetItems(items)
		m.list.ResetSelected()
		m.prevScreen = m.screen
		m.screen = screenDevices
		m.loading = false
		m.status = ""
		return m, nil

	case lyricsMsg:
		m.loading = false
		if msg.err != nil {
			m.status = "Lyrics: " + msg.err.Error()
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
		// If the player is still Playing, it gaplessly swapped to the preloaded
		// next track — sync the queue pointer (only preloaded for the linear next)
		// and refresh the UI without re-Play()ing. Otherwise advance + play.
		if m.player.State() == audio.Playing {
			m.q.Next()
			m.playing = true
			if t, ok := m.q.Current(); ok {
				return m, tea.Batch(m.onTrackChanged(t), m.preloadNextCmd(), m.waitFinish())
			}
		}
		return m, tea.Batch(m.advance(), m.waitFinish())

	case errMsg:
		m.loading = false
		m.status = "Error: " + msg.err.Error()
		return m, nil

	case tickMsg:
		m.publishMedia()
		m.publishControl()
		if m.remote != nil && m.screen == screenRemoteCtl {
			return m, tea.Batch(tickCmd(), remotePollCmd(m.remote))
		}
		return m, tickCmd()

	case remoteConnMsg:
		m.loading = false
		if msg.err != nil {
			m.status = "Remote: " + msg.err.Error()
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
		m.status = "Connected to " + name
		_ = SaveLastPeer(msg.addr)
		return m, nil

	case remoteStateMsg:
		if msg.err != nil {
			m.status = "Remote: " + msg.err.Error()
			return m, nil
		}
		m.remoteState = msg.state
		return m, nil

	case devicesDiscoveredMsg:
		m.loading = false
		items := []list.Item{row{
			kind: rowMenu, action: actRemoteManual,
			title: "✎  Enter address…", desc: "type a host:port manually",
		}}
		for _, p := range msg.peers {
			items = append(items, peerRow(p))
		}
		m.list.Title = "Connect to a device"
		m.list.SetItems(items)
		m.list.ResetSelected()
		m.screen = screenRemote
		if len(msg.peers) == 0 {
			m.status = "No devices found — enable OPENDEEZER_CONTROL=:7654 on the target."
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
			m.q.CycleRepeat()
		case "shuffle":
			m.q.ToggleShuffle()
		case "seek":
			m.player.SeekMS(msg.ms)
		case "volume":
			m.player.SetVolume(msg.vol)
		case "playtrack":
			cmd = m.playTrackByIDCmd(msg.id)
		case "playplaylist":
			cmd = m.playPlaylistByIDCmd(msg.id)
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
		m.episodeMode = msg.episodes
		m.list.Title = "Now Playing"
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
			if m.advertiser != nil {
				m.advertiser.Close()
			}
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
			m.status = "Connecting…"
			return m, m.remoteConnectCmd(m.search.Value(), true) // manual: trusted, may use token
		}
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		return m, cmd
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
				m.status = "Searching podcasts…"
				return m, m.podcastSearchCmd(q)
			}
			m.status = "Searching…"
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
		if m.advertiser != nil {
			m.advertiser.Close()
		}
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
			m.status = "Liking…"
			return m, m.likeCurrentCmd(t)
		}
		return m, nil
	case "r":
		m.status = "Repeat: " + m.q.CycleRepeat().String()
		return m, nil
	case "z":
		if m.q.ToggleShuffle() {
			m.status = "Shuffle on"
		} else {
			m.status = "Shuffle off"
		}
		return m, nil
	case "g":
		m.list.Select(0)
		return m, nil
	case "G":
		if n := len(m.list.Items()); n > 0 {
			m.list.Select(n - 1)
		}
		return m, nil
	case "u":
		m.toggleScreen(screenQueue)
		return m, nil
	case "t":
		m.status = "Theme: " + m.cycleTheme()
		return m, nil
	case "R":
		on := !m.player.ReplayGain()
		m.player.SetReplayGain(on)
		_ = SaveReplayGain(on)
		if on {
			m.status = "ReplayGain on (loudness normalization)"
		} else {
			m.status = "ReplayGain off"
		}
		return m, nil
	case "d":
		// Output device picker.
		m.loading = true
		m.status = "Loading devices…"
		return m, m.devicesCmd()
	case "x":
		// Cycle crossfade: 0 → 3s → 6s → 12s → 0.
		next := map[int]int{0: 3000, 3000: 6000, 6000: 12000, 12000: 0}[m.player.CrossfadeMS()]
		m.player.SetCrossfadeMS(next)
		_ = SaveCrossfadeMS(next)
		if next == 0 {
			m.status = "Crossfade off"
		} else {
			m.status = "Crossfade " + strconv.Itoa(next/1000) + "s"
		}
		return m, nil
	case "ctrl+g":
		on := !m.player.Gapless()
		m.player.SetGapless(on)
		_ = SaveGapless(on)
		if on {
			m.status = "Gapless on"
		} else {
			m.status = "Gapless off"
		}
		return m, nil
	case "l":
		// Show synced lyrics for the current track.
		if t, ok := m.q.Current(); ok {
			m.toggleScreen(screenLyrics)
			if m.screen == screenLyrics && (m.lyrics == nil || m.lyricsTrack != t.ID) {
				m.lyricsTrack = t.ID
				m.loading = true
				m.status = "Loading lyrics…"
				return m, m.lyricsCmd(t)
			}
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
			m.status = "Audio quality: HiFi (FLAC, falls back to MP3)"
		case 1:
			m.status = "Audio quality: High (MP3 320)"
		default:
			m.status = "Audio quality: Normal (MP3 128)"
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
	case "esc", "backspace":
		switch m.screen {
		case screenNowPlaying, screenCredits, screenQueue, screenLyrics, screenHelp, screenDevices:
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
			m.status = "Loading liked songs…"
			m.loading = true
			return m, m.favoritesCmd()
		case actPlaylists:
			m.status = "Loading playlists…"
			m.loading = true
			return m, m.playlistsCmd()
		case actCharts:
			m.status = "Loading charts…"
			m.loading = true
			return m, m.chartsCmd()
		case actFlow:
			m.status = "Loading Flow…"
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
			m.status = "Scanning for devices…"
			return m, m.discoverDevicesCmd()
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
		// Play, using the current list as the queue context. m.q holds only the
		// tracks; map the visible row to its queue index.
		idx := m.list.Index()
		ts := m.q.Tracks()
		if idx >= 0 && idx < len(ts) && ts[idx].ID == it.track.ID {
			m.q.SetIndex(idx)
		} else {
			m.q.SetIndex(m.findInQueue(it.track.ID))
		}
		return m, m.playCurrent()
	case rowArtist:
		m.status = "Loading artist…"
		m.loading = true
		return m, m.artistTopCmd(it.artist)
	case rowPodcast:
		m.status = "Loading episodes…"
		m.loading = true
		return m, m.episodesCmd(it.podcast)
	case rowEpisode:
		// Episodes are already loaded as the queue (episodeMode); play the row.
		idx := m.list.Index()
		ts := m.q.Tracks()
		if idx >= 0 && idx < len(ts) && ts[idx].ID == it.episode.ID {
			m.q.SetIndex(idx)
		} else {
			m.q.SetIndex(m.findInQueue(it.episode.ID))
		}
		return m, m.playCurrent()
	case rowDevice:
		if err := m.player.SetDevice(it.deviceID); err != nil {
			m.status = "Device error: " + err.Error()
		} else {
			_ = SaveAudioDevice(it.deviceID)
			m.status = "Output: " + it.title
		}
		m.screen = m.prevScreen
		return m, nil
	case rowPlaylist:
		m.status = "Loading playlist…"
		m.loading = true
		return m, m.playlistTracksCmd(it.playlist)
	case rowAlbum:
		m.status = "Loading album…"
		m.loading = true
		return m, m.albumTracksCmd(it.album)
	case rowPeer:
		m.loading = true
		m.status = "Connecting to " + it.title + "…"
		return m, m.remoteConnectCmd(it.peerAddr, false) // discovered: account-only
	}
	return m, nil
}

func (m *Model) findInQueue(id string) int {
	for i, t := range m.q.Tracks() {
		if t.ID == id {
			return i
		}
	}
	return 0
}

// playCurrent resolves + plays the queue's current track.
func (m *Model) playCurrent() tea.Cmd {
	t, ok := m.q.Current()
	if !ok {
		return nil
	}
	m.status = "Loading: " + t.Name
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
	// Don't stack overlay-on-overlay as the return target.
	switch m.screen {
	case screenNowPlaying, screenCredits, screenQueue, screenLyrics, screenHelp:
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

func volStatus(v float64) string {
	return "Volume " + strconv.Itoa(int(v*100+0.5)) + "%"
}
