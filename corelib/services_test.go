package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/control"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/history"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/queue"
)

// waitServing blocks until the server at addr returns an HTTP response, which
// proves http.Server.Serve has run past trackListener — so a subsequent Close
// reliably reclaims the port for the same-port rebind. A bare TCP dial isn't
// enough: the kernel completes the handshake from net.Listen's accept queue
// before Serve's goroutine registers the listener. Production never hits this —
// the control server has served for a long time before an account switch closes
// it — but the test opens and closes back-to-back.
func waitServing(t *testing.T, addr string) {
	t.Helper()
	cl := &http.Client{Timeout: 200 * time.Millisecond}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		// Any HTTP status (even 401/403/404) proves the handler chain ran.
		if resp, err := cl.Get("http://" + addr + "/whoami"); err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("control server at %s never started serving", addr)
}

// TestEngineQueueAutoAdvance proves a loaded playlist walks every track on
// successive natural finishes (the engine-side auto-advance), then stops at the
// end under the default RepeatOff. This is the core remote/control deliverable:
// playlist playback advances through ALL tracks instead of stopping after one.
func TestEngineQueueAutoAdvance(t *testing.T) {
	ts := []deezer.Track{{ID: "1"}, {ID: "2"}, {ID: "3"}}

	queueMu.Lock()
	engineQ.Set(ts, 0)
	queueMu.Unlock()
	setCurrentTrack(ts[0]) // the player started on the first track

	var played []string
	for {
		// Simulate the player's onFinish for the current track.
		next, ok := engineQueueAdvance(currentTrack().ID)
		if !ok {
			break
		}
		played = append(played, next.ID)
		setCurrentTrack(next) // the player is now on `next`
	}

	want := []string{"2", "3"}
	if len(played) != len(want) {
		t.Fatalf("auto-advance visited %v, want %v", played, want)
	}
	for i := range want {
		if played[i] != want[i] {
			t.Fatalf("auto-advance[%d]=%q want %q (full: %v)", i, played[i], want[i], played)
		}
	}

	// A further finish at the end (RepeatOff) must not advance.
	if _, ok := engineQueueAdvance(currentTrack().ID); ok {
		t.Fatal("auto-advance past the end should stop under RepeatOff")
	}
}

// TestEngineQueueRepeatAllWraps proves a natural finish at the end wraps back to
// the first track when the queue is in RepeatAll (honored for free via the
// shared queue.Queue).
func TestEngineQueueRepeatAllWraps(t *testing.T) {
	queueMu.Lock()
	engineQ.Set([]deezer.Track{{ID: "a"}, {ID: "b"}}, 1) // start on the last track
	engineQ.SetRepeat(queue.RepeatAll)
	queueMu.Unlock()
	setCurrentTrack(deezer.Track{ID: "b"})

	next, ok := engineQueueAdvance("b")
	if !ok || next.ID != "a" {
		t.Fatalf("RepeatAll finish at end -> %q ok=%v, want wrap to \"a\"", next.ID, ok)
	}

	// Restore default so other tests see RepeatOff.
	queueMu.Lock()
	engineQ.SetRepeat(queue.RepeatOff)
	queueMu.Unlock()
}

// TestEngineQueueAdvanceIgnoresAdHocTrack proves that finishing a track which is
// NOT the engine queue's current track (e.g. a direct DZPlay that bypassed the
// engine queue) does not trigger a stale auto-advance into a loaded playlist.
func TestEngineQueueAdvanceIgnoresAdHocTrack(t *testing.T) {
	queueMu.Lock()
	engineQ.Set([]deezer.Track{{ID: "10"}, {ID: "11"}}, 0)
	queueMu.Unlock()

	if _, ok := engineQueueAdvance("999"); ok {
		t.Fatal("finish of a non-queued track must not auto-advance the engine queue")
	}
}

// resetEngineQueue restores the engine queue to its pristine (unsynced) state
// so tests can't leak queue contents / repeat / shuffle into each other.
func resetEngineQueue() {
	queueMu.Lock()
	engineQ.Set(nil, 0)
	engineQ.SetRepeat(queue.RepeatOff)
	engineQ.SetShuffle(false)
	queueMu.Unlock()
}

