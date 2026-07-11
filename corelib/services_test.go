package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/Cycl0o0/OpenDeezer/internal/control"
	"github.com/Cycl0o0/OpenDeezer/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/internal/queue"
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
