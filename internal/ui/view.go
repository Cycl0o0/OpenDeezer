package ui

import (
	"fmt"
	"strings"

	"github.com/Cycl0o0/OpenDeezer/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/internal/deezer"

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
	var body string
	switch m.screen {
	case screenSearch:
		body = m.searchView()
	case screenNowPlaying:
		body = m.nowPlayingView()
	case screenCredits:
		body = m.creditsView()
	case screenQueue:
		body = m.queueView()
	case screenLyrics:
		body = m.lyricsView()
	case screenHelp:
		body = m.helpView()
	case screenRemote:
		body = m.list.View() // device picker
	case screenRemoteInput:
		body = m.remoteEntryView()
	case screenRemoteCtl:
		body = m.remoteCtlView()
	case screenWebRemote:
		body = m.webRemoteView()
	case screenBlocked:
		return m.blockedView() // full screen, no playback footer
	default:
		body = m.list.View()
	}
	return body + "\n" + m.footer()
}

// blockedView is shown for Free accounts: OpenDeezer streams on-demand, which a
// Deezer Free plan can't do, so the app is gated behind this message.
func (m *Model) blockedView() string {
	lines := []string{
		"",
		accent.Render("OpenDeezer"),
		"",
		statusSty.Render("Sorry — your account isn't supported."),
		"",
		"OpenDeezer needs a Deezer Premium subscription to stream.",
		dim.Render("Your account: " + m.acct.Offer),
		"",
		dim.Render("Subscribe at deezer.com, then restart OpenDeezer."),
		"",
		dim.Render("q to quit"),
	}
	return padTo(lines, max(1, m.height))
}

