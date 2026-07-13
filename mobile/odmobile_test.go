package odmobile

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/control"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/history"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/queue"
)

// resetEngineQueue restores the engine queue to its pristine (unsynced) state
// so tests can't leak queue contents / repeat / shuffle into each other.
func resetEngineQueue() {
	queueMu.Lock()
	engineQ.Set(nil, 0)
	engineQ.SetRepeat(queue.RepeatOff)
	engineQ.SetShuffle(false)
	queueMu.Unlock()
}

// TestSetQueueJSONReflectsInEngineState proves an app queue synced via
// SetQueueJSON/SetQueueIndex shows up on /status (State.Queue) with the cursor
// where the app put it — previously engineState never reported a queue.
func TestSetQueueJSONReflectsInEngineState(t *testing.T) {
	t.Cleanup(resetEngineQueue)

	js := `[
		{"id":"1","name":"One","durationMs":180000,"artistLine":"Solo Artist","albumName":"Alpha"},
		{"id":"2","name":"Two","durationMs":200000,"artists":[{"id":"77","name":"Duo"}],"albumName":"Beta"},
		{"id":"","name":"dropped: no id"}
	]`
	if err := SetQueueJSON(js); err != nil {
		t.Fatalf("SetQueueJSON: %v", err)
	}
	SetQueueIndex(1)

	st := engineState()
	if len(st.Queue) != 2 {
		t.Fatalf("engineState queue len = %d, want 2 (id-less entries dropped)", len(st.Queue))
	}
	if st.Queue[0].ID != "1" || st.Queue[0].Title != "One" || st.Queue[0].DurationMS != 180000 {
		t.Fatalf("queue[0] = %+v, want id=1 title=One durationMs=180000", st.Queue[0])
	}
	// artistLine-only wire tracks keep their artist via the fallback conversion.
	if st.Queue[0].Artist != "Solo Artist" {
		t.Fatalf("queue[0].Artist = %q, want artistLine fallback \"Solo Artist\"", st.Queue[0].Artist)
	}
	if st.Queue[1].ID != "2" || st.Queue[1].Artist != "Duo" || st.Queue[1].ArtistID != "77" {
		t.Fatalf("queue[1] = %+v, want id=2 artist=Duo artistId=77", st.Queue[1])
	}
	if got := QueueIndex(); got != 1 {
		t.Fatalf("QueueIndex = %d, want 1", got)
	}

	// The synced queue also feeds Preload's duration lookup.
	if got := queuedDurationMS("2"); got != 200000 {
		t.Fatalf("queuedDurationMS(2) = %d, want 200000", got)
	}
	if got := queuedDurationMS("missing"); got != 0 {
		t.Fatalf("queuedDurationMS(missing) = %d, want 0", got)
	}

	// Clearing (the "[]" payload) removes the queue from /status again.
	if err := SetQueueJSON(`[]`); err != nil {
		t.Fatalf("SetQueueJSON(\"[]\"): %v", err)
	}
	if st := engineState(); len(st.Queue) != 0 {
		t.Fatalf("cleared queue still reported: %+v", st.Queue)
	}
	if got := QueueIndex(); got != -1 {
		t.Fatalf("QueueIndex after clear = %d, want -1", got)
	}
}

// TestSetQueueJSONRejectsBadPayload proves malformed JSON errors instead of
// silently clearing the queue.
func TestSetQueueJSONRejectsBadPayload(t *testing.T) {
	t.Cleanup(resetEngineQueue)

	if err := SetQueueJSON(`[{"id":"1"}]`); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	if err := SetQueueJSON(`{"not":"an array"}`); err == nil {
		t.Fatal("non-array payload must be rejected")
	}
	if err := SetQueueJSON(`[{"id":`); err == nil {
		t.Fatal("truncated payload must be rejected")
	}
	// A rejected payload must leave the previously synced queue untouched.
	if got := QueueIndex(); got != 0 {
		t.Fatalf("bad payload disturbed the queue: index = %d, want 0", got)
	}
}

// TestRepeatShuffleReflectInEngineState proves the SetRepeat / SetShuffle
// persistence path: engineState reports the last set values instead of the old
// hard-coded Repeat:"off", Shuffle:false.
func TestRepeatShuffleReflectInEngineState(t *testing.T) {
	t.Cleanup(resetEngineQueue)

	SetRepeat(1) // all
	SetShuffle(1)
	st := engineState()
	if st.Repeat != "all" || !st.Shuffle {
		t.Fatalf("engineState repeat=%q shuffle=%v, want all/true", st.Repeat, st.Shuffle)
	}

	SetRepeat(2) // one
	if st := engineState(); st.Repeat != "one" {
		t.Fatalf("engineState repeat=%q, want one", st.Repeat)
	}

	SetRepeat(0)
	SetShuffle(0)
	st = engineState()
	if st.Repeat != "off" || st.Shuffle {
		t.Fatalf("engineState repeat=%q shuffle=%v, want off/false (default wire shape)", st.Repeat, st.Shuffle)
	}
}

