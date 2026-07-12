package ui

import (
	"testing"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/control"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/deezer"
)

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
