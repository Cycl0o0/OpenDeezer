package ui

import (
	"fmt"
	"strings"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/i18n"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/version"

	"github.com/charmbracelet/lipgloss"
)

var (
	accent    = lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true)
	dim       = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	barFill   = lipgloss.NewStyle().Foreground(lipgloss.Color("213"))
	barEmpty  = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	footerBox = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), true, false, false, false).
			BorderForeground(lipgloss.Color("238"))
	statusSty = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
)

// View renders the whole screen.
func (m *Model) View() string {
	if !m.ready {
		return "starting…"
	}
	if m.screen == screenNoInternet {
		return m.noInternetView() // full screen, no playback footer
	}
	footer := m.footer()
	if m.height <= 1 {
		return footer
	}
	bodyRows := availableBodyHeight(m.height, footer)
	// Footer height changes with status, sleep-timer, and update notices. Size
	// the list from the rendered footer on every frame so those lines never push
	// content below the terminal viewport.
	m.list.SetSize(max(1, m.width), bodyRows)

	var body string
	switch m.screen {
	case screenSearch:
		body = m.searchView(bodyRows)
	case screenNowPlaying:
		body = m.nowPlayingView(bodyRows)
	case screenCredits:
		body = m.creditsView(bodyRows)
	case screenQueue:
		body = m.queueView(bodyRows)
	case screenLyrics:
		body = m.lyricsView(bodyRows)
	case screenHelp:
		body = m.helpView(bodyRows)
	case screenRemote:
		body = m.list.View() // device picker
	case screenRemoteInput:
		body = m.remoteEntryView(bodyRows)
	case screenRemoteCtl:
		body = m.remoteCtlView(bodyRows)
	case screenWebRemote:
		body = m.webRemoteView(bodyRows)
	case screenPlaylistPrompt:
		body = m.playlistPromptView(bodyRows)
	default:
		body = m.list.View() // incl. the device / add-to-playlist pickers
	}
	body = lipgloss.NewStyle().MaxWidth(max(1, m.width)).MaxHeight(bodyRows).Render(body)
	return body + "\n" + footer
}

func availableBodyHeight(total int, footer string) int {
	return max(1, total-lipgloss.Height(footer))
}

// noInternetView is shown when a login or browse call fails at the transport
// level (DNS/refused/unreachable/timeout). The session is not dropped — the user
// stays "logged in" and just retries, which distinguishes a passing outage from
// an expired ARL (which instead prompts a re-login in the footer).
func (m *Model) noInternetView() string {
	hint := i18n.T("r to retry")
	if m.loading {
		hint = i18n.T("Reconnecting…")
	}
	lines := []string{
		"",
		accent.Render("OpenDeezer"),
		"",
		statusSty.Render(i18n.T("No internet connection")),
		"",
		i18n.T("Check your connection and try again."),
		"",
		dim.Render(hint + "  ·  " + i18n.T("q to quit")),
	}
	view := padTo(lines, max(1, m.height))
	return lipgloss.NewStyle().MaxWidth(max(1, m.width)).MaxHeight(max(1, m.height)).Render(view)
}

func (m *Model) searchView(rows int) string {
	// Refresh the placeholder each render so a live locale change is reflected.
	m.search.Placeholder = i18n.T("Search Deezer…")
	title := i18n.T("Search Deezer")
	if m.searchPodcast {
		title = i18n.T("Podcasts")
	}
	lines := []string{
		accent.Render(title),
		"",
		m.search.View(),
		"",
		dim.Render(i18n.T("enter to search · esc to go back")),
	}
	// Pad to roughly fill the list area.
	for len(lines) < rows {
		lines = append(lines, "")
	}
	return padTo(lines, rows)
}

// Credits text, shown on the credits screen.
const creditsAuthor = "Cycl0o0"

// Version is the app version, set from main at startup (defaults to the
// release number so library users get the right value without main).
var Version = version.Number

func (m *Model) creditsView(rows int) string {
	lines := []string{
		accent.Render("OpenDeezer") + dim.Render(" "+Version),
		dim.Render(i18n.T("An open source reimplementation of Deezer")),
		"",
		i18n.T("by") + " " + accent.Render(creditsAuthor),
		"",
		dim.Render(i18n.T("Built with:")),
		"  • Bubble Tea / Bubbles / Lip Gloss — Charm",
		"  • go-mp3 + oto — Hajime Hoshi / Ebitengine",
		"  • x/crypto/blowfish — Go authors",
		"",
		dim.Render(i18n.T("Audio decrypted + decoded locally. Your ARL never leaves your machine.")),
		dim.Render(i18n.T("AGPL-3.0. Not affiliated with Deezer.")),
		"",
	}
	switch {
	case m.updateChecking:
		lines = append(lines, statusSty.Render(i18n.T("Checking for updates…")))
	case m.updateInfo.HasUpdate:
		lines = append(lines, statusSty.Render(i18n.Tf(
			"⬆ v%s available — U to open · u to re-check", m.updateInfo.Latest)))
	case m.updateChecked:
		lines = append(lines, dim.Render(i18n.T("You're on the latest version. (u to check again)")))
	default:
		lines = append(lines, dim.Render(i18n.T("u to check for updates")))
	}
	lines = append(lines, "", dim.Render(i18n.T("? or esc to go back")))
	return padTo(lines, rows)
}

