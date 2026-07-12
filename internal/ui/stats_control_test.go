package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/history"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// syncSend runs control messages straight through Update, so the command hooks
// exercise the exact update-loop path they use in production (the reply channel
// is buffered, so the loop's reply never blocks even with a synchronous send).
func syncSend(m *Model) func(tea.Msg) {
	return func(msg tea.Msg) { _, _ = m.Update(msg) }
}

// ---- control queue hooks ----

func TestControlQueueHooksThreadThroughUpdateLoop(t *testing.T) {
	m := interactionModel(screenList)
	m.q.Set(testTracks(4), 0)
	cmds := buildControlCommands(syncSend(m), nil, nil)

	if err := cmds.QueueJump(2); err != nil {
		t.Fatalf("QueueJump(2): %v", err)
	}
	if m.q.Index() != 2 {
		t.Fatalf("jump: index = %d, want 2", m.q.Index())
	}
	if err := cmds.QueueJump(9); err == nil {
		t.Fatal("QueueJump out of range must fail")
	}

	if err := cmds.QueueRemove(m.q.Index()); err == nil {
		t.Fatal("QueueRemove must refuse the playing row (like the queue view)")
	}
	if err := cmds.QueueRemove(0); err != nil {
		t.Fatalf("QueueRemove(0): %v", err)
	}
	if m.q.Len() != 3 || m.q.Tracks()[0].ID != "b" {
		t.Fatalf("remove: len=%d first=%q", m.q.Len(), m.q.Tracks()[0].ID)
	}
	if err := cmds.QueueRemove(7); err == nil {
		t.Fatal("QueueRemove out of range must fail")
	}

	if err := cmds.QueueMove(0, 2); err != nil {
		t.Fatalf("QueueMove(0,2): %v", err)
	}
	if m.q.Tracks()[2].ID != "b" {
		t.Fatalf("move: tracks=%v", m.q.Tracks())
	}
	if err := cmds.QueueMove(0, 9); err == nil {
		t.Fatal("QueueMove out of range must fail")
	}
	if err := cmds.QueueMove(1, 1); err != nil {
		t.Fatalf("QueueMove to the same slot is a no-op, not an error: %v", err)
	}

	// The fetch-based hooks are guarded when no client is wired.
	if err := cmds.QueueAdd("123", true); err == nil {
		t.Fatal("QueueAdd without a client must fail cleanly")
	}
	if err := cmds.PlayAlbum("123"); err == nil {
		t.Fatal("PlayAlbum without a client must fail cleanly")
	}
}

func TestControlQueueAddMessageInsertsTrack(t *testing.T) {
	m := interactionModel(screenList)
	m.q.Set(testTracks(2), 0)
	send := syncSend(m)

	// "play next" inserts right after the current row (what the hook sends
	// after its off-loop client.Track fetch).
	reply := make(chan error, 1)
	send(controlQueueEditMsg{kind: "add", track: deezer.Track{ID: "x", Name: "X"}, next: true, reply: reply})
	if err := <-reply; err != nil {
		t.Fatalf("add next: %v", err)
	}
	if m.q.Len() != 3 || m.q.Tracks()[1].ID != "x" {
		t.Fatalf("add next: tracks=%v", m.q.Tracks())
	}

	// plain add appends.
	reply = make(chan error, 1)
	send(controlQueueEditMsg{kind: "add", track: deezer.Track{ID: "y", Name: "Y"}, next: false, reply: reply})
	if err := <-reply; err != nil {
		t.Fatalf("append: %v", err)
	}
	if m.q.Tracks()[m.q.Len()-1].ID != "y" {
		t.Fatalf("append: tracks=%v", m.q.Tracks())
	}

	// Mixing a music track into a podcast-episode queue is refused (mirrors the
	// n/e browse keys, which exclude episode queues).
	m.episodeMode = true
	reply = make(chan error, 1)
	send(controlQueueEditMsg{kind: "add", track: deezer.Track{ID: "z", Name: "Z"}, next: false, reply: reply})
	if err := <-reply; err == nil {
		t.Fatal("adding a track to an episode queue must fail")
	}
}

