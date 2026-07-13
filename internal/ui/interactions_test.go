package ui

import (
	"strings"
	"testing"

	"github.com/Cycl0o0/OpenDeezer/v3/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/v3/internal/queue"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// interactionModel builds a UI-only model (no client / audio device) with the
// production browse-list configuration, for exercising key handling.
func interactionModel(s screen) *Model {
	l := newBrowseList()
	l.SetSize(80, 24)
	return &Model{
		screen: s,
		list:   l,
		search: textinput.New(),
		q:      queue.New(),
		width:  80,
		height: 24,
	}
}

func testTracks(n int) []deezer.Track {
	ts := make([]deezer.Track, n)
	for i := range ts {
		id := string(rune('a' + i))
		ts[i] = deezer.Track{
			ID: id, Name: "Track " + id,
			Artists:   []deezer.Artist{{ID: id, Name: "Artist " + id}},
			AlbumName: "Album " + id,
		}
	}
	return ts
}

func setTrackList(m *Model, ts []deezer.Track) {
	items := make([]list.Item, len(ts))
	for i, t := range ts {
		items[i] = trackRow(t)
	}
	m.browse = ts
	m.list.SetItems(items)
	m.list.ResetSelected()
}

func keyRunes(m *Model, r rune) {
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
}

// ---- ctrl+f local filter ----

func TestCtrlFStartsListFilterAndSlashStaysSearch(t *testing.T) {
	m := interactionModel(screenList)
	setTrackList(m, testTracks(3))

	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlF})
	if m.list.FilterState() != list.Filtering {
		t.Fatalf("ctrl+f should start the list filter, state=%v", m.list.FilterState())
	}
	// While filtering, plain keys type into the filter — not global bindings.
	keyRunes(m, 'q')
	if m.screen != screenList {
		t.Fatalf("typing into the filter changed screens: %v", m.screen)
	}

	// "/" (outside filtering) still opens Deezer search.
	m2 := interactionModel(screenList)
	setTrackList(m2, testTracks(3))
	keyRunes(m2, '/')
	if m2.screen != screenSearch {
		t.Fatalf("/ should open Deezer search, got screen %v", m2.screen)
	}
}

func TestCtrlFIgnoredOnOverlays(t *testing.T) {
	m := interactionModel(screenHelp)
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlF})
	if m.list.FilterState() == list.Filtering {
		t.Fatal("ctrl+f on an overlay must not start filtering the hidden list")
	}
}

func TestEscClearsAppliedFilterBeforeNavigating(t *testing.T) {
	m := interactionModel(screenList)
	setTrackList(m, testTracks(3))
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlF})
	keyRunes(m, 'a')
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter}) // apply the filter
	if m.list.FilterState() != list.FilterApplied {
		t.Fatalf("filter not applied: %v", m.list.FilterState())
	}
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.list.FilterState() != list.Unfiltered || m.screen != screenList {
		t.Fatalf("first esc should only clear the filter (state=%v screen=%v)",
			m.list.FilterState(), m.screen)
	}
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.screen != screenMenu {
		t.Fatalf("second esc should navigate back, got %v", m.screen)
	}
}

func TestFilterValueIncludesArtistAndAlbum(t *testing.T) {
	r := trackRow(deezer.Track{
		Name:      "Song",
		Artists:   []deezer.Artist{{Name: "Some Artist"}},
		AlbumName: "Great Album",
	})
	fv := r.FilterValue()
	if !strings.Contains(fv, "Some Artist") || !strings.Contains(fv, "Great Album") {
		t.Fatalf("FilterValue should include artist and album: %q", fv)
	}
	if sectionRow("Top tracks").FilterValue() != "" {
		t.Fatal("section headers must not match filters")
	}
}

// ---- artist page ----

