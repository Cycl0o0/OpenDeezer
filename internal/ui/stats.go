package ui

import (
	"fmt"
	"time"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/history"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/i18n"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// statsWindow is the look-back period for the top tracks/artists sections.
const statsWindow = 30 * 24 * time.Hour

// statsMsg carries the local listening stats for the stats screen.
type statsMsg struct {
	recent   []history.Entry
	top      []history.TrackStat
	artists  []history.ArtistStat
	totalSec int64
}

// statsCmd loads the listening history off the update loop. The store is
// internally locked, so reading it from a tea.Cmd goroutine is safe alongside
// the recording writes.
func (m *Model) statsCmd() tea.Cmd {
	store := m.hist
	return func() tea.Msg {
		if store == nil {
			var err error
			store, err = history.Default()
			if err != nil {
				return errMsg{err}
			}
		}
		since := time.Now().Add(-statsWindow)
		recent, err := store.Recent(30)
		if err != nil {
			return errMsg{err}
		}
		top, err := store.TopTracks(since, 10)
		if err != nil {
			return errMsg{err}
		}
		artists, err := store.TopArtists(since, 10)
		if err != nil {
			return errMsg{err}
		}
		total, err := store.TotalListenedSec(since)
		if err != nil {
			return errMsg{err}
		}
		return statsMsg{recent: recent, top: top, artists: artists, totalSec: total}
	}
}

// statsItems renders the stats snapshot as a sectioned list (same pattern as
// the artist page). Recent/top track rows are playable (rowHistory carries the
// Deezer track id); artist rows and the totals line are informational.
func statsItems(s statsMsg) []list.Item {
	if len(s.recent) == 0 && len(s.top) == 0 && len(s.artists) == 0 {
		return []list.Item{row{
			kind:  rowInfo,
			title: i18n.T("No listening history yet."),
			desc:  i18n.T("Play something and check back."),
		}}
	}
	items := []list.Item{row{
		kind:  rowInfo,
		title: "⏱  " + i18n.Tf("Listening time (last 30 days): %s", fmtListened(s.totalSec)),
		desc:  i18n.T("local history — never leaves this machine"),
	}}
	if len(s.recent) > 0 {
		items = append(items, sectionRow(i18n.T("Recently played")))
		for _, e := range s.recent {
			items = append(items, row{
				kind: rowHistory, histID: e.TrackID, title: e.Title,
				desc: e.Artist + " · " + time.Unix(e.StartedAt, 0).Format("2006-01-02 15:04"),
			})
		}
	}
	if len(s.top) > 0 {
		items = append(items, sectionRow(i18n.T("Top tracks (last 30 days)")))
		for i, st := range s.top {
			items = append(items, row{
				kind: rowHistory, histID: st.TrackID,
				title: fmt.Sprintf("%d. %s", i+1, st.Title),
				desc: st.Artist + " · " + i18n.Tn("%d play", "%d plays", st.Plays) +
					" · " + fmtListened(st.TotalSec),
			})
		}
	}
	if len(s.artists) > 0 {
		items = append(items, sectionRow(i18n.T("Top artists (last 30 days)")))
		for i, st := range s.artists {
			items = append(items, row{
				kind:  rowInfo,
				title: fmt.Sprintf("%d. %s", i+1, st.Artist),
				desc: i18n.Tn("%d play", "%d plays", st.Plays) +
					" · " + fmtListened(st.TotalSec),
			})
		}
	}
	return items
}

// fmtListened renders seconds listened as "3 h 12 min" (or "45 min" under an
// hour).
func fmtListened(sec int64) string {
	h := sec / 3600
	min := (sec % 3600) / 60
	if h > 0 {
		return i18n.Tf("%d h %d min", h, min)
	}
	return i18n.Tf("%d min", min)
}