func TestControlHistoryRecentServesStore(t *testing.T) {
	st := history.New(filepath.Join(t.TempDir(), "history.jsonl"))
	if err := st.Record(history.Entry{TrackID: "42", Title: "Song", Artist: "Artist", DurationPlayedSec: 60}); err != nil {
		t.Fatal(err)
	}
	m := interactionModel(screenList)
	cmds := buildControlCommands(syncSend(m), nil, st)
	raw, err := cmds.HistoryRecent(5)
	if err != nil {
		t.Fatalf("HistoryRecent: %v", err)
	}
	if !strings.Contains(string(raw), `"trackId":"42"`) {
		t.Fatalf("HistoryRecent payload = %s", raw)
	}

	// No store wired: an empty JSON array, not an error.
	cmds = buildControlCommands(syncSend(m), nil, nil)
	raw, err = cmds.HistoryRecent(5)
	if err != nil || string(raw) != "[]" {
		t.Fatalf("HistoryRecent without a store = %s, %v", raw, err)
	}
}

// ---- history recording ----

func TestHistoryFlushRecordsOutgoingTrackOnce(t *testing.T) {
	m := interactionModel(screenList)
	m.hist = history.New(filepath.Join(t.TempDir(), "history.jsonl"))
	tr := deezer.Track{ID: "1", Name: "Song", Artists: []deezer.Artist{{ID: "9", Name: "Artist"}},
		AlbumName: "Album", DurationMS: 180000}
	m.histStartSession(tr)
	m.histPosMS = 42000

	cmd := m.historyFlushCmd()
	if cmd == nil {
		t.Fatal("an active session must produce a record command")
	}
	if m.histTrack.ID != "" {
		t.Fatal("flush must clear the session on the update loop")
	}
	_ = cmd() // run the disk write (normally a tea.Cmd goroutine)

	es, err := m.hist.Recent(5)
	if err != nil || len(es) != 1 {
		t.Fatalf("Recent = %v, %v", es, err)
	}
	e := es[0]
	if e.TrackID != "1" || e.Title != "Song" || e.Artist != "Artist" || e.DurationPlayedSec != 42 {
		t.Fatalf("entry = %+v", e)
	}

	// Duplicate guard: the session is gone, so a second flush is a no-op
	// (a pause/resume never re-records).
	if m.historyFlushCmd() != nil {
		t.Fatal("flushing twice must be a no-op")
	}
}

func TestTrackChangeRecordsOutgoingAndStartsNewSession(t *testing.T) {
	m := interactionModel(screenList)
	m.hist = history.New(filepath.Join(t.TempDir(), "history.jsonl"))
	a := deezer.Track{ID: "a", Name: "A", DurationMS: 100000}
	b := deezer.Track{ID: "b", Name: "B", DurationMS: 100000}
	m.histStartSession(a)
	m.histPosMS = 30000

	cmd := m.onTrackChanged(b) // no artwork URL → the returned cmd is the flush
	if cmd == nil {
		t.Fatal("track change with an active session must record it")
	}
	_ = cmd()
	if m.histTrack.ID != "b" {
		t.Fatalf("new session track = %q, want b", m.histTrack.ID)
	}
	es, _ := m.hist.Recent(5)
	if len(es) != 1 || es[0].TrackID != "a" || es[0].DurationPlayedSec != 30 {
		t.Fatalf("recorded = %+v", es)
	}

	// A natural finish records the full duration.
	m.histPosMS = 55000
	m.histMarkFull()
	cmd = m.historyFlushCmd()
	if cmd == nil {
		t.Fatal("expected a flush for the finished track")
	}
	_ = cmd()
	es, _ = m.hist.Recent(1)
	if es[0].TrackID != "b" || es[0].DurationPlayedSec != 100 {
		t.Fatalf("full-play entry = %+v", es[0])
	}
}

func TestEpisodeSessionsAreNotRecorded(t *testing.T) {
	m := interactionModel(screenList)
	m.hist = history.New(filepath.Join(t.TempDir(), "history.jsonl"))
	m.episodeMode = true
	m.histStartSession(deezer.Track{ID: "ep", Name: "Episode", DurationMS: 600000})
	m.histPosMS = 60000
	if m.historyFlushCmd() != nil {
		t.Fatal("podcast episodes must not be recorded")
	}
}