func TestArtistPageBuildsSectionedList(t *testing.T) {
	m := interactionModel(screenLoading)
	page := &deezer.ArtistPage{
		Artist: deezer.ArtistInfo{ID: "1", Name: "Artist"},
		Top:    testTracks(2),
		Albums: []deezer.Album{{ID: "al1", Name: "LP", Artists: []deezer.Artist{{Name: "Artist"}}}},
		Related: []deezer.ArtistInfo{
			{ID: "2", Name: "Friend"},
		},
	}
	_, _ = m.Update(artistPageMsg{page: page})

	if m.screen != screenList {
		t.Fatalf("screen = %v, want list", m.screen)
	}
	items := m.list.Items()
	// 3 headers + 2 tracks + 1 album + 1 related.
	if len(items) != 7 {
		t.Fatalf("items = %d, want 7", len(items))
	}
	kinds := make([]rowKind, len(items))
	for i, it := range items {
		kinds[i] = it.(row).kind
	}
	want := []rowKind{rowSection, rowTrack, rowTrack, rowSection, rowAlbum, rowSection, rowArtist}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("item %d kind = %v, want %v (%v)", i, kinds[i], want[i], kinds)
		}
	}
	if len(m.browse) != 2 || m.browse[0].ID != "a" {
		t.Fatalf("browse should hold only the top tracks: %+v", m.browse)
	}
	if m.list.Index() != 1 {
		t.Fatalf("selection should start on the first entry, not the header: %d", m.list.Index())
	}
	// Enter on the album row opens it (returns a fetch command).
	m.list.Select(4)
	_, cmd := m.activate()
	if cmd == nil {
		t.Fatal("activating an album row should return a command")
	}
	// Section headers do nothing.
	m.list.Select(3)
	_, cmd = m.activate()
	if cmd != nil {
		t.Fatal("activating a section header should be a no-op")
	}
}

// ---- like toggle ----

func TestLikeToggleFlipsCachedSetOptimistically(t *testing.T) {
	m := interactionModel(screenList)
	ts := testTracks(2)
	setTrackList(m, ts)
	m.likedIDs = map[string]bool{"a": true}

	// Selected row "a" is liked -> f unlikes it (optimistically removed).
	keyRunes(m, 'f')
	if m.likedIDs["a"] {
		t.Fatal("unlike should remove the id from the cached set")
	}
	// A failed round-trip rolls the flip back.
	_, _ = m.Update(favToggleMsg{id: "a", name: "Track a", liked: false, err: errFake})
	if !m.likedIDs["a"] {
		t.Fatal("failed unlike should restore the cached id")
	}

	// Selected row "b" is not liked -> f likes it.
	m.list.Select(1)
	keyRunes(m, 'f')
	if !m.likedIDs["b"] {
		t.Fatal("like should add the id to the cached set")
	}
}

func TestFavoritesViewRefreshesLikedCache(t *testing.T) {
	m := interactionModel(screenMenu)
	m.likedIDs = map[string]bool{"stale": true}
	_, _ = m.Update(tracksMsg{title: "Liked", tracks: testTracks(2), favorites: true})
	if m.likedIDs["stale"] || !m.likedIDs["a"] || !m.likedIDs["b"] {
		t.Fatalf("favorites view should rebuild the liked cache: %v", m.likedIDs)
	}
	if m.ownPlaylists {
		t.Fatal("a tracks view must clear the own-playlists flag")
	}
}

// ---- add to playlist / playlist CRUD ----

func TestAddToPlaylistPickerFlow(t *testing.T) {
	m := interactionModel(screenList)
	setTrackList(m, testTracks(2))

	// The picker opens over the browse list (snapshot + restore like devices).
	_, _ = m.Update(playlistPickMsg{
		playlists: []deezer.Playlist{{ID: "p1", Name: "Mix"}},
		track:     m.browse[0],
	})
	if m.screen != screenPlaylistPick {
		t.Fatalf("screen = %v, want playlist picker", m.screen)
	}
	items := m.list.Items()
	if len(items) != 2 || items[0].(row).action != actNewPlaylist || items[1].(row).kind != rowPlaylist {
		t.Fatalf("picker items wrong: %+v", items)
	}
	// Enter on a playlist adds and restores the browse list.
	m.list.Select(1)
	_, cmd := m.activate()
	if cmd == nil {
		t.Fatal("picking a playlist should return the add command")
	}
	if m.screen != screenList || len(m.list.Items()) != 2 || m.list.Items()[0].(row).kind != rowTrack {
		t.Fatalf("picker should restore the browse list: screen=%v items=%d", m.screen, len(m.list.Items()))
	}
}

func TestNewPlaylistEntryOpensTitlePrompt(t *testing.T) {
	m := interactionModel(screenList)
	setTrackList(m, testTracks(1))
	_, _ = m.Update(playlistPickMsg{playlists: nil, track: m.browse[0]})
	_, _ = m.activate() // index 0 = "New playlist…"
	if m.screen != screenPlaylistPrompt || m.plPrompt != plCreateWithTrack {
		t.Fatalf("new-playlist entry: screen=%v prompt=%v", m.screen, m.plPrompt)
	}
	// Enter with a title dispatches the create and leaves the prompt.
	m.search.SetValue("Road trip")
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || m.screen == screenPlaylistPrompt {
		t.Fatalf("prompt enter should create and close (screen=%v)", m.screen)
	}
}