// TestEngineNextPrevWalkSyncedQueue proves the control API's /next and /prev
// walk a queue synced through SetQueueJSON (cursor movement is the queue
// deliverable; play itself needs a logged-in client + audio device).
func TestEngineNextPrevWalkSyncedQueue(t *testing.T) {
	t.Cleanup(resetEngineQueue)

	if err := SetQueueJSON(`[{"id":"a"},{"id":"b"},{"id":"c"}]`); err != nil {
		t.Fatalf("SetQueueJSON: %v", err)
	}

	engineNext()
	if got := QueueIndex(); got != 1 {
		t.Fatalf("after Next: index = %d, want 1", got)
	}
	engineNext()
	if got := QueueIndex(); got != 2 {
		t.Fatalf("after Next x2: index = %d, want 2", got)
	}
	engineNext() // end of queue under RepeatOff: stays put
	if got := QueueIndex(); got != 2 {
		t.Fatalf("Next past the end moved the cursor to %d, want 2", got)
	}
	enginePrev()
	if got := QueueIndex(); got != 1 {
		t.Fatalf("after Prev: index = %d, want 1", got)
	}
	enginePrev()
	if got := QueueIndex(); got != 0 {
		t.Fatalf("after Prev x2: index = %d, want 0", got)
	}
}

// TestPreloadRequiresEngine proves Preload surfaces "engine not ready" before
// Init instead of panicking, and ClearPreload is a safe no-op without a player.
func TestPreloadRequiresEngine(t *testing.T) {
	if err := Preload("123"); err == nil {
		t.Fatal("Preload before Init must return an error")
	}
	ClearPreload() // must not panic without a player
}

// TestEngineCommandsWiresExtendedHooks proves the v2.2 extended-control hooks
// are all wired (a nil hook makes the control server answer 501/404 for its
// endpoint, silently disabling the feature for every remote).
func TestEngineCommandsWiresExtendedHooks(t *testing.T) {
	cmds := engineCommands()
	if cmds.QueueAdd == nil || cmds.QueueJump == nil || cmds.QueueRemove == nil || cmds.QueueMove == nil {
		t.Fatal("queue hooks (add/jump/remove/move) must be wired")
	}
	if cmds.PlayAlbum == nil || cmds.PlayMixTrack == nil || cmds.PlayMixArtist == nil {
		t.Fatal("play hooks (album/mix track/mix artist) must be wired")
	}
	if cmds.HistoryRecent == nil {
		t.Fatal("HistoryRecent hook must be wired")
	}
}

