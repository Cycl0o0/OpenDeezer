package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Fixed geometry of the bubbles list as configured in newBrowseList: the title
// bar renders one title line plus one padding row (default TitleBar style), and
// the default delegate draws two lines per item with one blank spacing row.
const (
	listHeaderRows = 2
	listItemHeight = 2
	listItemStride = listItemHeight + 1 // item + spacing
)

// handleMouse implements conservative mouse support: the wheel scrolls
// whatever the current screen scrolls, a left click selects the row under the
// cursor (a click on the already-selected row activates it), and a click on
// the footer progress bar seeks proportionally. Clicks anywhere ambiguous are
// ignored.
func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.screen == screenNoInternet { // full-screen view, no footer/list geometry
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		return m.handleWheel(true)
	case tea.MouseButtonWheelDown:
		return m.handleWheel(false)
	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		return m.handleClick(msg.X, msg.Y)
	}
	return m, nil
}

// handleWheel scrolls lists, the help overlay, the queue cursor and plain
// lyrics. Synced lyrics follow the playback position on their own.
func (m *Model) handleWheel(up bool) (tea.Model, tea.Cmd) {
	switch m.screen {
	case screenHelp:
		if up {
			m.helpOffset = max(0, m.helpOffset-1)
		} else {
			m.helpOffset++ // clamped by helpView
		}
	case screenQueue:
		if up {
			m.queueSel = max(0, m.queueSel-1)
		} else {
			m.queueSel = min(max(0, m.q.Len()-1), m.queueSel+1)
		}
	case screenLyrics:
		if up {
			m.lyricsOffset = max(0, m.lyricsOffset-1)
		} else {
			m.lyricsOffset++ // clamped by lyricsView
		}
	case screenMenu, screenList, screenDevices, screenRemote, screenPlaylistPick:
		if up {
			m.list.CursorUp()
		} else {
			m.list.CursorDown()
		}
	}
	return m, nil
}

// handleClick dispatches a left click: progress-bar seek first (its row is
// fixed at the bottom of the footer), then row selection on list-like screens.
func (m *Model) handleClick(x, y int) (tea.Model, tea.Cmd) {
	// The footer always ends with the shortcut legend, with the progress bar
	// directly above it — so the bar is the terminal's second-to-last row.
	if y == m.height-2 {
		return m, m.clickSeek(x)
	}
	switch m.screen {
	case screenMenu, screenList, screenDevices, screenRemote, screenPlaylistPick:
		return m.clickList(y)
	case screenQueue:
		return m.clickQueue(y)
	}
	return m, nil
}

// clickSeek maps an x position on the progress bar to a proportional seek.
func (m *Model) clickSeek(x int) tea.Cmd {
	if m.player == nil {
		return nil
	}
	dur := m.player.DurationMS()
	if dur <= 0 {
		return nil
	}
	// Mirror progressBar's layout: the bar spans the row up to the time display.
	times := fmt.Sprintf("%s / %s", fmtMS(m.player.PositionMS()), fmtMS(dur))
	barWidth := max(1, m.width) - lipgloss.Width(times) - 1
	if barWidth <= 0 || x < 0 || x >= barWidth {
		return nil // click on the times text or off the bar — ignore
	}
	m.player.SeekMS(dur * int64(x) / int64(barWidth))
	return nil
}

// clickList selects the list row under the cursor; a click on the selected row
// activates it (same as enter). Clicks on the title bar, spacing rows or below
// the last item are ignored.
func (m *Model) clickList(y int) (tea.Model, tea.Cmd) {
	// The list body occupies the rows above the footer; m.list is sized to that
	// area on every render, so its height is the body bound.
	if y >= m.list.Height() {
		return m, nil
	}
	rel := y - listHeaderRows
	if rel < 0 || rel%listItemStride >= listItemHeight {
		return m, nil // title bar or the blank spacing row between items
	}
	offset := rel / listItemStride
	if offset >= m.list.Paginator.PerPage {
		return m, nil
	}
	idx := m.list.Paginator.Page*m.list.Paginator.PerPage + offset
	if idx >= len(m.list.VisibleItems()) {
		return m, nil
	}
	if idx == m.list.Index() {
		return m.activate()
	}
	m.list.Select(idx)
	return m, nil
}

// clickQueue selects the queue entry under the cursor; a click on the selected
// entry jumps playback to it (same as enter on the queue screen).
func (m *Model) clickQueue(y int) (tea.Model, tea.Cmd) {
	n := m.q.Len()
	if n == 0 {
		return m, nil
	}
	bodyRows := availableBodyHeight(m.height, m.footer())
	if y >= bodyRows {
		return m, nil
	}
	start, visible := m.queueWindow(bodyRows, n)
	rel := y - 2 // title + hint header lines
	if rel < 0 || rel >= visible {
		return m, nil
	}
	idx := start + rel
	if idx >= n {
		return m, nil
	}
	if idx == m.queueSel {
		return m, m.queueJump(idx)
	}
	m.queueSel = idx
	return m, nil
}