func (m *Model) nowPlayingView(rows int) string {
	var meta []string
	if t, ok := m.q.Current(); ok {
		meta = []string{
			accent.Render(t.Name),
			t.ArtistLine(),
			dim.Render(t.AlbumName),
			"",
			dim.Render(i18n.T(m.player.State().String())),
		}
		if f := deezer.FormatLabel(m.player.Format()); f != "" {
			meta = append(meta, dim.Render(i18n.Tf("Output: %s", f)))
		}
		if m.player.IsPreview() {
			meta = append(meta, dim.Render(i18n.T("Preview · 30-second clip (free account)")))
		}
	} else {
		meta = []string{dim.Render(i18n.T("Nothing playing."))}
	}

	cover := m.curCover
	if cover == "" {
		if !artworkSupported() {
			cover = dim.Render(i18n.T("(artwork needs a 256-color / truecolor terminal)"))
		} else if m.playing {
			cover = dim.Render(i18n.T("(loading cover…)"))
		} else {
			cover = dim.Render(i18n.T("(no cover)"))
		}
	}

	info := lipgloss.JoinVertical(lipgloss.Left, meta...)
	row := lipgloss.JoinHorizontal(lipgloss.Top,
		cover, lipgloss.NewStyle().PaddingLeft(2).Render(info))
	return padTo([]string{row}, rows)
}

// padTo joins lines and pads with blanks to fill n rows.
func padTo(lines []string, n int) string {
	if n < 1 {
		n = 1
	}
	rendered := strings.Split(strings.Join(lines, "\n"), "\n")
	if len(rendered) > n {
		rendered = rendered[:n]
	}
	for len(rendered) < n {
		rendered = append(rendered, "")
	}
	return strings.Join(rendered, "\n")
}

func (m *Model) footer() string {
	st := m.player.State()
	var now string
	cur, hasCur := m.q.Current()
	if hasCur && (m.playing || st == audio.Playing || st == audio.Paused) {
		t := cur
		icon := "▶"
		switch st {
		case audio.Paused:
			icon = "⏸"
		case audio.Loading:
			icon = "…"
		}
		now = fmt.Sprintf("%s %s %s",
			icon, accent.Render(t.Name), dim.Render("· "+t.ArtistLine()))
		if f := deezer.FormatLabel(m.player.Format()); f != "" {
			now += dim.Render("  [" + f + "]")
		}
		if m.player.IsPreview() {
			now += dim.Render("  " + i18n.T("· preview"))
		}
	} else if e := m.player.LastError(); e != "" {
		now = dim.Render("⏹ " + i18n.Tf("stopped — %s", e))
	} else {
		now = dim.Render("⏹ " + i18n.T("nothing playing"))
	}

	bar := m.progressBar()

	shuf := i18n.T("off")
	if m.q.Shuffle() {
		shuf = i18n.T("on")
	}
	help := dim.Render(m.footerHelp(
		shuf, i18n.T(m.q.Repeat().String()), int(m.player.Volume()*100+0.5)))

	status := ""
	if m.status != "" {
		s := m.status
		if m.loading {
			s = m.spinner.View() + s
		}
		status = statusSty.Render(s)
	}

	content := now + "\n" + bar + "\n" + help
	if m.player.SleepActive() {
		content = dim.Render("☾ "+sleepStatus(m.player)) + "\n" + content
	}
	if status != "" {
		content = status + "\n" + content
	}
	if m.updateInfo.HasUpdate && !m.updateDismissed {
		notice := statusSty.Render(i18n.Tf(
			"⬆ v%s available — U to open · X to dismiss", m.updateInfo.Latest))
		content = notice + "\n" + content
	}
	width := max(1, m.width)
	maxHeight := max(1, m.height-1) // reserve at least one row for body content
	return footerBox.Width(width).MaxWidth(width).MaxHeight(maxHeight).Render(content)
}

