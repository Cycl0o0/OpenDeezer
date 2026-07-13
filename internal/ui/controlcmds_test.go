package ui

import (
	"testing"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/control"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/queue"
	tea "github.com/charmbracelet/bubbletea"
)

// ctrlPublishModel builds a UI-only model with a live control server + a
// zero-value player so publishControl can build a snapshot (it only reads the
// player's atomic fields, opening no audio device).
func ctrlPublishModel() *Model {
	m := interactionModel(screenList)
	m.player = &audio.Player{}
	m.ctrl = control.New(control.Config{},
		func() control.State { return control.State{} },
		func() control.Account { return control.Account{} },
		control.Commands{}, nil)
	return m
}

// TestBuildControlCommandsWiresSetVariants pins the host SET half of the control
// contract: the TUI must serve SetRepeat/SetShuffle (absolute) commands, not just
// CycleRepeat/ToggleShuffle, so a GUI/web/SDK controller can drive it either way.
func TestBuildControlCommandsWiresSetVariants(t *testing.T) {
	var got []controlCmdMsg
	send := func(msg tea.Msg) {
		if c, ok := msg.(controlCmdMsg); ok {
			got = append(got, c)
		}
	}
	cmds := buildControlCommands(send, nil, nil)
	if cmds.SetRepeat == nil || cmds.SetShuffle == nil {
		t.Fatal("SetRepeat/SetShuffle not wired into control.Commands")
	}
	// Both command forms must remain available (the SET half is additive).
	if cmds.CycleRepeat == nil || cmds.ToggleShuffle == nil {
		t.Fatal("CycleRepeat/ToggleShuffle must still be wired")
	}
	cmds.SetRepeat("all")
	cmds.SetShuffle(true)
	if len(got) != 2 {
		t.Fatalf("got %d controlCmdMsg, want 2", len(got))
	}
	if got[0].kind != "repeat-set" || got[0].mode != "all" {
		t.Fatalf("SetRepeat emitted %+v, want {repeat-set all}", got[0])
	}
	if got[1].kind != "shuffle-set" || !got[1].on {
		t.Fatalf("SetShuffle emitted %+v, want {shuffle-set on=true}", got[1])
	}
}

// TestControlSetRepeatMutatesQueueAndPublishes drives the repeat-set kind through
// the update loop and checks it mutates the queue to the absolute mode and
// republishes the control snapshot.
func TestControlSetRepeatMutatesQueueAndPublishes(t *testing.T) {
	m := ctrlPublishModel()
	m.q.Set(testTracks(3), 0)

	for _, tc := range []struct {
		mode string
		want queue.Repeat
		str  string
	}{
		{"one", queue.RepeatOne, "one"},
		{"all", queue.RepeatAll, "all"},
		{"off", queue.RepeatOff, "off"},
	} {
		_, _ = m.Update(controlCmdMsg{kind: "repeat-set", mode: tc.mode})
		if m.q.Repeat() != tc.want {
			t.Fatalf("repeat-set %q: queue repeat = %v, want %v", tc.mode, m.q.Repeat(), tc.want)
		}
		if st := m.ctrlState.Load(); st == nil || st.Repeat != tc.str {
			t.Fatalf("repeat-set %q: published snapshot = %+v, want Repeat=%q", tc.mode, st, tc.str)
		}
	}
}

// TestControlSetShuffleMutatesQueueAndPublishes drives the shuffle-set kind and
// checks it sets the absolute shuffle state and republishes.
func TestControlSetShuffleMutatesQueueAndPublishes(t *testing.T) {
	m := ctrlPublishModel()
	m.q.Set(testTracks(3), 0)

	_, _ = m.Update(controlCmdMsg{kind: "shuffle-set", on: true})
	if !m.q.Shuffle() {
		t.Fatal("shuffle-set on did not enable shuffle")
	}
	if st := m.ctrlState.Load(); st == nil || !st.Shuffle {
		t.Fatalf("published snapshot = %+v, want Shuffle=true", st)
	}

	_, _ = m.Update(controlCmdMsg{kind: "shuffle-set", on: false})
	if m.q.Shuffle() {
		t.Fatal("shuffle-set off did not disable shuffle")
	}
	if st := m.ctrlState.Load(); st == nil || st.Shuffle {
		t.Fatalf("published snapshot = %+v, want Shuffle=false", st)
	}
}

// TestControlQueueEditPublishesBeforeReply pins the ordering invariant of
// handleControlQueueEdit: the refreshed control snapshot must be stored BEFORE
// the reply is sent. The control HTTP handler serializes state as soon as its
// queue-edit call returns, so if the reply were sent first it could observe
// the pre-edit queue. The reply channel here is deliberately unbuffered so the
// handler's send synchronizes with our receive: with the correct ordering the
// snapshot check below is deterministic; with the reversed ordering it races
// against a publish that has not happened yet. The "add" kind is used because
// it is the one edit whose branch does not publish on its own (jump/remove/
// move already publish inside their queue helpers).
func TestControlQueueEditPublishesBeforeReply(t *testing.T) {
	m := interactionModel(screenList)
	m.q.Set(testTracks(3), 0)
	// A zero-value player suffices: publishControl and the add path only read
	// its atomic fields (no audio device is opened, no preload is scheduled).
	m.player = &audio.Player{}
	m.ctrl = control.New(control.Config{},
		func() control.State { return control.State{} },
		func() control.Account { return control.Account{} },
		control.Commands{}, nil)

	m.publishControl()
	if st := m.ctrlState.Load(); st == nil || len(st.Queue) != 3 {
		t.Fatalf("seed snapshot = %+v, want 3 queued tracks", st)
	}

	reply := make(chan error) // unbuffered: the send happens-before our receive returns
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = m.Update(controlQueueEditMsg{
			kind: "add", track: deezer.Track{ID: "x", Name: "X"}, reply: reply,
		})
	}()
	if err := <-reply; err != nil {
		t.Fatalf("add: %v", err)
	}
	// The edit must already be published when the reply arrives.
	st := m.ctrlState.Load()
	if st == nil || len(st.Queue) != 4 {
		n := -1
		if st != nil {
			n = len(st.Queue)
		}
		t.Fatalf("snapshot at reply time has %d tracks, want 4 (publish must precede the reply)", n)
	}
	if st.Queue[3].ID != "x" {
		t.Fatalf("snapshot tail = %+v, want the added track", st.Queue[3])
	}
	<-done
}