// TestQueueSyncReflectsInEngineState proves a GUI queue synced via the
// DZQueueSet/DZQueueSetIndex path shows up on /status (State.Queue) with the
// cursor where the GUI put it — previously engineState never reported a queue.
func TestQueueSyncReflectsInEngineState(t *testing.T) {
	t.Cleanup(resetEngineQueue)

	js := `[
		{"id":"1","name":"One","durationMs":180000,"artistLine":"Solo Artist","albumName":"Alpha"},
		{"id":"2","name":"Two","durationMs":200000,"artists":[{"id":"77","name":"Duo"}],"albumName":"Beta"},
		{"id":"","name":"dropped: no id"}
	]`
	ts, err := queueTracksFromJSON(js)
	if err != nil {
		t.Fatalf("queueTracksFromJSON: %v", err)
	}
	engineQueueSet(ts)
	engineQueueSetIndex(1)

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
	if got := engineQueueIndex(); got != 1 {
		t.Fatalf("engineQueueIndex = %d, want 1", got)
	}

	// Clearing (the "[]" payload) removes the queue from /status again.
	ts, err = queueTracksFromJSON(`[]`)
	if err != nil {
		t.Fatalf("queueTracksFromJSON(\"[]\"): %v", err)
	}
	engineQueueSet(ts)
	if st := engineState(); len(st.Queue) != 0 {
		t.Fatalf("cleared queue still reported: %+v", st.Queue)
	}
	if got := engineQueueIndex(); got != -1 {
		t.Fatalf("engineQueueIndex after clear = %d, want -1", got)
	}
}

// TestQueueTracksFromJSONRejectsBadPayload proves DZQueueSet's 0-return path:
// malformed JSON must error instead of silently clearing the queue.
func TestQueueTracksFromJSONRejectsBadPayload(t *testing.T) {
	if _, err := queueTracksFromJSON(`{"not":"an array"}`); err == nil {
		t.Fatal("non-array payload must be rejected")
	}
	if _, err := queueTracksFromJSON(`[{"id":`); err == nil {
		t.Fatal("truncated payload must be rejected")
	}
	// "" and "null" are the documented clear forms.
	if ts, err := queueTracksFromJSON(""); err != nil || len(ts) != 0 {
		t.Fatalf("empty payload = (%v, %v), want empty clear", ts, err)
	}
	if ts, err := queueTracksFromJSON("null"); err != nil || len(ts) != 0 {
		t.Fatalf("null payload = (%v, %v), want empty clear", ts, err)
	}
}

// TestRepeatShuffleReflectInEngineState proves the DZSetRepeat / DZSetShuffle
// persistence path: engineState reports the last set values instead of the old
// hard-coded Repeat:"off", Shuffle:false.
func TestRepeatShuffleReflectInEngineState(t *testing.T) {
	t.Cleanup(resetEngineQueue)

	engineSetRepeat("all")
	engineSetShuffle(true)
	st := engineState()
	if st.Repeat != "all" || !st.Shuffle {
		t.Fatalf("engineState repeat=%q shuffle=%v, want all/true", st.Repeat, st.Shuffle)
	}

	engineSetRepeat("one")
	if st := engineState(); st.Repeat != "one" {
		t.Fatalf("engineState repeat=%q, want one", st.Repeat)
	}

	engineSetRepeat("off")
	engineSetShuffle(false)
	st = engineState()
	if st.Repeat != "off" || st.Shuffle {
		t.Fatalf("engineState repeat=%q shuffle=%v, want off/false (default wire shape)", st.Repeat, st.Shuffle)
	}
}