// footerHelp keeps the primary controls discoverable without letting the full
// shortcut legend run off narrow terminals. The complete legend remains on
// wide screens and on the dedicated ? help screen.
func (m *Model) footerHelp(shuffle, repeat string, volume int) string {
	switch {
	case m.width >= 124:
		return i18n.Tf(
			"space play · n/p · z shuf:%s · r rep:%s · +/- %d%% · m radio · / search · ^f filter · l lyrics · u queue · h qual · ? help · q quit",
			shuffle, repeat, volume)
	case m.width >= 62:
		return "space " + i18n.T("play / pause") + " · n/p · +/- · / " +
			i18n.T("Search") + " · ? · q " + i18n.T("quit")
	case m.width >= 38:
		return "space · n/p · / " + i18n.T("Search") + " · ? · q"
	default:
		return "space · / · ? · q"
	}
}

func (m *Model) progressBar() string {
	pos := m.player.PositionMS()
	dur := m.player.DurationMS()
	width := max(1, m.width)
	times := fmt.Sprintf("%s / %s", fmtMS(pos), fmtMS(dur))
	timesWidth := lipgloss.Width(times)
	if timesWidth >= width {
		return lipgloss.NewStyle().MaxWidth(width).Render(times)
	}
	barWidth := width - timesWidth - 1
	filled := 0
	if dur > 0 {
		filled = int(int64(barWidth) * pos / dur)
		if filled < 0 {
			filled = 0
		}
		if filled > barWidth {
			filled = barWidth
		}
	}
	bar := barFill.Render(strings.Repeat("━", filled)) +
		barEmpty.Render(strings.Repeat("━", barWidth-filled))
	return bar + " " + times
}

