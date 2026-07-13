package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cycl0o0/OpenDeezer/v3/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/v3/internal/control"
	"github.com/Cycl0o0/OpenDeezer/v3/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/v3/internal/history"
	"github.com/Cycl0o0/OpenDeezer/v3/internal/mediacache"
	"github.com/Cycl0o0/OpenDeezer/v3/internal/queue"
)

// clearRoutedRemote resets the Connect routing globals so a test that installs a
// fake remote can't leak it into another test's routedRemote() reads.
func clearRoutedRemote() {
	mu.Lock()
	remoteCli = nil
	remoteSt = control.State{}
	remoteAddr = ""
	mu.Unlock()
}

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

// TestEpisodeListenRecordsKind proves the episode play path stamps
// Kind="episode" on the recorded history entry (so a native history screen
// replays it via DZPlayEpisode), a song stays Kind="", and the kind reaches the
// /history/recent JSON wire — the exact shape DZHistoryRecentJSON serves.
func TestEpisodeListenRecordsKind(t *testing.T) {
	t.Cleanup(func() { setCurrentTrack(deezer.Track{}) })
	setHistoryStore(history.New(filepath.Join(t.TempDir(), "history.jsonl")))

	// Play an episode, then swap away: the transition (player still Playing)
	// records the outgoing episode via currentTrackSnapshot -> recordListen.
	setCurrentEpisode(deezer.Track{ID: "ep1", Name: "Episode", DurationMS: 600000})
	recordTransition(audio.Playing, 120000)
	entries := waitHistory(t, 1)
	if entries[0].TrackID != "ep1" || entries[0].Kind != history.KindEpisode {
		t.Fatalf("episode listen = %+v, want trackId=ep1 Kind=episode", entries[0])
	}

	// A song recorded next carries no kind (setCurrentTrack clears it).
	setCurrentTrack(deezer.Track{ID: "s1", Name: "Song", DurationMS: 200000})
	recordTransition(audio.Playing, 90000)
	entries = waitHistory(t, 2)
	if entries[0].TrackID != "s1" || entries[0].Kind != "" {
		t.Fatalf("song listen = %+v, want trackId=s1 empty Kind", entries[0])
	}

	// The kind reaches the /history/recent JSON wire that DZHistoryRecentJSON serves.
	raw, err := engineHistoryRecent(10)
	if err != nil {
		t.Fatalf("engineHistoryRecent: %v", err)
	}
	var wire []history.Entry
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal recent JSON: %v", err)
	}
	byID := map[string]history.Entry{}
	for _, e := range wire {
		byID[e.TrackID] = e
	}
	if byID["ep1"].Kind != history.KindEpisode {
		t.Fatalf("recent JSON episode kind = %q, want episode (raw=%s)", byID["ep1"].Kind, raw)
	}
	if byID["s1"].Kind != "" {
		t.Fatalf("recent JSON song kind = %q, want empty", byID["s1"].Kind)
	}
}