func (m *Model) searchView() string {
	lines := []string{
		accent.Render("Search Deezer"),
		"",
		m.search.View(),
		"",
		dim.Render("enter to search · esc to go back"),
	}
	// Pad to roughly fill the list area.
	for len(lines) < max(1, m.height-footerHeight) {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// Credits text, shown on the credits screen.
const creditsAuthor = "Cycl0o0"

// Version is the app version, set from main at startup.
var Version = "1.0.0"

func (m *Model) creditsView() string {
	lines := []string{
		accent.Render("OpenDeezer") + dim.Render(" "+Version),
		dim.Render("An open source reimplementation of Deezer"),
		"",
		"by " + accent.Render(creditsAuthor),
		"",
		dim.Render("Built with:"),
		"  • Bubble Tea / Bubbles / Lip Gloss — Charm",
		"  • go-mp3 + oto — Hajime Hoshi / Ebitengine",
		"  • x/crypto/blowfish — Go authors",
		"",
		dim.Render("Audio decrypted + decoded locally. Your ARL never leaves your machine."),
		dim.Render("AGPL-3.0. Not affiliated with Deezer."),
		"",
		dim.Render("? or esc to go back"),
	}
	return padTo(lines, max(1, m.height-footerHeight))
}

func (m *Model) nowPlayingView() string {
	var meta []string
	if t, ok := m.q.Current(); ok {
		meta = []string{
			accent.Render(t.Name),
			t.ArtistLine(),
			dim.Render(t.AlbumName),
			"",
			dim.Render(m.player.State().String()),
		}
		if f := deezer.FormatLabel(m.player.Format()); f != "" {
			meta = append(meta, dim.Render("Output: "+f))
		}
	} else {
		meta = []string{dim.Render("Nothing playing.")}
	}

	cover := m.curCover
	if cover == "" {
		if !artworkSupported() {
			cover = dim.Render("(artwork needs a 256-color / truecolor terminal)")
		} else if m.playing {
			cover = dim.Render("(loading cover…)")
		} else {
			cover = dim.Render("(no cover)")
		}
	}

	info := lipgloss.JoinVertical(lipgloss.Left, meta...)
	row := lipgloss.JoinHorizontal(lipgloss.Top,
		cover, lipgloss.NewStyle().PaddingLeft(2).Render(info))
	return padTo([]string{row}, max(1, m.height-footerHeight))
}

// padTo joins lines and pads with blanks to fill n rows.
func padTo(lines []string, n int) string {
	out := strings.Join(lines, "\n")
	have := strings.Count(out, "\n") + 1
	for have < n {
		out += "\n"
		have++
	}
	return out
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
	} else if e := m.player.LastError(); e != "" {
		now = dim.Render("⏹ stopped — " + e)
	} else {
		now = dim.Render("⏹ nothing playing")
	}

	bar := m.progressBar()

	shuf := "off"
	if m.q.Shuffle() {
		shuf = "on"
	}
	help := dim.Render(fmt.Sprintf(
		"space play · n/p · z shuf:%s · r rep:%s · +/- %d%% · / search · l lyrics · u queue · h qual · ? help · q quit",
		shuf, m.q.Repeat().String(), int(m.player.Volume()*100+0.5)))

	status := ""
	if m.status != "" {
		s := m.status
		if m.loading {
			s = m.spinner.View() + s
		}
		status = statusSty.Render(s)
	}

	content := now + "\n" + bar + "\n" + help
	if status != "" {
		content = status + "\n" + content
	}
	return footerBox.Width(max(10, m.width)).Render(content)
}

func (m *Model) progressBar() string {
	pos := m.player.PositionMS()
	dur := m.player.DurationMS()
	width := max(10, m.width-20)
	filled := 0
	if dur > 0 {
		filled = int(int64(width) * pos / dur)
		if filled > width {
			filled = width
		}
	}
	bar := barFill.Render(strings.Repeat("━", filled)) +
		barEmpty.Render(strings.Repeat("━", width-filled))
	return fmt.Sprintf("%s %s / %s", bar, fmtMS(pos), fmtMS(dur))
}

func fmtMS(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	s := ms / 1000
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// queueView lists the upcoming tracks with the current one highlighted.
func (m *Model) queueView() string {
	ts := m.q.Tracks()
	if len(ts) == 0 {
		return padTo([]string{dim.Render("Queue is empty.")}, max(1, m.height-footerHeight))
	}
	cur := m.q.Index()
	rows := max(1, m.height-footerHeight-2)
	lines := []string{accent.Render(fmt.Sprintf("Queue (%d tracks)", len(ts))), ""}
	// Window around the current track so long queues stay readable.
	start := 0
	if cur > rows/2 {
		start = cur - rows/2
	}
	for i := start; i < len(ts) && len(lines) < rows; i++ {
		t := ts[i]
		marker := "  "
		line := fmt.Sprintf("%2d. %s — %s", i+1, t.Name, t.ArtistLine())
		if i == cur {
			marker = accent.Render("▶ ")
			line = accent.Render(line)
		} else {
			line = dim.Render(line)
		}
		lines = append(lines, marker+line)
	}
	return padTo(lines, max(1, m.height-footerHeight))
}

// lyricsView shows lyrics; synced lyrics auto-scroll with playback position and
// highlight the current line.
func (m *Model) lyricsView() string {
	t, ok := m.q.Current()
	if !ok {
		return padTo([]string{dim.Render("Nothing playing.")}, max(1, m.height-footerHeight))
	}
	header := accent.Render(t.Name) + dim.Render(" — "+t.ArtistLine())
	if m.lyrics == nil {
		return padTo([]string{header, "", dim.Render("(loading lyrics…)")}, max(1, m.height-footerHeight))
	}
	rows := max(3, m.height-footerHeight-2)

	if m.lyrics.IsSynced() {
		pos := m.player.PositionMS()
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
		return padTo(lines, max(1, m.height-footerHeight))
	}

	if m.lyrics.Plain == "" {
		return padTo([]string{header, "", dim.Render("(no lyrics available)")}, max(1, m.height-footerHeight))
	}
	lines := append([]string{header, ""}, strings.Split(m.lyrics.Plain, "\n")...)
	return padTo(lines, max(1, m.height-footerHeight))
}

// helpView lists every keybinding.
func (m *Model) helpView() string {
	binds := [][2]string{
		{"↑/↓ or j/k", "move selection"},
		{"g / G", "jump to top / bottom"},
		{"enter", "play track / open album·artist·playlist / menu action"},
		{"/", "search (tracks, artists, albums, playlists)"},
		{"space", "play / pause"},
		{"n / p", "next / previous track"},
		{"f", "like the current track"},
		{"← / →", "seek −10s / +10s"},
		{"+ / -", "volume up / down"},
		{"z", "toggle shuffle"},
		{"r", "cycle repeat (off → all → one)"},
		{"h", "cycle quality (Normal → High → HiFi)"},
		{"R", "toggle ReplayGain (loudness normalization)"},
		{"d", "choose output device"},
		{"x", "cycle crossfade (off/3/6/12s)"},
		{"ctrl+g", "toggle gapless"},
		{"l", "lyrics (synced when available)"},
		{"u", "queue view"},
		{"c", "now playing / cover"},
		{"t", "cycle theme"},
		{"s", "stop"},
		{"i", "about / credits"},
		{"? ", "this help"},
		{"esc", "back"},
		{"q", "quit"},
	}
	lines := []string{accent.Render("Keybindings"), ""}
	for _, b := range binds {
		lines = append(lines, "  "+accent.Render(fmt.Sprintf("%-12s", b[0]))+dim.Render(b[1]))
	}
	lines = append(lines, "", dim.Render("? or esc to go back"))
	return padTo(lines, max(1, m.height-footerHeight))
}