func TestPlaylistOpsKeysOnOwnPlaylistsScreen(t *testing.T) {
	m := interactionModel(screenMenu)
	_, _ = m.Update(playlistsMsg{title: "My Playlists", playlists: []deezer.Playlist{{ID: "p1", Name: "Mix"}}})
	if !m.ownPlaylists || m.screen != screenList {
		t.Fatalf("playlists view: own=%v screen=%v", m.ownPlaylists, m.screen)
	}

	// R opens the rename prompt prefilled with the current title.
	keyRunes(m, 'R')
	if m.screen != screenPlaylistPrompt || m.plPrompt != plRename || m.search.Value() != "Mix" {
		t.Fatalf("rename prompt: screen=%v prompt=%v value=%q", m.screen, m.plPrompt, m.search.Value())
	}
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.screen != screenList {
		t.Fatalf("prompt esc should return to the playlists screen, got %v", m.screen)
	}

	// X asks for confirmation; n cancels, y deletes.
	keyRunes(m, 'X')
	if !m.plConfirm {
		t.Fatal("X should arm the delete confirmation")
	}
	keyRunes(m, 'n') // must cancel, NOT skip to the next track
	if m.plConfirm {
		t.Fatal("n should cancel the delete confirmation")
	}
	keyRunes(m, 'X')
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if m.plConfirm || cmd == nil {
		t.Fatal("y should dispatch the delete command")
	}

	// N opens the create prompt.
	m.loading = false
	keyRunes(m, 'N')
	if m.screen != screenPlaylistPrompt || m.plPrompt != plCreateEmpty {
		t.Fatalf("N should open the new-playlist prompt: screen=%v prompt=%v", m.screen, m.plPrompt)
	}
}

func TestPlaylistOpsIgnoredOffPlaylistsScreen(t *testing.T) {
	m := interactionModel(screenList)
	setTrackList(m, testTracks(1)) // a plain browse list (ownPlaylists = false)
	keyRunes(m, 'N')
	if m.screen == screenPlaylistPrompt {
		t.Fatal("N must be inert outside the playlists screen")
	}
	keyRunes(m, 'X')
	if m.plConfirm {
		t.Fatal("X must not arm a delete outside the playlists screen")
	}
	if !m.updateDismissed {
		t.Fatal("X should keep its update-dismiss role elsewhere")
	}
}

// ---- interactive queue + queue building ----

func TestQueueScreenNavigationAndEditing(t *testing.T) {
	m := interactionModel(screenList)
	ts := testTracks(4)
	m.q.Set(ts, 0)

	keyRunes(m, 'u') // open the queue view with the cursor on the playing row
	if m.screen != screenQueue || m.queueSel != 0 {
		t.Fatalf("queue open: screen=%v sel=%d", m.screen, m.queueSel)
	}
	keyRunes(m, 'j')
	keyRunes(m, 'j')
	if m.queueSel != 2 {
		t.Fatalf("j j -> sel %d, want 2", m.queueSel)
	}
	keyRunes(m, 'k')
	if m.queueSel != 1 {
		t.Fatalf("k -> sel %d, want 1", m.queueSel)
	}

	// x removes the selected (non-playing) row.
	keyRunes(m, 'x')
	if m.q.Len() != 3 || m.q.Tracks()[1].ID != "c" {
		t.Fatalf("x should remove row 1: len=%d tracks[1]=%q", m.q.Len(), m.q.Tracks()[1].ID)
	}
	// x on the playing row is refused.
	m.queueSel = 0
	keyRunes(m, 'x')
	if m.q.Len() != 3 {
		t.Fatal("x must not remove the playing track")
	}

	// J/K move the selected row, cursor following.
	m.queueSel = 1 // "c"
	keyRunes(m, 'J')
	if m.q.Tracks()[2].ID != "c" || m.queueSel != 2 {
		t.Fatalf("J: tracks=%v sel=%d", m.q.Tracks(), m.queueSel)
	}
	keyRunes(m, 'K')
	if m.q.Tracks()[1].ID != "c" || m.queueSel != 1 {
		t.Fatalf("K: tracks=%v sel=%d", m.q.Tracks(), m.queueSel)
	}

	// enter jumps playback to the selected row.
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.q.Index() != 1 || cmd == nil {
		t.Fatalf("enter should jump to row 1 and start a play: idx=%d", m.q.Index())
	}

	// g / G jump the cursor.
	keyRunes(m, 'G')
	if m.queueSel != m.q.Len()-1 {
		t.Fatalf("G -> sel %d", m.queueSel)
	}
	keyRunes(m, 'g')
	if m.queueSel != 0 {
		t.Fatalf("g -> sel %d", m.queueSel)
	}
}