func fmtMS(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	s := ms / 1000
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// queueWindow computes the queue view's scroll window: the first entry shown
// and how many entry rows fit under the two header lines. Centered on the
// cursor and clamped, so mouse clicks can map body rows back to entries.
func (m *Model) queueWindow(bodyRows, n int) (start, visible int) {
	visible = max(1, bodyRows-2)
	sel := min(max(0, m.queueSel), n-1)
	start = sel - visible/2
	start = min(start, n-visible)
	start = max(start, 0)
	return start, visible
}

// queueView lists the queued tracks: ▶ marks the playing one, › the cursor.
// Rows are edited in place (enter jump, x remove, J/K move — see handleKey).
func (m *Model) queueView(bodyRows int) string {
	ts := m.q.Tracks()
	if len(ts) == 0 {
		return padTo([]string{dim.Render(i18n.T("Queue is empty."))}, bodyRows)
	}
	m.queueSel = min(max(0, m.queueSel), len(ts)-1) // clamp after external edits
	cur := m.q.Index()
	start, visible := m.queueWindow(bodyRows, len(ts))
	lines := []string{
		accent.Render(i18n.Tn("Queue (%d track)", "Queue (%d tracks)", len(ts))),
		dim.Render(i18n.T("enter play · x remove · J/K move · m radio · esc back")),
	}
	for i := start; i < len(ts) && i < start+visible; i++ {
		t := ts[i]
		marker := "  "
		line := fmt.Sprintf("%2d. %s — %s", i+1, t.Name, t.ArtistLine())
		switch {
		case i == cur:
			marker = accent.Render("▶ ")
			line = accent.Render(line)
		case i == m.queueSel:
			marker = accent.Render("› ") // cursor row stays unstyled = brighter than dim
		default:
			line = dim.Render(line)
		}
		lines = append(lines, marker+line)
	}
	return padTo(lines, bodyRows)
}

// playlistPromptView is the playlist-title input (create / rename).
func (m *Model) playlistPromptView(rows int) string {
	title := i18n.T("New playlist")
	if m.plPrompt == plRename {
		title = i18n.T("Rename playlist")
	}
	lines := []string{
		accent.Render("≡  " + title),
		"",
		i18n.T("Playlist title:"),
		"  " + m.search.View(),
		"",
		dim.Render(i18n.T("enter save · esc cancel")),
	}
	if m.status != "" {
		lines = append(lines, "", statusSty.Render(m.status))
	}
	return padTo(lines, rows)
}

// lyricsView shows lyrics; synced lyrics auto-scroll with playback position and
// highlight the current line.
func (m *Model) lyricsView(bodyRows int) string {
	// When driving a remote peer, show its now-playing track and position (the
	// local queue/player are unrelated to what the peer is playing).
	t, ok := m.q.Current()
	pos := m.player.PositionMS()
	if m.remote != nil && m.remoteState.Track != nil {
		t, ok = remoteTrack(m.remoteState.Track), true
		pos = m.remoteState.PositionMS
	}
	if !ok {
		return padTo([]string{dim.Render(i18n.T("Nothing playing."))}, bodyRows)
	}
	header := accent.Render(t.Name) + dim.Render(" — "+t.ArtistLine())
	if m.lyrics == nil {
		return padTo([]string{header, "", dim.Render(i18n.T("(loading lyrics…)"))}, bodyRows)
	}
	rows := max(3, bodyRows-2)

	if m.lyrics.IsSynced() {
		active := 0
		for i, ln := range m.lyrics.Synced {
			if ln.TimeMS <= pos {
				active = i
			}
		}
		lines := []string{header, ""}
		start := 0
		if active > rows/2 {
			start = active - rows/2
		}
		for i := start; i < len(m.lyrics.Synced) && len(lines) < rows+2; i++ {
			ln := m.lyrics.Synced[i].Text
			if i == active {
				lines = append(lines, accent.Render(ln))
			} else {
				lines = append(lines, dim.Render(ln))
			}
		}
		return padTo(lines, bodyRows)
	}

	if m.lyrics.Plain == "" {
		return padTo([]string{header, "", dim.Render(i18n.T("(no lyrics available)"))}, bodyRows)
	}
	// Plain lyrics scroll manually (mouse wheel); synced lyrics above follow the
	// playback position on their own, so the offset only applies here.
	body := strings.Split(m.lyrics.Plain, "\n")
	m.lyricsOffset = min(max(0, m.lyricsOffset), max(0, len(body)-1))
	lines := append([]string{header, ""}, body[m.lyricsOffset:]...)
	return padTo(lines, bodyRows)
}

// helpView lists every keybinding.
func (m *Model) helpView(rows int) string {
	binds := [][2]string{
		{"↑/↓ or j/k", "move selection"},
		{"g / G", "jump to top / bottom"},
		{"enter", "play track / open album·artist·playlist / menu action"},
		{"/", "search (tracks, artists, albums, playlists)"},
		{"ctrl+f", "filter the current list (esc clears)"},
		{"space", "play / pause"},
		{"n / p", "next / previous track"},
		{"n / e", "browse lists: play selected next / add to queue"},
		{"m", "start radio from the selected track / artist"},
		{"f", "like / unlike the current or selected track"},
		{"a", "add the selected track to a playlist"},
		{"N / R / X", "playlists: new / rename / delete"},
		{"D", "download the selected track (paid plans)"},
		{"A", "toggle ads / play-reporting (free accounts)"},
		{"← / →", "seek −10s / +10s"},
		{"+ / -", "volume up / down"},
		{"z", "toggle shuffle"},
		{"r", "cycle repeat (off → all → one)"},
		{"h", "cycle quality (Normal → High → HiFi)"},
		{"R", "toggle ReplayGain (loudness normalization)"},
		{"E", "toggle equalizer · ctrl+e cycle preset"},
		{"M", "toggle mono downmix"},
		{"d", "choose output device"},
		{"x", "cycle crossfade (off/3/6/12s)"},
		{"ctrl+g", "toggle gapless"},
		{"l", "lyrics (synced when available)"},
		{"u", "queue view"},
		{"enter/x/J/K", "queue: jump / remove / move down / up"},
		{"mouse", "click select · click again play · wheel scroll · click bar seek"},
		{"c", "now playing / cover"},
		{"t", "cycle theme"},
		{"T", "sleep timer (off → 15/30/45/60m → end of track)"},
		{"s", "stop"},
		{"i", "about / credits (u there: check for updates)"},
		{"U", "open available update in browser · X dismiss notice"},
		{"? ", "this help"},
		{"esc", "back"},
		{"q", "quit"},
	}
	bindingLines := make([]string, 0, len(binds))
	for _, b := range binds {
		bindingLines = append(bindingLines,
			"  "+accent.Render(fmt.Sprintf("%-12s", b[0]))+dim.Render(i18n.T(b[1])))
	}
	visible := max(1, rows-3) // title + spacer + scroll/back hint
	maxOffset := max(0, len(bindingLines)-visible)
	m.helpOffset = min(max(0, m.helpOffset), maxOffset)
	end := min(len(bindingLines), m.helpOffset+visible)

	lines := []string{accent.Render(i18n.T("Keybindings")), ""}
	lines = append(lines, bindingLines[m.helpOffset:end]...)
	if maxOffset > 0 {
		lines = append(lines, dim.Render(fmt.Sprintf(
			"↑/↓  %d-%d/%d  ·  esc %s", m.helpOffset+1, end, len(bindingLines), i18n.T("back"))))
	} else {
		lines = append(lines, dim.Render(i18n.T("? or esc to go back")))
	}
	return padTo(lines, rows)
}