// TestEngineNextPrevWalkSyncedQueue proves the control API's /next and /prev
// walk a queue synced through the DZQueueSet path (cursor movement is the queue
// deliverable; play itself needs a logged-in client + audio device).
func TestEngineNextPrevWalkSyncedQueue(t *testing.T) {
	t.Cleanup(resetEngineQueue)

	ts, err := queueTracksFromJSON(`[{"id":"a"},{"id":"b"},{"id":"c"}]`)
	if err != nil {
		t.Fatalf("queueTracksFromJSON: %v", err)
	}
	engineQueueSet(ts)

	engineNext()
	if got := engineQueueIndex(); got != 1 {
		t.Fatalf("after Next: index = %d, want 1", got)
	}
	engineNext()
	if got := engineQueueIndex(); got != 2 {
		t.Fatalf("after Next x2: index = %d, want 2", got)
	}
	engineNext() // end of queue under RepeatOff: stays put
	if got := engineQueueIndex(); got != 2 {
		t.Fatalf("Next past the end moved the cursor to %d, want 2", got)
	}
	enginePrev()
	if got := engineQueueIndex(); got != 1 {
		t.Fatalf("after Prev: index = %d, want 1", got)
	}
	enginePrev()
	if got := engineQueueIndex(); got != 0 {
		t.Fatalf("after Prev x2: index = %d, want 0", got)
	}
}

