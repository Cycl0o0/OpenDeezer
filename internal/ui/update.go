package ui

import (
	"math/rand"
	"strconv"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

const footerHeight = 4

// menuRows is the home screen.
func menuRows() []list.Item {
	return []list.Item{
		row{kind: rowMenu, title: "❤  Liked Songs", desc: "your favorite tracks", action: actLiked},
		row{kind: rowMenu, title: "≡  My Playlists", desc: "playlists you own", action: actPlaylists},
		row{kind: rowMenu, title: "🔍 Search", desc: "tracks, albums, playlists", action: actSearch},
	}
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
			m.status = "Login failed: " + msg.err.Error() + "  (set DEEZER_ARL or ~/.config/deezertui/arl.txt)"
			return m, nil
		}
		m.screen = screenMenu
		m.status = "Logged in."
		m.list.Title = "DeezerTUI"
		m.list.SetItems(menuRows())
		return m, nil

	case tracksMsg:
		m.loading = false
		items := make([]list.Item, len(msg.tracks))
		for i, t := range msg.tracks {
			items[i] = trackRow(t)
		}
		m.queue = msg.tracks
		m.history = nil
		m.list.Title = msg.title
		m.list.SetItems(items)
		m.list.ResetSelected()
		m.screen = screenList
		m.status = ""
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
		for _, a := range msg.results.Albums {
			items = append(items, albumRow(a))
		}
		for _, p := range msg.results.Playlists {
			items = append(items, playlistRow(p))
		}
		// Keep tracks as the playable queue context.
		m.queue = msg.results.Tracks
		m.history = nil
		m.list.Title = "Search results"
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
		// Reset + fetch artwork for the new track.
		m.curImg = nil
		m.curCover = ""
		m.curImgTrack = msg.track.ID
		if artworkSupported() && msg.track.ArtworkURL != "" {
			return m, m.coverCmd(msg.track.ID, msg.track.ArtworkURL)
		}
		return m, nil

	case artMsg:
		if msg.img != nil && msg.trackID == m.curImgTrack {
			m.curImg = msg.img
			m.refreshCover()
		}
		return m, nil

	case trackFinishedMsg:
		// Advance the queue, then keep waiting for the next finish.
		return m, tea.Batch(m.advance(), m.waitFinish())

	case errMsg:
		m.loading = false
		m.status = "Error: " + msg.err.Error()
		return m, nil

	case tickMsg:
		return m, tickCmd()

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
			m.status = "Searching…"
			m.loading = true
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
		m.player.Stop()
		return m, tea.Quit
	case " ":
		m.player.TogglePause()
		return m, nil
	case "n":
		return m, m.next()
	case "p":
		return m, m.prev()
	case "r":
		m.repeat = (m.repeat + 1) % 3
		return m, nil
	case "z":
		m.shuffle = !m.shuffle
		if m.shuffle {
			m.status = "Shuffle on"
		} else {
			m.status = "Shuffle off"
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
		// Toggle the now-playing / cover screen.
		if m.screen == screenNowPlaying {
			m.screen = m.prevScreen
		} else {
			m.prevScreen = m.screen
			m.screen = screenNowPlaying
		}
		return m, nil
	case "?":
		if m.screen == screenCredits {
			m.screen = m.prevScreen
		} else {
			m.prevScreen = m.screen
			m.screen = screenCredits
		}
		return m, nil
	case "esc", "backspace":
		switch m.screen {
		case screenNowPlaying, screenCredits:
			m.screen = m.prevScreen
		case screenList:
			m.screen = screenMenu
			m.list.Title = "DeezerTUI"
			m.list.SetItems(menuRows())
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
		case actSearch:
			m.search.SetValue("")
			m.search.Focus()
			m.screen = screenSearch
			return m, nil
		}
	case rowTrack:
		// Play, using the current list as the queue context.
		idx := m.list.Index()
		// queue holds only tracks; map the visible index to the queue index
		// when the list is tracks-only (liked/playlist/album/search-tracks).
		if idx >= 0 && idx < len(m.queue) && m.queue[idx].ID == it.track.ID {
			m.qIndex = idx
		} else {
			m.qIndex = m.findInQueue(it.track.ID)
		}
		return m, m.playCurrent()
	case rowPlaylist:
		m.status = "Loading playlist…"
		m.loading = true
		return m, m.playlistTracksCmd(it.playlist)
	case rowAlbum:
		m.status = "Loading album…"
		m.loading = true
		return m, m.albumTracksCmd(it.album)
	}
	return m, nil
}

func (m *Model) findInQueue(id string) int {
	for i, t := range m.queue {
		if t.ID == id {
			return i
		}
	}
	return 0
}

// playCurrent resolves + plays m.queue[m.qIndex].
func (m *Model) playCurrent() tea.Cmd {
	if m.qIndex < 0 || m.qIndex >= len(m.queue) {
		return nil
	}
	t := m.queue[m.qIndex]
	m.status = "Loading: " + t.Name
	m.loading = true
	return m.streamCmd(t)
}

func (m *Model) next() tea.Cmd {
	if len(m.queue) == 0 {
		return nil
	}
	m.history = append(m.history, m.qIndex)
	if m.shuffle && len(m.queue) > 1 {
		// Pick a different random track.
		next := m.qIndex
		for next == m.qIndex {
			next = rand.Intn(len(m.queue))
		}
		m.qIndex = next
	} else if m.qIndex+1 < len(m.queue) {
		m.qIndex++
	} else if m.repeat == repeatAll {
		m.qIndex = 0
	} else {
		m.history = m.history[:len(m.history)-1] // nothing to advance to
		return nil
	}
	return m.playCurrent()
}

func (m *Model) prev() tea.Cmd {
	if len(m.queue) == 0 {
		return nil
	}
	if n := len(m.history); n > 0 {
		m.qIndex = m.history[n-1]
		m.history = m.history[:n-1]
	} else if m.qIndex > 0 {
		m.qIndex--
	}
	return m.playCurrent()
}

// advance is called when a track finishes naturally.
func (m *Model) advance() tea.Cmd {
	switch m.repeat {
	case repeatOne:
		return m.playCurrent()
	default:
		if cmd := m.next(); cmd != nil {
			return cmd
		}
		m.playing = false
		return nil
	}
}

func volStatus(v float64) string {
	return "Volume " + strconv.Itoa(int(v*100+0.5)) + "%"
}
