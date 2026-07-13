package control_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Cycl0o0/OpenDeezer/v3/sdk/control"
	sdkdeezer "github.com/Cycl0o0/OpenDeezer/v3/sdk/deezer"
)

func TestNewClient(t *testing.T) {
	c := control.NewClient("http://127.0.0.1:9999", "", "")
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
}

func TestNewServerStartStop(t *testing.T) {
	dz := sdkdeezer.New("test-arl")
	srv := control.NewServer(
		// 127.0.0.1:0 → loopback, ephemeral port (passes the security check).
		control.Config{Addr: "127.0.0.1:0"},
		func() control.State { return control.State{State: "stopped"} },
		func() control.Account { return control.Account{} },
		control.Commands{},
		dz,
	)
	if srv == nil {
		t.Fatal("NewServer returned nil")
	}
	if err := srv.Start(); err != nil {
		t.Fatal("Start:", err)
	}
	defer srv.Close()

	addr := srv.Addr()
	if addr == "" {
		t.Error("Addr should be non-empty after Start")
	}
	t.Log("control server listening at", addr)
}

func TestNewServerNilDeezer(t *testing.T) {
	// Passing nil for the Deezer client is valid — browse endpoints return 503.
	srv := control.NewServer(
		control.Config{Addr: "127.0.0.1:0"},
		func() control.State { return control.State{State: "stopped"} },
		func() control.Account { return control.Account{} },
		control.Commands{},
		nil,
	)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	srv.Close()
}

// TestClientServerRoundTrip drives a real Server through every Client method
// added for control-API parity: exact repeat/shuffle set, cycle/toggle, sleep
// timer arm/cancel, and the equalizer bridge wired via Server.SetEQ.
func TestClientServerRoundTrip(t *testing.T) {
	var (
		gotRepeat            string
		gotShuffle           bool
		cycled, toggled      bool
		sleepMin             int
		sleepEOT, sleepXCncl bool
	)
	eq := control.EQState{Preset: "flat", GainsDB: make([]float64, 10),
		Presets: []string{"flat", "rock"}}
	srv := control.NewServer(
		control.Config{Addr: "127.0.0.1:0"},
		func() control.State { return control.State{State: "playing"} },
		func() control.Account { return control.Account{} },
		control.Commands{
			SetRepeat:        func(mode string) { gotRepeat = mode },
			SetShuffle:       func(on bool) { gotShuffle = on },
			CycleRepeat:      func() { cycled = true },
			ToggleShuffle:    func() { toggled = true },
			SetSleepTimer:    func(min int, eot bool) { sleepMin, sleepEOT = min, eot },
			CancelSleepTimer: func() { sleepXCncl = true },
		},
		nil, // browse endpoints disabled → Search/Playlists must error
	)
	// The EQ bridge must be wired before Start or /eq is 404.
	srv.SetEQ(&control.EQ{
		State:      func() control.EQState { return eq },
		SetEnabled: func(on bool) { eq.Enabled = on },
		SetPreset:  func(name string) error { eq.Preset = name; return nil },
	})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	c := control.NewClient("http://"+srv.Addr(), "", "")

	if _, err := c.SetRepeat("one"); err != nil || gotRepeat != "one" {
		t.Errorf("SetRepeat: err=%v repeat=%q", err, gotRepeat)
	}
	if _, err := c.SetShuffle(true); err != nil || !gotShuffle {
		t.Errorf("SetShuffle: err=%v on=%v", err, gotShuffle)
	}
	if _, err := c.CycleRepeat(); err != nil || !cycled {
		t.Errorf("CycleRepeat: err=%v cycled=%v", err, cycled)
	}
	if _, err := c.ToggleShuffle(); err != nil || !toggled {
		t.Errorf("ToggleShuffle: err=%v toggled=%v", err, toggled)
	}
	if _, err := c.SetSleepTimer(30, false); err != nil || sleepMin != 30 || sleepEOT {
		t.Errorf("SetSleepTimer: err=%v min=%d eot=%v", err, sleepMin, sleepEOT)
	}
	if _, err := c.CancelSleepTimer(); err != nil || !sleepXCncl {
		t.Errorf("CancelSleepTimer: err=%v cancelled=%v", err, sleepXCncl)
	}

	st, err := c.SetEQ(url.Values{"on": {"1"}, "preset": {"rock"}})
	if err != nil || !st.Enabled || st.Preset != "rock" {
		t.Errorf("SetEQ: err=%v state=%+v", err, st)
	}
	st, err = c.EQ()
	if err != nil || st.Preset != "rock" {
		t.Errorf("EQ: err=%v state=%+v", err, st)
	}

	// Without a Deezer client the browse endpoints answer 503 → error.
	if _, err := c.Search("daft punk"); err == nil {
		t.Error("Search should error when the server has no Deezer client")
	}
	if _, err := c.Playlists(); err == nil {
		t.Error("Playlists should error when the server has no Deezer client")
	}

	// RevokeSession on an unknown id is a safe no-op.
	srv.RevokeSession("no-such-session")
}