// TestRefreshControlServerSwapsStaleClient proves that an account switch rebuilds
// the control server around the new client (so it stops serving the old
// account's library) and is a no-op for the same client.
func TestRefreshControlServerSwapsStaleClient(t *testing.T) {
	old := deezer.New("old-arl")
	srv := control.New(
		control.Config{Addr: "127.0.0.1:0"},
		engineState, engineAccount, engineCommands(), old,
	)
	if err := srv.Start(); err != nil {
		t.Fatalf("start control server: %v", err)
	}
	waitServing(t, srv.Addr()) // so refreshControlServer's Close reclaims the port
	defer func() {
		mu.Lock()
		s := ctrlSrv
		ctrlSrv, ctrlSrvUserID = nil, ""
		mu.Unlock()
		if s != nil {
			s.Close()
		}
	}()

	mu.Lock()
	ctrlSrv = srv
	ctrlCfg = control.Config{Addr: "127.0.0.1:0"}
	ctrlSrvUserID = old.Account().UserID
	mu.Unlock()

	// Same account: no-op (must not tear down / rebuild). Staleness now keys on
	// the account UserID, not the *deezer.Client pointer, so a fresh client for
	// the same account stays a no-op.
	refreshControlServer(old)
	mu.Lock()
	unchanged := ctrlSrv == srv && ctrlSrvUserID == old.Account().UserID
	mu.Unlock()
	if !unchanged {
		t.Fatal("refreshControlServer for the same account must be a no-op")
	}

	// Account switch: simulate the tracked account being a different one (in
	// production both clients are logged in with real, distinct UserIDs; the test
	// clients aren't logged in, so seed the tracked id to force the mismatch).
	mu.Lock()
	ctrlSrvUserID = "old-account-uid"
	mu.Unlock()
	fresh := deezer.New("new-arl")
	refreshControlServer(fresh)
	mu.Lock()
	swapped := ctrlSrv != nil && ctrlSrv != srv && ctrlSrvUserID == fresh.Account().UserID
	mu.Unlock()
	if !swapped {
		t.Fatal("refreshControlServer did not rebuild the control server for the new account")
	}
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
// DZQueueVersion exposes for GUI resync).
func TestEngineQueueInsertNextAndAppend(t *testing.T) {
	t.Cleanup(resetEngineQueue)

	engineQueueSet([]deezer.Track{{ID: "1"}, {ID: "2"}, {ID: "3"}})
	v0 := engineQueueVersion()

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
	if engineQueueIndex() != 0 {
		t.Fatalf("adds moved the cursor to %d, want 0", engineQueueIndex())
	}
	if v1 := engineQueueVersion(); v1 <= v0 {
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

	engineQueueSet([]deezer.Track{{ID: "a"}, {ID: "b"}, {ID: "c"}})

	if tr, err := engineQueueSelect(1); err != nil || tr.ID != "b" {
		t.Fatalf("engineQueueSelect(1) = (%q, %v), want (b, nil)", tr.ID, err)
	}
	tr, err := engineQueueSelect(2)
	if err != nil || tr.ID != "c" {
		t.Fatalf("engineQueueSelect(2) = (%q, %v), want (c, nil)", tr.ID, err)
	}
	if engineQueueIndex() != 2 {
		t.Fatalf("cursor = %d after select, want 2", engineQueueIndex())
	}
	// Prev retraces to the row that was playing before the jump.
	enginePrev()
	if engineQueueIndex() != 1 {
		t.Fatalf("Prev after jump retraced to %d, want 1", engineQueueIndex())
	}
	if _, err := engineQueueSelect(3); err == nil {
		t.Fatal("out-of-range select must error")
	}
	if _, err := engineQueueSelect(-1); err == nil {
		t.Fatal("negative select must error")
	}
}

// TestEngineQueueSetIndexAlignsWithoutHistory proves the GUI cursor-sync path
// (DZQueueSetIndex → engineQueueSetIndex → queue.AlignIndex) records no history,
// so a remote Prev after alignment never jumps to a never-played row 0.
func TestEngineQueueSetIndexAlignsWithoutHistory(t *testing.T) {
	t.Cleanup(resetEngineQueue)

	engineQueueSet([]deezer.Track{{ID: "a"}, {ID: "b"}, {ID: "c"}})
	engineQueueSetIndex(2) // GUI says "I'm playing row 2" — pure alignment
	if engineQueueIndex() != 2 {
		t.Fatalf("cursor = %d after alignment, want 2", engineQueueIndex())
	}
	enginePrev()
	if engineQueueIndex() == 0 {
		t.Fatal("Prev after alignment jumped to the never-played row 0")
	}
	if engineQueueIndex() != 1 {
		t.Fatalf("Prev after alignment stepped to %d, want linear 1", engineQueueIndex())
	}
}

// TestEngineQueueJumpRequiresEngine proves QueueJump fails cleanly (and leaves
// the cursor alone) when no client/player is up, instead of half-jumping.
func TestEngineQueueJumpRequiresEngine(t *testing.T) {
	t.Cleanup(resetEngineQueue)

	engineQueueSet([]deezer.Track{{ID: "a"}, {ID: "b"}})
	if err := engineQueueJump(1); err == nil {
		t.Fatal("QueueJump without a logged-in engine must error")
	}
	if engineQueueIndex() != 0 {
		t.Fatalf("failed jump moved the cursor to %d, want 0", engineQueueIndex())
	}
}

// TestEngineQueueRemoveGuards proves QueueRemove rejects the playing row and
// out-of-range indices, and otherwise edits the queue /status reports.
func TestEngineQueueRemoveGuards(t *testing.T) {
	t.Cleanup(resetEngineQueue)

	engineQueueSet([]deezer.Track{{ID: "a"}, {ID: "b"}, {ID: "c"}})
	engineQueueSetIndex(1)

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
	if engineQueueIndex() != 1 {
		t.Fatalf("cursor = %d after removing a later row, want 1", engineQueueIndex())
	}
}

// TestEngineQueueMoveFollowsCursor proves QueueMove reorders the queue while
// the cursor keeps following the playing track, and rejects bad indices.
func TestEngineQueueMoveFollowsCursor(t *testing.T) {
	t.Cleanup(resetEngineQueue)

	engineQueueSet([]deezer.Track{{ID: "a"}, {ID: "b"}, {ID: "c"}}) // playing "a" (cursor 0)

	if err := engineQueueMove(2, 0); err != nil {
		t.Fatalf("move failed: %v", err)
	}
	st := engineState()
	got := []string{st.Queue[0].ID, st.Queue[1].ID, st.Queue[2].ID}
	if got[0] != "c" || got[1] != "a" || got[2] != "b" {
		t.Fatalf("queue after move = %v, want [c a b]", got)
	}
	if engineQueueIndex() != 1 {
		t.Fatalf("cursor = %d, want 1 (still on the playing track)", engineQueueIndex())
	}
	if err := engineQueueMove(0, 0); err != nil {
		t.Fatalf("from == to must be a no-op success, got %v", err)
	}
	if err := engineQueueMove(0, 9); err == nil {
		t.Fatal("out-of-range move must error")
	}
}

// TestPlayCommandsRequireEngine proves the album/mix commands surface a clean
// error before login instead of panicking or silently doing nothing (the
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
// shipped-GUI flow (DZPreload WITHOUT a DZQueueSet sync): after the player
// gaplessly promotes the armed preload, now-playing moves onto the preloaded
// track, the outgoing track lands in history exactly once, and the promoted
// track is recorded on its own later finish (previously /status stayed on the
// old track, which was then recorded twice while the new one never was).
func TestGaplessPromoteUnsyncedQueueUsesArmedPreload(t *testing.T) {
	resetEngineQueue() // this test depends on an EMPTY (unsynced) engine queue
	t.Cleanup(resetEngineQueue)
	t.Cleanup(func() { clearPreloadedTrack(); setCurrentTrack(deezer.Track{}) })
	setHistoryStore(history.New(filepath.Join(t.TempDir(), "history.jsonl")))

	a := deezer.Track{ID: "A", Name: "First", DurationMS: 3000}
	b := deezer.Track{ID: "B", Name: "Second", DurationMS: 4000}
	setCurrentTrack(a)   // playing A; the engine queue stays empty (unsynced)
	setPreloadedTrack(b) // the GUI armed a preload for B (DZPreload)

	// A drains; the player promotes B and keeps playing. The onFinish callback
	// records A (noteTrackFinished), then runs the promote bookkeeping.
	noteTrackFinished()
	if engineSyncOnGaplessPromote() {
		t.Fatal("an unsynced engine queue must not own the finish (the GUI's own queue advances)")
	}
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
// walks the cursor + now-playing along the queue and reports the finish as
// engine-owned (so the GUI's finished counter is not bumped as well).
func TestGaplessPromoteSyncedQueueStillWalks(t *testing.T) {
	t.Cleanup(resetEngineQueue)
	t.Cleanup(func() { clearPreloadedTrack(); setCurrentTrack(deezer.Track{}) })

	a := deezer.Track{ID: "A", DurationMS: 3000}
	b := deezer.Track{ID: "B", DurationMS: 4000}
	queueMu.Lock()
	engineQ.Set([]deezer.Track{a, b}, 0)
	queueMu.Unlock()
	setCurrentTrack(a)
	setPreloadedTrack(b)

	if !engineSyncOnGaplessPromote() {
		t.Fatal("a synced+aligned engine queue must own the promote")
	}
	if got := currentTrack().ID; got != "B" {
		t.Fatalf("currentTrack after owned promote = %q, want B", got)
	}
	if got := engineQueueIndex(); got != 1 {
		t.Fatalf("queue cursor after owned promote = %d, want 1", got)
	}
	if _, armed := takePreloadedTrack(); armed {
		t.Fatal("an owned promote must also consume the preload stash")
	}
}

// TestRepeatShuffleToggleClearsArmedPreload proves DZSetRepeat / DZSetShuffle
// invalidate an armed preload exactly like every queue-edit path does: after a
// toggle the stale linear-next must not gapless-promote (repeat-one replays
// the CURRENT track; a reshuffle changes the upcoming one).
func TestRepeatShuffleToggleClearsArmedPreload(t *testing.T) {
	resetEngineQueue() // this test depends on an EMPTY (unsynced) engine queue
	t.Cleanup(resetEngineQueue)
	t.Cleanup(func() { clearPreloadedTrack(); setCurrentTrack(deezer.Track{}) })

	setCurrentTrack(deezer.Track{ID: "A", DurationMS: 3000})

	setPreloadedTrack(deezer.Track{ID: "stale-next", DurationMS: 3000})
	engineSetRepeat("one")
	if _, armed := takePreloadedTrack(); armed {
		t.Fatal("engineSetRepeat must clear an armed preload")
	}

	setPreloadedTrack(deezer.Track{ID: "stale-next", DurationMS: 3000})
	engineSetShuffle(true)
	if _, armed := takePreloadedTrack(); armed {
		t.Fatal("engineSetShuffle must clear an armed preload")
	}

	// With the preload cleared, a subsequent finish cannot promote the stale
	// next: now-playing stays put and the finish is not engine-owned.
	if engineSyncOnGaplessPromote() {
		t.Fatal("a cleared preload must not make the finish engine-owned")
	}
	if got := currentTrack().ID; got != "A" {
		t.Fatalf("cleared preload still moved now-playing to %q, want A", got)
	}
}

// TestStopRecordsOutgoingListenOnce proves the Stop paths (DZStop + the
// control-host Stop handler) record the in-progress listen exactly once: the
// closures call noteTrackTransition BEFORE p.Stop() while the player is still
// Playing, and the state guard makes the second Stop — the player is already
// Stopped then — record nothing.
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