// ---- stats screen ----

func TestStatsScreenBuildsSectionedListAndPlaysByID(t *testing.T) {
	m := interactionModel(screenMenu)
	now := time.Now().Unix()
	_, _ = m.Update(statsMsg{
		recent:   []history.Entry{{TrackID: "7", Title: "Recent Song", Artist: "Artist", StartedAt: now, DurationPlayedSec: 60}},
		top:      []history.TrackStat{{TrackID: "7", Title: "Recent Song", Artist: "Artist", Plays: 3, TotalSec: 180}},
		artists:  []history.ArtistStat{{Artist: "Artist", Plays: 3, TotalSec: 180}},
		totalSec: 3720,
	})
	if m.screen != screenList {
		t.Fatalf("stats should open as a list screen, got %v", m.screen)
	}
	items := m.list.Items()
	var sections, playable int
	firstPlayable := -1
	for i, it := range items {
		r := it.(row)
		if r.kind == rowSection {
			sections++
		}
		if r.kind == rowHistory {
			playable++
			if firstPlayable < 0 {
				firstPlayable = i
			}
			if r.histID != "7" {
				t.Fatalf("history row id = %q", r.histID)
			}
		}
	}
	if sections != 3 || playable != 2 {
		t.Fatalf("sections=%d playable=%d, want 3 sections + 2 playable rows", sections, playable)
	}
	if got := items[0].(row); got.kind != rowInfo || !strings.Contains(got.title, "1 h 2 min") {
		t.Fatalf("totals row = %+v", got)
	}

	// Enter on a recent/top row starts a play-by-id fetch.
	m.list.Select(firstPlayable)
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !m.loading {
		t.Fatal("enter on a history row must start a play-by-id command")
	}

	// Render smoke: the sectioned list draws without a player.
	view := m.list.View()
	if !strings.Contains(view, "Recent Song") {
		t.Fatalf("stats list render missing rows:\n%s", view)
	}
}

func TestStatsScreenEmptyHistory(t *testing.T) {
	m := interactionModel(screenMenu)
	_, _ = m.Update(statsMsg{})
	items := m.list.Items()
	if len(items) != 1 || items[0].(row).kind != rowInfo {
		t.Fatalf("empty stats should show a single info row, got %d items", len(items))
	}
}

// ---- start radio ('m') ----

func TestStartRadioSeedsFromContext(t *testing.T) {
	// Track row on a browse list → song radio.
	m := interactionModel(screenList)
	setTrackList(m, testTracks(3))
	m.list.Select(1)
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if cmd == nil || !m.loading {
		t.Fatal("m on a track row must start a mix command")
	}

	// Artist row → artist radio.
	m = interactionModel(screenList)
	m.list.SetItems([]list.Item{artistRow(deezer.ArtistInfo{ID: "5", Name: "Artist"})})
	_, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if cmd == nil {
		t.Fatal("m on an artist row must start an artist mix")
	}

	// Queue view → the cursor row seeds.
	m = interactionModel(screenQueue)
	m.q.Set(testTracks(2), 0)
	m.queueSel = 1
	_, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if cmd == nil {
		t.Fatal("m on the queue view must start a mix from the cursor row")
	}

	// Now-playing overlay → the current track seeds.
	m = interactionModel(screenNowPlaying)
	m.q.Set(testTracks(1), 0)
	_, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if cmd == nil {
		t.Fatal("m on now-playing must start a mix from the current track")
	}

	// Episode queues have no mixes.
	m = interactionModel(screenQueue)
	m.q.Set(testTracks(1), 0)
	m.episodeMode = true
	_, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if cmd != nil {
		t.Fatal("m must be a no-op on an episode queue")
	}

	// An artist page with no seed row selected falls back to the page's artist.
	m = interactionModel(screenList)
	m.pageArtist = deezer.ArtistInfo{ID: "5", Name: "Artist"}
	m.list.SetItems(nil)
	_, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if cmd == nil {
		t.Fatal("m on an artist page must seed from the page artist")
	}
}