// TestClientServerQueueRoundTrip drives a real Server through the queue,
// album/mix and history methods: every command must reach its Commands hook
// with the exact arguments, and HistoryRecent must return the hook's raw JSON.
func TestClientServerQueueRoundTrip(t *testing.T) {
	var (
		addID                            string
		addNext                          bool
		jumped, removed                  = -1, -1
		movedFrom, movedTo               = -1, -1
		albumID, mixTrackID, mixArtistID string
		historyN                         int
	)
	srv := control.NewServer(
		control.Config{Addr: "127.0.0.1:0"},
		func() control.State { return control.State{State: "playing"} },
		func() control.Account { return control.Account{} },
		control.Commands{
			QueueAdd:      func(id string, next bool) error { addID, addNext = id, next; return nil },
			QueueJump:     func(i int) error { jumped = i; return nil },
			QueueRemove:   func(i int) error { removed = i; return nil },
			QueueMove:     func(from, to int) error { movedFrom, movedTo = from, to; return nil },
			PlayAlbum:     func(id string) error { albumID = id; return nil },
			PlayMixTrack:  func(id string) error { mixTrackID = id; return nil },
			PlayMixArtist: func(id string) error { mixArtistID = id; return nil },
			HistoryRecent: func(n int) (json.RawMessage, error) {
				historyN = n
				return json.RawMessage(`[{"id":"1","title":"Song"}]`), nil
			},
		},
		nil,
	)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	c := control.NewClient("http://"+srv.Addr(), "", "")

	if _, err := c.QueueAdd("3135556", true); err != nil || addID != "3135556" || !addNext {
		t.Errorf("QueueAdd: err=%v id=%q next=%v", err, addID, addNext)
	}
	if _, err := c.QueueJump(2); err != nil || jumped != 2 {
		t.Errorf("QueueJump: err=%v index=%d", err, jumped)
	}
	if _, err := c.QueueRemove(1); err != nil || removed != 1 {
		t.Errorf("QueueRemove: err=%v index=%d", err, removed)
	}
	if _, err := c.QueueMove(4, 0); err != nil || movedFrom != 4 || movedTo != 0 {
		t.Errorf("QueueMove: err=%v from=%d to=%d", err, movedFrom, movedTo)
	}
	if _, err := c.PlayAlbum("302127"); err != nil || albumID != "302127" {
		t.Errorf("PlayAlbum: err=%v id=%q", err, albumID)
	}
	if _, err := c.PlayMixTrack("3135556"); err != nil || mixTrackID != "3135556" {
		t.Errorf("PlayMixTrack: err=%v id=%q", err, mixTrackID)
	}
	if _, err := c.PlayMixArtist("27"); err != nil || mixArtistID != "27" {
		t.Errorf("PlayMixArtist: err=%v id=%q", err, mixArtistID)
	}
	raw, err := c.HistoryRecent(5)
	if err != nil || historyN != 5 || !strings.Contains(string(raw), `"title":"Song"`) {
		t.Errorf("HistoryRecent: err=%v n=%d raw=%s", err, historyN, raw)
	}
}

// TestClientEvents smoke-tests the SSE stream: the initial snapshot must
// arrive, and cancelling the context must close the channel.
func TestClientEvents(t *testing.T) {
	srv := control.NewServer(
		control.Config{Addr: "127.0.0.1:0"},
		func() control.State { return control.State{State: "playing"} },
		func() control.Account { return control.Account{} },
		control.Commands{},
		nil,
	)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	states, err := control.NewClient("http://"+srv.Addr(), "", "").Events(ctx)
	if err != nil {
		t.Fatal("Events:", err)
	}

	select {
	case st, ok := <-states:
		if !ok {
			t.Fatal("stream closed before the initial snapshot")
		}
		if st.State != "playing" {
			t.Fatalf("initial snapshot state = %q", st.State)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the initial snapshot")
	}

	cancel()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-states:
			if !ok {
				return // channel closed on cancel — done
			}
		case <-deadline:
			t.Fatal("channel not closed after context cancel")
		}
	}
}