// TestListenEntry proves the history-record guards: no id and sub-second
// listens are dropped (start-skips, pause/resume can't produce records), the
// listened time is clamped to the track duration, and the fields map through.
func TestListenEntry(t *testing.T) {
	if _, ok := listenEntry(deezer.Track{}, "", 5000); ok {
		t.Fatal("track without an id must not be recorded")
	}
	if _, ok := listenEntry(deezer.Track{ID: "1"}, "", 999); ok {
		t.Fatal("sub-second listens must not be recorded")
	}
	e, ok := listenEntry(deezer.Track{
		ID: "42", Name: "Song", AlbumName: "Album", DurationMS: 3000,
		Artists: []deezer.Artist{{ID: "7", Name: "Artist"}},
	}, "", 10000)
	if !ok {
		t.Fatal("valid listen was dropped")
	}
	if e.TrackID != "42" || e.Title != "Song" || e.Artist != "Artist" || e.Album != "Album" {
		t.Fatalf("entry fields = %+v", e)
	}
	if e.Kind != "" {
		t.Fatalf("song entry Kind = %q, want empty", e.Kind)
	}
	if e.DurationPlayedSec != 3 {
		t.Fatalf("played sec = %d, want 3 (clamped to track duration)", e.DurationPlayedSec)
	}
	if e.StartedAt == 0 {
		t.Fatal("StartedAt must be derived, not left for Record's now()")
	}
	// An episode listen carries Kind="episode" so replay can route it.
	ep, ok := listenEntry(deezer.Track{ID: "9", Name: "Ep", DurationMS: 3000}, history.KindEpisode, 10000)
	if !ok || ep.Kind != history.KindEpisode {
		t.Fatalf("episode entry = %+v ok=%v, want Kind=episode", ep, ok)
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

// TestGetRepeatShuffleLocalVsRouted proves the B1 getters read the engine queue
// when playing locally and the routed device's snapshot when casting, so a
// casting host GUI reads the real modes (empty remote repeat falls back to off).
func TestGetRepeatShuffleLocalVsRouted(t *testing.T) {
	t.Cleanup(resetEngineQueue)
	t.Cleanup(clearRoutedRemote)

	// Local: reflects the engine queue.
	engineSetRepeat("all")
	engineSetShuffle(true)
	if got := currentRepeatMode(); got != "all" {
		t.Fatalf("local repeat = %q, want all", got)
	}
	if !currentShuffleOn() {
		t.Fatal("local shuffle = false, want true")
	}

	// Routed: reflects the remote snapshot, NOT the engine queue.
	rc := control.NewClient("http://127.0.0.1:1", "", "")
	mu.Lock()
	remoteCli = rc
	remoteSt = control.State{Repeat: "one", Shuffle: false}
	mu.Unlock()
	if got := currentRepeatMode(); got != "one" {
		t.Fatalf("routed repeat = %q, want the remote snapshot's one", got)
	}
	if currentShuffleOn() {
		t.Fatal("routed shuffle = true, want the remote snapshot's false")
	}

	// An empty remote repeat mode falls back to off.
	mu.Lock()
	remoteSt = control.State{Repeat: ""}
	mu.Unlock()
	if got := currentRepeatMode(); got != "off" {
		t.Fatalf("routed empty repeat = %q, want off fallback", got)
	}
}

// TestRepeatNotForwardedToRoutedRemote proves B2: with a device connected,
// setting/cycling repeat records locally but is NOT forwarded to the host (which
// would trap it looping its single-item queue), while shuffle IS still forwarded.
func TestRepeatNotForwardedToRoutedRemote(t *testing.T) {
	t.Cleanup(resetEngineQueue)
	t.Cleanup(clearRoutedRemote)

	var repeatCalls, shuffleCalls int32
	host := control.New(
		control.Config{Addr: "127.0.0.1:0"},
		func() control.State { return control.State{} },
		func() control.Account { return control.Account{} },
		control.Commands{
			SetRepeat:  func(string) { atomic.AddInt32(&repeatCalls, 1) },
			SetShuffle: func(bool) { atomic.AddInt32(&shuffleCalls, 1) },
		},
		nil,
	)
	if err := host.Start(); err != nil {
		t.Fatalf("start fake host: %v", err)
	}
	defer host.Close()
	waitServing(t, host.Addr())

	rc := control.NewClient("http://"+host.Addr(), "", "")
	mu.Lock()
	remoteCli = rc
	remoteSt = control.State{}
	mu.Unlock()

	cmds := engineCommands()
	cmds.SetRepeat("all") // records engineQ=all, must NOT forward (B2)
	cmds.CycleRepeat()    // all -> one, must NOT forward (B2)
	if n := atomic.LoadInt32(&repeatCalls); n != 0 {
		t.Fatalf("repeat forwarded to the routed host %d times, want 0 (B2)", n)
	}

	// The mode is still recorded on the engine queue (drives the engine's own
	// auto-advance, which honors repeat when it feeds the host).
	queueMu.Lock()
	local := engineQ.Repeat().String()
	queueMu.Unlock()
	if local != "one" {
		t.Fatalf("engine repeat = %q, want one (off->all->one)", local)
	}

	// Shuffle is still forwarded to the host.
	cmds.SetShuffle(true)
	if n := atomic.LoadInt32(&shuffleCalls); n != 1 {
		t.Fatalf("shuffle forwarded %d times, want 1", n)
	}
}

// TestAutoAdvanceBumpsPlaySeqAndGuardDropsStale proves B3: engineAdvanceOnFinish
// claims a play-seq for its async launch, and a later user play bumps past it so
// the in-flight resolve is recognised as superseded (and dropped) instead of
// clobbering the user's track.
func TestAutoAdvanceBumpsPlaySeqAndGuardDropsStale(t *testing.T) {
	t.Cleanup(resetEngineQueue)
	t.Cleanup(func() { setCurrentTrack(deezer.Track{}) })

	queueMu.Lock()
	engineQ.Set([]deezer.Track{{ID: "1"}, {ID: "2"}}, 0)
	queueMu.Unlock()
	setCurrentTrack(deezer.Track{ID: "1"})

	before := playSeq.Load()
	// No live client/player in tests: the async launch's goroutine no-ops, but
	// engineAdvanceOnFinish still advances the queue and bumps playSeq.
	if !engineAdvanceOnFinish() {
		t.Fatal("auto-advance should own the finish of the queue's current track")
	}
	seqAtLaunch := playSeq.Load()
	if seqAtLaunch == before {
		t.Fatal("engineAdvanceOnFinish must bump playSeq for its async launch (B3)")
	}
	if playSuperseded(seqAtLaunch) {
		t.Fatal("a resolve must not be superseded before any newer play")
	}
	// A user DZPlay/DZPlayEpisode bumps playSeq: the in-flight resolve is stale.
	playSeq.Add(1)
	if !playSuperseded(seqAtLaunch) {
		t.Fatal("a newer play must supersede the stale auto-advance resolve (B3)")
	}
}

// TestErroredFinishNotRecorded proves B4: a finish that carries a player error
// with ~0 played (a CDN/decode failure) is neither logged as a listen nor
// auto-advanced, while a clean finish (and an error deep into the track) records.
func TestErroredFinishNotRecorded(t *testing.T) {
	t.Cleanup(func() { setCurrentTrack(deezer.Track{}) })
	setHistoryStore(history.New(filepath.Join(t.TempDir(), "history.jsonl")))

	// The predicate: error + ~0 played => errored finish.
	if !isErroredFinish(audio.Errored, "", 0) {
		t.Fatal("Errored state with ~0 played is an errored finish")
	}
	if !isErroredFinish(audio.Stopped, "CDN 403", 0) {
		t.Fatal("a non-empty LastError with ~0 played is an errored finish")
	}
	if isErroredFinish(audio.Stopped, "CDN 403", 120000) {
		t.Fatal("an error deep into the track is a real partial listen, not an errored finish")
	}
	if isErroredFinish(audio.Playing, "", 0) {
		t.Fatal("a Playing gapless promote is never an errored finish")
	}
	if isErroredFinish(audio.Stopped, "", 200000) {
		t.Fatal("a clean finish (no error) is not an errored finish")
	}

	prev := deezer.Track{ID: "E", Name: "Failed", DurationMS: 200000}
	setCurrentTrack(prev)

	// An errored finish records nothing (recordFinished returns before spawning
	// the async write, so an immediate read is authoritative).
	recordFinished(prev, "", audio.Errored, "decode failed", 0, true)
	if got, _ := historyStore().Recent(10); len(got) != 0 {
		t.Fatalf("errored finish was recorded: %+v", got)
	}

	// A clean finish IS recorded.
	recordFinished(prev, "", audio.Stopped, "", 0, true)
	if entries := waitHistory(t, 1); entries[0].TrackID != "E" {
		t.Fatalf("clean finish recorded %q, want E", entries[0].TrackID)
	}
}

// TestSetRemoteStateIdentityChecked proves B7: setRemoteState applies only when
// the passed client is still the CURRENT remote — a late in-flight response from
// a device we've switched away from can neither overwrite state nor bump the
// finished counter for the wrong device.
func TestSetRemoteStateIdentityChecked(t *testing.T) {
	t.Cleanup(clearRoutedRemote)

	rcA := control.NewClient("http://127.0.0.1:1", "", "") // the OLD device
	rcB := control.NewClient("http://127.0.0.1:2", "", "") // the CURRENT device
	mu.Lock()
	remoteCli = rcB
	remoteSt = control.State{State: "playing", DurationMS: 1000, PositionMS: 900}
	startFinished := finished
	mu.Unlock()

	// Late response from A (no longer current): ignored — state and the finished
	// counter are unchanged.
	setRemoteState(rcA, control.State{State: "stopped", DurationMS: 1000, PositionMS: 1000})
	mu.Lock()
	stA, finA := remoteSt, finished
	mu.Unlock()
	if stA.State != "playing" {
		t.Fatalf("a late response from a non-current device overwrote B's state: %+v", stA)
	}
	if finA != startFinished {
		t.Fatalf("a non-current device bumped finished: %d -> %d", startFinished, finA)
	}

	// Current device B applies, and its playing->stopped-near-end bumps finished.
	setRemoteState(rcB, control.State{State: "stopped", DurationMS: 1000, PositionMS: 1000})
	mu.Lock()
	stB, finB := remoteSt, finished
	mu.Unlock()
	if stB.State != "stopped" {
		t.Fatalf("current-device response not applied: %+v", stB)
	}
	if finB != startFinished+1 {
		t.Fatalf("current device end-of-track did not bump finished: %d -> %d", startFinished, finB)
	}
}

// ---- Phase-4 corelib wiring: SetQueue hook, NotifyFinished, queue edits, offline ----

// resetOfflineGlobals clears the media-cache + pending-meta globals so an
// offline test can't leak a cache into a later test's preparePlan/persist path.
func resetOfflineGlobals() {
	mu.Lock()
	mediaCache = nil
	mu.Unlock()
	pendingMetaMu.Lock()
	pendingMeta = map[string]mediacache.StreamMeta{}
	pendingMetaMu.Unlock()
}

// TestSetQueueCommandHookReplacesEngineQueue proves the control command
// SetQueue(tracksJSON, index) parses the wire payload, replaces engineQ with the
// cursor at index, and surfaces in engineState (/status). Mirrors the mobile
// binding's SetQueue hook.
func TestSetQueueCommandHookReplacesEngineQueue(t *testing.T) {
	t.Cleanup(resetEngineQueue)
	clearRoutedRemote()

	cmds := engineCommands()
	if cmds.SetQueue == nil {
		t.Fatal("SetQueue hook must be wired")
	}
	if err := cmds.SetQueue(`[{"id":"a"},{"id":"b"},{"id":"c"}]`, 1); err != nil {
		t.Fatalf("SetQueue: %v", err)
	}
	st := engineState()
	got := make([]string, len(st.Queue))
	for i, tr := range st.Queue {
		got[i] = tr.ID
	}
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("queue after SetQueue = %v, want [a b c]", got)
	}
	if engineQueueIndex() != 1 {
		t.Fatalf("cursor after SetQueue(index=1) = %d, want 1", engineQueueIndex())
	}
	// A bad payload is surfaced as an error and leaves the queue untouched.
	if err := cmds.SetQueue(`{"not":"an array"}`, 0); err == nil {
		t.Fatal("SetQueue with a non-array payload must error")
	}
	if engineQueueIndex() != 1 {
		t.Fatalf("a failed SetQueue disturbed the cursor: %d", engineQueueIndex())
	}
}

// TestOnNaturalFinishNotifiesControlServer proves the factored onFinish callback
// publishes a natural end-of-track edge to the control server's /events
// subscribers (NotifyFinished) with the finishing track id AND bumps the GUI
// finished-counter when the engine queue didn't own the finish. The errored-vs-
// natural guard itself is covered by TestErroredFinishNotRecorded.
func TestOnNaturalFinishNotifiesControlServer(t *testing.T) {
	t.Cleanup(resetEngineQueue)
	setHistoryStore(history.New(filepath.Join(t.TempDir(), "history.jsonl")))
	clearRoutedRemote()

	// Empty engine queue: engineAdvanceOnFinish returns false, so the GUI
	// finished-counter path runs and NotifyFinished still fires.
	queueMu.Lock()
	engineQ.Set(nil, 0)
	queueMu.Unlock()

	srv := control.New(control.Config{Addr: "127.0.0.1:0"}, engineState, engineAccount, engineCommands(), deezer.New("arl"))
	if err := srv.Start(); err != nil {
		t.Fatalf("start control server: %v", err)
	}
	waitServing(t, srv.Addr())
	mu.Lock()
	ctrlSrv = srv
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		s := ctrlSrv
		ctrlSrv, ctrlSrvUserID = nil, ""
		mu.Unlock()
		if s != nil {
			s.Close()
		}
	})

	ready := make(chan struct{})
	gotFinished := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + srv.Addr() + "/events")
		if err != nil {
			close(ready)
			return
		}
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sawState, expectFinishedData := false, false
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: finished"):
				expectFinishedData = true
			case strings.HasPrefix(line, "event: state"):
				if !sawState {
					sawState = true
					close(ready) // subscriber is registered now
				}
			case expectFinishedData && strings.HasPrefix(line, "data: "):
				var fe struct {
					TrackID string `json:"trackId"`
				}
				_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &fe)
				gotFinished <- fe.TrackID
				return
			}
		}
	}()

	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("SSE subscriber never received the initial state event")
	}

	mu.Lock()
	before := finished
	mu.Unlock()
	setCurrentTrack(deezer.Track{ID: "t1", DurationMS: 200000})

	onNaturalFinish()

	select {
	case id := <-gotFinished:
		if id != "t1" {
			t.Fatalf("finished SSE event trackId = %q, want t1", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no finished SSE event after a natural finish")
	}

	mu.Lock()
	after := finished
	mu.Unlock()
	if after != before+1 {
		t.Fatalf("GUI finished-counter: %d -> %d, want +1 (unsynced queue)", before, after)
	}
}

// TestTracksFromInsertJSONAcceptsObjectOrArray proves DZQueueInsertNext's
// flexible decoder: a single wire object, an array of them, and rejects junk.
func TestTracksFromInsertJSONAcceptsObjectOrArray(t *testing.T) {
	if ts := tracksFromInsertJSON(`{"id":"solo","name":"S"}`); len(ts) != 1 || ts[0].ID != "solo" {
		t.Fatalf("single object decode = %+v, want one track id=solo", ts)
	}
	if ts := tracksFromInsertJSON(`[{"id":"a"},{"id":"b"}]`); len(ts) != 2 || ts[0].ID != "a" || ts[1].ID != "b" {
		t.Fatalf("array decode = %+v, want [a b]", ts)
	}
	if ts := tracksFromInsertJSON(``); len(ts) != 0 {
		t.Fatalf("empty payload = %+v, want none", ts)
	}
	if ts := tracksFromInsertJSON(`{"no":"id"}`); len(ts) != 0 {
		t.Fatalf("id-less object = %+v, want none", ts)
	}
	if ts := tracksFromInsertJSON(`not json`); len(ts) != 0 {
		t.Fatalf("junk payload = %+v, want none", ts)
	}
}

// TestQueueEditExportsMutateEngineQueue proves the GUI Up-Next edit path behind
// DZQueueInsertNext / DZQueueRemove / DZQueueMove: insert-next splices after the
// cursor preserving order, remove guards the playing row, move follows the
// cursor, and every edit bumps the content version for GUI resync.
func TestQueueEditExportsMutateEngineQueue(t *testing.T) {
	t.Cleanup(resetEngineQueue)
	clearRoutedRemote()

	engineQueueSet([]deezer.Track{{ID: "1"}, {ID: "2"}, {ID: "3"}}) // cursor 0
	v0 := engineQueueVersion()

	// Insert-next (via the export's decoder): array order preserved after cursor.
	engineQueueInsertNext(tracksFromInsertJSON(`[{"id":"X"},{"id":"Y"}]`))
	ids := func() []string {
		ts, _, _ := engineQueueSnapshot()
		out := make([]string, len(ts))
		for i, tr := range ts {
			out[i] = tr.ID
		}
		return out
	}
	if got, want := ids(), []string{"1", "X", "Y", "2", "3"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("after insert-next = %v, want %v", got, want)
	}
	if engineQueueVersion() <= v0 {
		t.Fatal("insert-next did not bump the queue version")
	}

	// Remove guards the playing row (index 0), removes a non-playing row.
	if err := engineQueueRemove(0); err == nil {
		t.Fatal("removing the playing row must error")
	}
	if err := engineQueueRemove(1); err != nil { // drop "X"
		t.Fatalf("engineQueueRemove(1): %v", err)
	}
	if got, want := ids(), []string{"1", "Y", "2", "3"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("after remove = %v, want %v", got, want)
	}

	// Move reorders without changing what's audible (cursor stays on "1").
	if err := engineQueueMove(3, 0); err != nil {
		t.Fatalf("engineQueueMove(3,0): %v", err)
	}
	if got, want := ids(), []string{"3", "1", "Y", "2"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("after move = %v, want %v", got, want)
	}
	if cur, _ := func() (deezer.Track, bool) {
		queueMu.Lock()
		defer queueMu.Unlock()
		return engineQ.Current()
	}(); cur.ID != "1" {
		t.Fatalf("move changed the audible track to %q, want 1", cur.ID)
	}
}

// TestPreparePlanPrefersCache proves the offline cached-plan preference: with a
// media cache holding a track's meta, preparePlan returns a zero-network,
// cache-sourced plan (CDNURL==""); a cache miss falls back to the network
// PrepareStream (which errors "not logged in" for this un-logged-in client).
func TestPreparePlanPrefersCache(t *testing.T) {
	t.Cleanup(resetOfflineGlobals)

	mc, err := mediacache.New(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatalf("mediacache.New: %v", err)
	}
	if err := mc.PutMeta("cached1.MP3_128", mediacache.StreamMeta{Format: "MP3_128", Encrypted: true, GainDB: -3}); err != nil {
		t.Fatalf("PutMeta: %v", err)
	}
	mu.Lock()
	mediaCache = mc
	mu.Unlock()

	c := deezer.New("arl") // not logged in: a cache hit must not need the network

	plan, err := preparePlan(c, "cached1")
	if err != nil {
		t.Fatalf("preparePlan(cached) unexpected error: %v", err)
	}
	if plan.CDNURL != "" {
		t.Fatalf("cache-sourced plan must have empty CDNURL, got %q", plan.CDNURL)
	}
	if plan.Format != "MP3_128" || !plan.Encrypted {
		t.Fatalf("cache-sourced plan meta mismatch: %+v", plan)
	}
	// A cache-sourced plan is already cached: preparePlan must NOT stash pending
	// meta for it (nothing to re-persist on finish).
	pendingMetaMu.Lock()
	_, stashed := pendingMeta["cached1"]
	pendingMetaMu.Unlock()
	if stashed {
		t.Fatal("preparePlan stashed pending meta for an already-cached plan")
	}

	// Cache miss on an un-logged-in client falls back to the network path.
	if _, err := preparePlan(c, "missing2"); err == nil {
		t.Fatal("preparePlan on a cache miss (not logged in) must error via PrepareStream")
	}
}

// TestStoreAndPersistPendingMeta proves the natural-finish offline persistence:
// a freshly-resolved encrypted plan is stashed then written into the cache on
// finish, while cache-sourced and preview plans are never stashed.
func TestStoreAndPersistPendingMeta(t *testing.T) {
	t.Cleanup(resetOfflineGlobals)

	mc, err := mediacache.New(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatalf("mediacache.New: %v", err)
	}
	mu.Lock()
	mediaCache = mc
	mu.Unlock()

	// Encrypted full plan with a real CDN URL: stashed, then persisted on finish.
	storePendingMeta("full1", &deezer.StreamPlan{CDNURL: "http://cdn/x", TrackID: "full1", Format: "MP3_320", GainDB: -2, Encrypted: true})
	// Preview and cache-sourced (CDNURL=="") plans must NOT be stashed.
	storePendingMeta("prev1", &deezer.StreamPlan{CDNURL: "http://cdn/p", TrackID: "prev1", Format: "MP3_128", Encrypted: false, Preview: true})
	storePendingMeta("cch1", &deezer.StreamPlan{CDNURL: "", TrackID: "cch1", Format: "FLAC", Encrypted: true})

	pendingMetaMu.Lock()
	_, hasFull := pendingMeta["full1"]
	_, hasPrev := pendingMeta["prev1"]
	_, hasCch := pendingMeta["cch1"]
	pendingMetaMu.Unlock()
	if !hasFull || hasPrev || hasCch {
		t.Fatalf("pending stash = full:%v preview:%v cached:%v, want full only", hasFull, hasPrev, hasCch)
	}

	persistFinishedMeta("full1")
	m, ok := mc.GetMetaForTrack("full1")
	if !ok || m.Format != "MP3_320" || !m.Encrypted {
		t.Fatalf("persisted meta = (%+v, %v), want MP3_320 encrypted", m, ok)
	}
	// The pending entry is consumed exactly once.
	pendingMetaMu.Lock()
	_, still := pendingMeta["full1"]
	pendingMetaMu.Unlock()
	if still {
		t.Fatal("persistFinishedMeta did not consume the pending entry")
	}
}

// TestFetchCiphertextToCache proves the offline download body path: raw bytes at
// a URL are fetched fully and committed to the cache under the plan key, then
// served back by Get.
func TestFetchCiphertextToCache(t *testing.T) {
	want := []byte("ciphertext-bytes-0123456789")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(want)
	}))
	defer srv.Close()

	mc, err := mediacache.New(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatalf("mediacache.New: %v", err)
	}
	key := "dl1.MP3_128"
	if err := fetchCiphertextToCache(mc, key, srv.URL); err != nil {
		t.Fatalf("fetchCiphertextToCache: %v", err)
	}
	rc, sz, ok := mc.Get(key)
	if !ok {
		t.Fatal("cache miss after fetchCiphertextToCache")
	}
	defer rc.Close()
	if sz != int64(len(want)) {
		t.Fatalf("cached size = %d, want %d", sz, len(want))
	}
	got := make([]byte, sz)
	if _, err := rc.Read(got); err != nil && err.Error() != "EOF" {
		t.Fatalf("read cached body: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("cached body = %q, want %q", got, want)
	}
}