// TestEngineQueueInsertNextAndAppend proves the QueueAdd plumbing: "play next"
// splices right after the cursor, plain add appends, and both show up in
// engineState (what /status reports) and bump the queue content version (what
// QueueVersion exposes for app resync).
func TestEngineQueueInsertNextAndAppend(t *testing.T) {
	t.Cleanup(resetEngineQueue)

	if err := SetQueueJSON(`[{"id":"1"},{"id":"2"},{"id":"3"}]`); err != nil {
		t.Fatalf("SetQueueJSON: %v", err)
	}
	v0 := QueueVersion()

	engineQueueInsert(deezer.Track{ID: "X"}, true) // play next -> after cursor (0)
	engineQueueInsert(deezer.Track{ID: "Y"}, false)

	st := engineState()
	got := make([]string, len(st.Queue))
	for i, tr := range st.Queue {
		got[i] = tr.ID
	}
	want := []string{"1", "X", "2", "3", "Y"}
	if len(got) != len(want) {
		t.Fatalf("queue after add = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("queue[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
	if QueueIndex() != 0 {
		t.Fatalf("adds moved the cursor to %d, want 0", QueueIndex())
	}
	if v1 := QueueVersion(); v1 <= v0 {
		t.Fatalf("queue version did not bump on adds: %d -> %d", v0, v1)
	}
}

// TestEngineQueueSelectMovesCursor proves the pure QueueJump plumbing: a valid
// index moves the cursor (recording history so Prev retraces) and returns the
// selected track; out-of-range indices error without disturbing the queue.
// engineQueueSelect uses queue.SetIndex, so every jump — including the first
// after a replace — records the outgoing row.
func TestEngineQueueSelectMovesCursor(t *testing.T) {
	t.Cleanup(resetEngineQueue)

	if err := SetQueueJSON(`[{"id":"a"},{"id":"b"},{"id":"c"}]`); err != nil {
		t.Fatalf("SetQueueJSON: %v", err)
	}

	if tr, err := engineQueueSelect(1); err != nil || tr.ID != "b" {
		t.Fatalf("engineQueueSelect(1) = (%q, %v), want (b, nil)", tr.ID, err)
	}
	tr, err := engineQueueSelect(2)
	if err != nil || tr.ID != "c" {
		t.Fatalf("engineQueueSelect(2) = (%q, %v), want (c, nil)", tr.ID, err)
	}
	if QueueIndex() != 2 {
		t.Fatalf("cursor = %d after select, want 2", QueueIndex())
	}
	// Prev retraces to the row that was playing before the jump.
	enginePrev()
	if QueueIndex() != 1 {
		t.Fatalf("Prev after jump retraced to %d, want 1", QueueIndex())
	}
	if _, err := engineQueueSelect(3); err == nil {
		t.Fatal("out-of-range select must error")
	}
	if _, err := engineQueueSelect(-1); err == nil {
		t.Fatal("negative select must error")
	}
}

// TestSetQueueIndexAlignsWithoutHistory proves the app cursor-sync path
// (SetQueueIndex → queue.AlignIndex) records no history, so a remote Prev after
// alignment never jumps to a never-played row 0.
func TestSetQueueIndexAlignsWithoutHistory(t *testing.T) {
	t.Cleanup(resetEngineQueue)

	if err := SetQueueJSON(`[{"id":"a"},{"id":"b"},{"id":"c"}]`); err != nil {
		t.Fatalf("SetQueueJSON: %v", err)
	}
	SetQueueIndex(2) // app says "I'm playing row 2" — pure alignment
	if QueueIndex() != 2 {
		t.Fatalf("cursor = %d after alignment, want 2", QueueIndex())
	}
	enginePrev()
	if QueueIndex() == 0 {
		t.Fatal("Prev after alignment jumped to the never-played row 0")
	}
	if QueueIndex() != 1 {
		t.Fatalf("Prev after alignment stepped to %d, want linear 1", QueueIndex())
	}
}

// TestEngineQueueJumpRequiresEngine proves QueueJump fails cleanly (and leaves
// the cursor alone) when no client/player is up, instead of half-jumping.
func TestEngineQueueJumpRequiresEngine(t *testing.T) {
	t.Cleanup(resetEngineQueue)

	if err := SetQueueJSON(`[{"id":"a"},{"id":"b"}]`); err != nil {
		t.Fatalf("SetQueueJSON: %v", err)
	}
	if err := engineQueueJump(1); err == nil {
		t.Fatal("QueueJump without a logged-in engine must error")
	}
	if QueueIndex() != 0 {
		t.Fatalf("failed jump moved the cursor to %d, want 0", QueueIndex())
	}
}

// TestEngineQueueRemoveGuards proves QueueRemove rejects the playing row and
// out-of-range indices, and otherwise edits the queue /status reports.
func TestEngineQueueRemoveGuards(t *testing.T) {
	t.Cleanup(resetEngineQueue)

	if err := SetQueueJSON(`[{"id":"a"},{"id":"b"},{"id":"c"}]`); err != nil {
		t.Fatalf("SetQueueJSON: %v", err)
	}
	SetQueueIndex(1)

	if err := engineQueueRemove(1); err == nil {
		t.Fatal("removing the playing row must be rejected")
	}
	if err := engineQueueRemove(3); err == nil {
		t.Fatal("out-of-range remove must error")
	}
	if err := engineQueueRemove(2); err != nil {
		t.Fatalf("valid remove failed: %v", err)
	}
	st := engineState()
	if len(st.Queue) != 2 || st.Queue[0].ID != "a" || st.Queue[1].ID != "b" {
		t.Fatalf("queue after remove = %+v, want [a b]", st.Queue)
	}
	if QueueIndex() != 1 {
		t.Fatalf("cursor = %d after removing a later row, want 1", QueueIndex())
	}
}

// TestEngineQueueMoveFollowsCursor proves QueueMove reorders the queue while
// the cursor keeps following the playing track, and rejects bad indices.
func TestEngineQueueMoveFollowsCursor(t *testing.T) {
	t.Cleanup(resetEngineQueue)

	if err := SetQueueJSON(`[{"id":"a"},{"id":"b"},{"id":"c"}]`); err != nil { // playing "a" (cursor 0)
		t.Fatalf("SetQueueJSON: %v", err)
	}

	if err := engineQueueMove(2, 0); err != nil {
		t.Fatalf("move failed: %v", err)
	}
	st := engineState()
	got := []string{st.Queue[0].ID, st.Queue[1].ID, st.Queue[2].ID}
	if got[0] != "c" || got[1] != "a" || got[2] != "b" {
		t.Fatalf("queue after move = %v, want [c a b]", got)
	}
	if QueueIndex() != 1 {
		t.Fatalf("cursor = %d, want 1 (still on the playing track)", QueueIndex())
	}
	if err := engineQueueMove(0, 0); err != nil {
		t.Fatalf("from == to must be a no-op success, got %v", err)
	}
	if err := engineQueueMove(0, 9); err == nil {
		t.Fatal("out-of-range move must error")
	}
}

// TestPlayCommandsRequireEngine proves the album/mix commands surface a clean
// error before Init instead of panicking or silently doing nothing (the
// control server maps it to 409 for the remote).
func TestPlayCommandsRequireEngine(t *testing.T) {
	if err := enginePlayAlbum("123"); err == nil {
		t.Fatal("PlayAlbum without a client must error")
	}
	if err := enginePlayMixTrack("123"); err == nil {
		t.Fatal("PlayMixTrack without a client must error")
	}
	if err := enginePlayMixArtist("123"); err == nil {
		t.Fatal("PlayMixArtist without a client must error")
	}
	if err := engineQueueAdd("123", true); err == nil {
		t.Fatal("QueueAdd without a client must error")
	}
}

// TestEnginePlayFetchedRejectsEmpty proves an album/mix that resolves to zero
// tracks errors instead of clearing the queue and "succeeding" silently.
func TestEnginePlayFetchedRejectsEmpty(t *testing.T) {
	if err := enginePlayFetched("album", nil, nil); err == nil {
		t.Fatal("empty track list must error")
	}
}

// TestQueueJSONExport proves the QueueJSON app-resync export carries version,
// cursor and the wire-shaped track list.
func TestQueueJSONExport(t *testing.T) {
	t.Cleanup(resetEngineQueue)

	if err := SetQueueJSON(`[{"id":"a","name":"Alpha"},{"id":"b"}]`); err != nil {
		t.Fatalf("SetQueueJSON: %v", err)
	}
	SetQueueIndex(1)

	var got struct {
		Version uint64 `json:"version"`
		Index   int    `json:"index"`
		Tracks  []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"tracks"`
	}
	if err := json.Unmarshal([]byte(QueueJSON()), &got); err != nil {
		t.Fatalf("QueueJSON unmarshal: %v", err)
	}
	if got.Index != 1 || len(got.Tracks) != 2 || got.Tracks[0].ID != "a" || got.Tracks[0].Name != "Alpha" {
		t.Fatalf("QueueJSON = %+v", got)
	}
	if got.Version == 0 {
		t.Fatal("QueueJSON version missing (Set must bump it)")
	}
}

// TestEngineHistoryRecentReturnsJSON proves /history/recent's backing hook
// returns valid JSON (newest first) from the history store, and [] — not
// null — for an empty log.
func TestEngineHistoryRecentReturnsJSON(t *testing.T) {
	setHistoryStore(history.New(filepath.Join(t.TempDir(), "history.jsonl")))

	raw, err := engineHistoryRecent(10)
	if err != nil {
		t.Fatalf("empty history errored: %v", err)
	}
	if string(raw) != "[]" {
		t.Fatalf("empty history = %s, want []", raw)
	}

	st := historyStore()
	if err := st.Record(history.Entry{TrackID: "1", Title: "First", Artist: "A", StartedAt: 100, DurationPlayedSec: 60}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := st.Record(history.Entry{TrackID: "2", Title: "Second", Artist: "B", StartedAt: 200, DurationPlayedSec: 30}); err != nil {
		t.Fatalf("record: %v", err)
	}

	raw, err = engineHistoryRecent(10)
	if err != nil {
		t.Fatalf("engineHistoryRecent: %v", err)
	}
	if !json.Valid(raw) {
		t.Fatalf("invalid JSON: %s", raw)
	}
	var entries []history.Entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entries) != 2 || entries[0].TrackID != "2" || entries[1].TrackID != "1" {
		t.Fatalf("entries = %+v, want newest first [2 1]", entries)
	}
}

// TestListenEntry proves the history-record guards: no id and sub-second
// listens are dropped (start-skips, pause/resume can't produce records), the
// listened time is clamped to the track duration, and the fields map through.
func TestListenEntry(t *testing.T) {
	if _, ok := listenEntry(deezer.Track{}, 5000); ok {
		t.Fatal("track without an id must not be recorded")
	}
	if _, ok := listenEntry(deezer.Track{ID: "1"}, 999); ok {
		t.Fatal("sub-second listens must not be recorded")
	}
	e, ok := listenEntry(deezer.Track{
		ID: "42", Name: "Song", AlbumName: "Album", DurationMS: 3000,
		Artists: []deezer.Artist{{ID: "7", Name: "Artist"}},
	}, 10000)
	if !ok {
		t.Fatal("valid listen was dropped")
	}
	if e.TrackID != "42" || e.Title != "Song" || e.Artist != "Artist" || e.Album != "Album" {
		t.Fatalf("entry fields = %+v", e)
	}
	if e.DurationPlayedSec != 3 {
		t.Fatalf("played sec = %d, want 3 (clamped to track duration)", e.DurationPlayedSec)
	}
	if e.StartedAt == 0 {
		t.Fatal("StartedAt must be derived, not left for Record's now()")
	}
}

// waitHistory polls the history store until it holds at least n entries and
// returns them newest-first. recordListen appends on its own goroutine, so
// tests must wait rather than read immediately after the recording call.
func waitHistory(t *testing.T, n int) []history.Entry {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		entries, err := historyStore().Recent(50)
		if err == nil && len(entries) >= n {
			return entries
		}
		if time.Now().After(deadline) {
			t.Fatalf("history has %d entries (err=%v), want %d", len(entries), err, n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestGaplessPromoteUnsyncedQueueUsesArmedPreload proves the fix for the
// shipped-app flow (Preload WITHOUT a SetQueueJSON sync): after the player
// gaplessly promotes the armed preload, now-playing moves onto the preloaded
// track, the outgoing track lands in history exactly once, and the promoted
// track is recorded on its own later finish (previously NowPlaying stayed on
// the old track, which was then recorded twice while the new one never was).
func TestGaplessPromoteUnsyncedQueueUsesArmedPreload(t *testing.T) {
	resetEngineQueue() // this test depends on an EMPTY (unsynced) engine queue
	t.Cleanup(resetEngineQueue)
	t.Cleanup(func() { clearPreloadedTrack(); setCurrentTrack(deezer.Track{}) })
	setHistoryStore(history.New(filepath.Join(t.TempDir(), "history.jsonl")))

	a := deezer.Track{ID: "A", Name: "First", DurationMS: 3000}
	b := deezer.Track{ID: "B", Name: "Second", DurationMS: 4000}
	setCurrentTrack(a)   // playing A; the engine queue stays empty (unsynced)
	setPreloadedTrack(b) // the app armed a preload for B (Preload)

	// A drains; the player promotes B and keeps playing. The onFinish callback
	// records A (noteTrackFinished), then runs the promote bookkeeping.
	noteTrackFinished()
	advanceNowPlayingOnGaplessPromote()
	if got := currentTrack().ID; got != "B" {
		t.Fatalf("currentTrack after promote = %q, want B (the armed preload)", got)
	}
	if _, armed := takePreloadedTrack(); armed {
		t.Fatal("the promote must consume the preload stash (single-use)")
	}
	// Wait for A's (async) history write before finishing B: in production the
	// two finishes are a whole track apart, and the file order is the assertion.
	if got := waitHistory(t, 1); got[0].TrackID != "A" {
		t.Fatalf("promote finish recorded %q, want the outgoing track A", got[0].TrackID)
	}

	// B finishes naturally later: its own listen is recorded — NOT A again.
	noteTrackFinished()

	entries := waitHistory(t, 2)
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.TrackID
	}
	if len(entries) != 2 || ids[0] != "B" || ids[1] != "A" {
		t.Fatalf("history = %v, want [B A] (newest first, A exactly once)", ids)
	}
}

// TestGaplessPromoteSyncedQueueStillWalks proves the pre-existing happy path
// is unchanged: with the engine queue synced and aligned, a gapless promote
// walks the cursor + now-playing along the queue.
func TestGaplessPromoteSyncedQueueStillWalks(t *testing.T) {
	t.Cleanup(resetEngineQueue)
	t.Cleanup(func() { clearPreloadedTrack(); setCurrentTrack(deezer.Track{}) })

	if err := SetQueueJSON(`[{"id":"A","durationMs":3000},{"id":"B","durationMs":4000}]`); err != nil {
		t.Fatalf("SetQueueJSON: %v", err)
	}
	setCurrentTrack(deezer.Track{ID: "A", DurationMS: 3000})
	setPreloadedTrack(deezer.Track{ID: "B", DurationMS: 4000})

	advanceNowPlayingOnGaplessPromote()
	if got := currentTrack().ID; got != "B" {
		t.Fatalf("currentTrack after owned promote = %q, want B", got)
	}
	if got := QueueIndex(); got != 1 {
		t.Fatalf("queue cursor after owned promote = %d, want 1", got)
	}
	if _, armed := takePreloadedTrack(); armed {
		t.Fatal("an owned promote must also consume the preload stash")
	}
}

// TestRepeatShuffleToggleClearsArmedPreload proves SetRepeat / SetShuffle
// invalidate an armed preload exactly like every queue-edit path does: after a
// toggle the stale linear-next must not gapless-promote (repeat-one replays
// the CURRENT track; a reshuffle changes the upcoming one).
func TestRepeatShuffleToggleClearsArmedPreload(t *testing.T) {
	resetEngineQueue() // this test depends on an EMPTY (unsynced) engine queue
	t.Cleanup(resetEngineQueue)
	t.Cleanup(func() { clearPreloadedTrack(); setCurrentTrack(deezer.Track{}) })

	setCurrentTrack(deezer.Track{ID: "A", DurationMS: 3000})

	setPreloadedTrack(deezer.Track{ID: "stale-next", DurationMS: 3000})
	SetRepeat(2) // one
	if _, armed := takePreloadedTrack(); armed {
		t.Fatal("SetRepeat must clear an armed preload")
	}

	setPreloadedTrack(deezer.Track{ID: "stale-next", DurationMS: 3000})
	SetShuffle(1)
	if _, armed := takePreloadedTrack(); armed {
		t.Fatal("SetShuffle must clear an armed preload")
	}

	// With the preload cleared, a subsequent finish cannot promote the stale
	// next: now-playing stays put.
	advanceNowPlayingOnGaplessPromote()
	if got := currentTrack().ID; got != "A" {
		t.Fatalf("cleared preload still moved now-playing to %q, want A", got)
	}
}

// TestStopRecordsOutgoingListenOnce proves the Stop paths (the exported Stop +
// the control-host Stop handler) record the in-progress listen exactly once:
// the closures call noteTrackTransition BEFORE p.Stop() while the player is
// still Playing, and the state guard makes the second Stop — the player is
// already Stopped then — record nothing.
func TestStopRecordsOutgoingListenOnce(t *testing.T) {
	t.Cleanup(func() { setCurrentTrack(deezer.Track{}) })
	setHistoryStore(history.New(filepath.Join(t.TempDir(), "history.jsonl")))

	setCurrentTrack(deezer.Track{ID: "77", Name: "Stopped Song", DurationMS: 240000})

	// First Stop: noteTrackTransition runs while the player is still Playing,
	// so the listen-so-far is recorded.
	recordTransition(audio.Playing, 65000)
	entries := waitHistory(t, 1)
	if entries[0].TrackID != "77" || entries[0].DurationPlayedSec != 65 {
		t.Fatalf("stop-listen entry = %+v, want track 77 played 65s", entries[0])
	}

	// Second Stop: p.Stop() already moved the player to Stopped, so the
	// transition records nothing (the guard is synchronous — recordListen is
	// never called — so this can be asserted immediately).
	recordTransition(audio.Stopped, 65000)
	if got, _ := historyStore().Recent(10); len(got) != 1 {
		t.Fatalf("second Stop double-recorded: history = %+v", got)
	}
}

// ---- test helpers for the Phase-1 fixes ----

// clearRemote resets the Connect (controller-side) routing globals so a routed
// test can't leak remoteCli/remoteSt into the next test.
func clearRemote() {
	mu.Lock()
	remoteCli = nil
	remoteSt = control.State{}
	remoteAddr = ""
	if remoteStop != nil {
		close(remoteStop)
		remoteStop = nil
	}
	mu.Unlock()
}

// resetControlGlobals closes and clears the shared control-server globals so a
// server a test published (or built) never leaks into another test.
func resetControlGlobals() {
	mu.Lock()
	srv, adv := ctrlSrv, hostAdv
	ctrlSrv, ctrlSrvClient, hostAdv = nil, nil, nil
	ctrlCfg = control.Config{}
	mu.Unlock()
	if adv != nil {
		adv.Close()
	}
	if srv != nil {
		srv.Close()
	}
}

// startTestControlServer starts a real control server on loopback (open auth,
// nil client) so tests can exercise the Connect controller/host paths without a
// live device. Registered for Close via t.Cleanup.
func startTestControlServer(t *testing.T) *control.Server {
	t.Helper()
	s := control.New(control.Config{Addr: "127.0.0.1:0"}, engineState, engineAccount, engineCommands(), nil)
	if err := s.Start(); err != nil {
		t.Fatalf("start test control server: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// TestGetRepeatShuffleRoutedVsLocal proves the B1 getters: with no Connect
// device they read the engine queue, and while routed they read the remote
// host's snapshot (so the app renders the host's real modes while casting)
// rather than the controller's local engine queue.
func TestGetRepeatShuffleRoutedVsLocal(t *testing.T) {
	t.Cleanup(resetEngineQueue)
	t.Cleanup(clearRemote)

	// Local: getters reflect the engine queue.
	SetRepeat(1) // all
	SetShuffle(1)
	if got := GetRepeat(); got != "all" {
		t.Fatalf("local GetRepeat = %q, want all", got)
	}
	if !GetShuffle() {
		t.Fatal("local GetShuffle = false, want true")
	}

	// Routed: getters reflect the remote host snapshot, NOT the local queue
	// (local is all/true; the host reports one/false).
	mu.Lock()
	remoteCli = control.NewClient("http://127.0.0.1:1", "", "")
	remoteSt = control.State{Repeat: "one", Shuffle: false}
	mu.Unlock()

	if got := GetRepeat(); got != "one" {
		t.Fatalf("routed GetRepeat = %q, want the host snapshot \"one\" (not local \"all\")", got)
	}
	if GetShuffle() {
		t.Fatal("routed GetShuffle = true, want the host snapshot false (not local true)")
	}
}

// TestCycleRepeatAndToggleShuffleWired proves the B1 host-both wiring: the
// engineCommands legacy verbs (CycleRepeat/ToggleShuffle) are wired alongside
// the SET variants and drive the engine queue (off->all->one->off; shuffle
// flip), visible through engineState.
func TestCycleRepeatAndToggleShuffleWired(t *testing.T) {
	t.Cleanup(resetEngineQueue)
	resetEngineQueue()

	cmds := engineCommands()
	if cmds.CycleRepeat == nil || cmds.ToggleShuffle == nil {
		t.Fatal("CycleRepeat/ToggleShuffle hooks must be wired (B1)")
	}
	if cmds.SetRepeat == nil || cmds.SetShuffle == nil {
		t.Fatal("SetRepeat/SetShuffle hooks must stay wired")
	}

	if got := engineState().Repeat; got != "off" {
		t.Fatalf("baseline repeat = %q, want off", got)
	}
	for _, want := range []string{"all", "one", "off"} {
		cmds.CycleRepeat()
		if got := engineState().Repeat; got != want {
			t.Fatalf("after cycle repeat = %q, want %q", got, want)
		}
	}
	cmds.ToggleShuffle()
	if !engineState().Shuffle {
		t.Fatal("ToggleShuffle must turn shuffle on")
	}
	cmds.ToggleShuffle()
	if engineState().Shuffle {
		t.Fatal("ToggleShuffle must turn shuffle back off")
	}
}

// TestLogoutClearsRemoteRouting proves B8: Logout tears down the Connect
// controller routing (remoteCli/remoteSt/remoteAddr/remoteStop) before wiping
// the session, so commands can't keep routing to the old device under a new
// account.
func TestLogoutClearsRemoteRouting(t *testing.T) {
	t.Cleanup(clearRemote)
	t.Cleanup(resetControlGlobals)

	srv := startTestControlServer(t)
	rc := control.NewClient("http://"+srv.Addr(), "", "")

	mu.Lock()
	remoteCli = rc
	remoteSt = control.State{State: "playing"}
	remoteAddr = srv.Addr()
	remoteStop = make(chan struct{})
	mu.Unlock()

	if routedRemote() == nil {
		t.Fatal("precondition: a device must be routed")
	}

	Logout()

	if routedRemote() != nil {
		t.Fatal("Logout must clear remoteCli (else commands route to the stale device)")
	}
	if got := ConnectedDevice(); got != "" {
		t.Fatalf("Logout must clear remoteAddr, got %q", got)
	}
	mu.Lock()
	stopCleared := remoteStop == nil
	stateCleared := remoteSt.State == ""
	mu.Unlock()
	if !stopCleared {
		t.Fatal("Logout must clear remoteStop")
	}
	if !stateCleared {
		t.Fatal("Logout must clear the cached remote state")
	}
}

// TestMobileStartServerKeepsToken proves B9: the LAN control-server rebind
// carries the configured token, so a token-protected control API is NOT
// downgraded to token-less (same-account-only) auth when bound onto all
// interfaces.
func TestMobileStartServerKeepsToken(t *testing.T) {
	t.Setenv("OPENDEEZER_CONTROL_TOKEN", "s3cr3t")
	t.Cleanup(resetControlGlobals)

	srv := mobileStartServer("127.0.0.1:0")
	if srv == nil {
		t.Fatal("mobileStartServer returned nil")
	}
	t.Cleanup(srv.Close)
	base := "http://" + srv.Addr()

	// No token -> rejected (the rebind must not downgrade to token-less auth).
	if _, err := control.NewClient(base, "", "").Status(); err == nil {
		t.Fatal("token-less request accepted: LAN rebind downgraded auth (B9 regression)")
	}
	// A same-account header alone must NOT unlock a token-protected server
	// (token takes priority over same-account in the server's auth switch).
	if _, err := control.NewClient(base, "", "someacct").Status(); err == nil {
		t.Fatal("same-account request accepted despite a configured token (B9 regression)")
	}
	// The correct token is accepted.
	if _, err := control.NewClient(base, "s3cr3t", "").Status(); err != nil {
		t.Fatalf("correct-token request rejected: %v", err)
	}
}

// TestStartControlServerNoDeadPublish proves B11: when Start fails (port already
// bound) startControlServer returns nil and publishes nothing, so ctrlSrv never
// holds a listener-less handle that mobileEnsureLANServer would treat as live.
func TestStartControlServerNoDeadPublish(t *testing.T) {
	t.Cleanup(resetControlGlobals)

	// Occupy a loopback port so the control server's net.Listen fails.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}
	defer ln.Close()
	occupied := ln.Addr().String()

	mu.Lock()
	ctrlSrv = nil
	mu.Unlock()

	if s := startControlServer(control.Config{Addr: occupied}, nil, false); s != nil {
		t.Fatal("startControlServer must return nil when Start fails")
	}
	mu.Lock()
	published := ctrlSrv
	mu.Unlock()
	if published != nil {
		t.Fatal("a server that failed to Start must NOT be published to ctrlSrv (B11)")
	}
}

// TestMobileServerListening proves the B11 liveness probe: a started server
// reports listening; once closed it does not — so mobileEnsureLANServer rebuilds
// a dead server instead of handing it back.
func TestMobileServerListening(t *testing.T) {
	if mobileServerListening(nil) {
		t.Fatal("nil server must not report listening")
	}
	s := startTestControlServer(t)
	if !mobileServerListening(s) {
		t.Fatal("a started server must report listening")
	}
	s.Close()
	deadline := time.Now().Add(2 * time.Second)
	for mobileServerListening(s) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if mobileServerListening(s) {
		t.Fatal("a closed server must not report listening (B11 dead-server guard)")
	}
}

// TestIsErroredFinish proves the B4 gate used by BOTH the onFinish closure and
// noteTrackFinished: an errored finish (Errored state, or a stored LastError)
// with ~0 played is classified as "skip" (no history, no advance), while a clean
// finish, a gapless promote (Playing, no error) and a mid-track error that
// already played real audio are all classified as genuine finishes.
func TestIsErroredFinish(t *testing.T) {
	cases := []struct {
		name    string
		state   audio.State
		lastErr string
		posMS   int64
		want    bool
	}{
		{"clean stopped finish", audio.Stopped, "", 0, false},
		{"gapless promote (playing, no error)", audio.Playing, "", 0, false},
		{"errored finish, nothing played", audio.Stopped, "decode failed", 0, true},
		{"device-loss Errored, nothing played", audio.Errored, "device lost", 0, true},
		{"errored state, nothing played, empty msg", audio.Errored, "", 0, true},
		{"error after real playback", audio.Stopped, "late decode error", 65000, false},
		{"errored state but played a lot", audio.Errored, "", 65000, false},
	}
	for _, c := range cases {
		if got := isErroredFinish(c.state, c.lastErr, c.posMS); got != c.want {
			t.Errorf("%s: isErroredFinish(%v,%q,%d) = %v, want %v",
				c.name, c.state, c.lastErr, c.posMS, got, c.want)
		}
	}
	// erroredFinish tolerates a nil player (pre-Init / tests): never errored.
	if erroredFinish(nil) {
		t.Fatal("erroredFinish(nil) must be false")
	}
}

// TestFavoriteIDsJSONNoClient proves FavoriteIDsJSON degrades to a valid empty
// JSON array (never null/error) when not logged in.
func TestFavoriteIDsJSONNoClient(t *testing.T) {
	if got := FavoriteIDsJSON(); got != "[]" {
		t.Fatalf("FavoriteIDsJSON with no client = %q, want []", got)
	}
}