func TestBrowsePlayNextAndAddToEnd(t *testing.T) {
	m := interactionModel(screenList)
	ts := testTracks(3)
	setTrackList(m, ts)
	m.q.Set([]deezer.Track{{ID: "z", Name: "Playing"}}, 0)

	// n on a selected track row queues it right after the current track.
	m.list.Select(1) // track "b"
	keyRunes(m, 'n')
	if m.q.Len() != 2 || m.q.Tracks()[1].ID != "b" || m.q.Index() != 0 {
		t.Fatalf("play-next: tracks=%v idx=%d", m.q.Tracks(), m.q.Index())
	}
	// e appends to the end.
	m.list.Select(2) // track "c"
	keyRunes(m, 'e')
	if m.q.Len() != 3 || m.q.Tracks()[2].ID != "c" {
		t.Fatalf("append: tracks=%v", m.q.Tracks())
	}
	// n with no track row selected falls back to "next track".
	m.list.SetItems(nil)
	before := m.q.Index()
	keyRunes(m, 'n')
	if m.q.Index() != before+1 {
		t.Fatalf("n without a track row should advance playback: %d -> %d", before, m.q.Index())
	}
}

// ---- control queue snapshot cache ----

func TestCtrlQueueSnapshotRebuiltOnlyOnVersionChange(t *testing.T) {
	m := interactionModel(screenList)
	if m.ctrlQueueSnapshot() != nil {
		t.Fatal("empty queue should snapshot to nil")
	}
	m.q.Set(testTracks(3), 0)
	s1 := m.ctrlQueueSnapshot()
	if len(s1) != 3 || s1[0].ID != "a" {
		t.Fatalf("snapshot = %+v", s1)
	}
	s2 := m.ctrlQueueSnapshot()
	if &s1[0] != &s2[0] {
		t.Fatal("unchanged queue must reuse the cached snapshot slice")
	}
	// Cursor moves don't bump the version — still cached.
	m.q.Next()
	s3 := m.ctrlQueueSnapshot()
	if &s1[0] != &s3[0] {
		t.Fatal("cursor move must not rebuild the snapshot")
	}
	// A queue edit does.
	m.q.Append(deezer.Track{ID: "x", Name: "X"})
	s4 := m.ctrlQueueSnapshot()
	if len(s4) != 4 || s4[3].ID != "x" {
		t.Fatalf("snapshot after append = %+v", s4)
	}
}

// ---- mouse ----

func TestMouseWheelScrollsListAndClickSelectsRow(t *testing.T) {
	m := interactionModel(screenList)
	setTrackList(m, testTracks(5))
	m.list.SetSize(80, 20)

	_, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	if m.list.Index() != 1 {
		t.Fatalf("wheel down should move the cursor, index=%d", m.list.Index())
	}
	_, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
	if m.list.Index() != 0 {
		t.Fatalf("wheel up should move the cursor back, index=%d", m.list.Index())
	}

	// Click on the third row: y = header(2) + 2*stride(3) = 8.
	_, _ = m.Update(tea.MouseMsg{X: 4, Y: 8, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.list.Index() != 2 {
		t.Fatalf("click should select row 2, index=%d", m.list.Index())
	}
	// A click on the spacing row between items is ignored.
	_, _ = m.Update(tea.MouseMsg{X: 4, Y: 4, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.list.Index() != 2 {
		t.Fatalf("spacing-row click must be ignored, index=%d", m.list.Index())
	}
	// A second click on the selected row activates it (play => queue commit).
	_, cmd := m.Update(tea.MouseMsg{X: 4, Y: 8, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if cmd == nil || m.q.Len() != 5 || m.q.Index() != 2 {
		t.Fatalf("click-on-selected should activate: qlen=%d idx=%d", m.q.Len(), m.q.Index())
	}
}

func TestMouseWheelScrollsHelpAndQueue(t *testing.T) {
	m := interactionModel(screenHelp)
	_, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	if m.helpOffset != 1 {
		t.Fatalf("wheel should scroll help, offset=%d", m.helpOffset)
	}
	q := interactionModel(screenQueue)
	q.q.Set(testTracks(3), 0)
	_, _ = q.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	if q.queueSel != 1 {
		t.Fatalf("wheel should move the queue cursor, sel=%d", q.queueSel)
	}
}

// errFake is a sentinel error for round-trip failure tests.
var errFake = errFakeT{}

type errFakeT struct{}

func (errFakeT) Error() string { return "fake failure" }
