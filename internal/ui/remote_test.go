package ui

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cycl0o0/OpenDeezer/v3/internal/control"
	tea "github.com/charmbracelet/bubbletea"
)

func TestNormalizePeer(t *testing.T) {
	cases := []struct{ in, base, hostport string }{
		{"192.168.1.5", "http://192.168.1.5:7654", "192.168.1.5:7654"},
		{"192.168.1.5:9000", "http://192.168.1.5:9000", "192.168.1.5:9000"},
		{"http://host:7654/", "http://host:7654", "host:7654"},
		{"  host  ", "http://host:7654", "host:7654"},
		{"", "", ""},
	}
	for _, c := range cases {
		base, hp := normalizePeer(c.in)
		if base != c.base || hp != c.hostport {
			t.Errorf("normalizePeer(%q) = (%q,%q), want (%q,%q)", c.in, base, hp, c.base, c.hostport)
		}
	}
}

// stopCountingServer returns a control-API stub that counts POST /stop calls and
// signals `hit` on each, replying with an empty State to every request.
func stopCountingServer(t *testing.T, stops *int32, hit chan<- struct{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stop" {
			atomic.AddInt32(stops, 1)
			select {
			case hit <- struct{}{}:
			default:
			}
		}
		_, _ = w.Write([]byte("{}"))
	}))
}

// TestRemoteEscDetachesWithoutStoppingPeer pins B18: leaving the remote screen
// with esc must detach non-destructively — it must NOT send Stop to the peer
// (disconnect != stop its music) and must not make a blocking HTTP call on the
// update loop (esc returns nil, no cmd, and never hits the peer).
func TestRemoteEscDetachesWithoutStoppingPeer(t *testing.T) {
	var stops int32
	srv := stopCountingServer(t, &stops, nil)
	defer srv.Close()

	m := interactionModel(screenRemoteCtl)
	m.remote = control.NewClient(srv.URL, "", "acct")
	m.remoteState = control.State{Volume: 0.5}

	_, cmd := m.handleRemoteKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.remote != nil {
		t.Fatal("esc did not detach the remote client")
	}
	if m.screen != screenMenu {
		t.Fatalf("screen = %v, want screenMenu", m.screen)
	}
	if cmd != nil {
		t.Fatal("esc returned a cmd; detach must be non-blocking with no peer call")
	}
	// Give any (erroneous) stop a chance to land, then assert none was sent.
	time.Sleep(50 * time.Millisecond)
	if n := atomic.LoadInt32(&stops); n != 0 {
		t.Fatalf("esc sent %d /stop to the peer; disconnect must not stop it", n)
	}
}

// TestRemoteStopKeyStopsPeerAndDetaches pins the explicit stop-and-disconnect
// key ('S'): it detaches AND stops the peer, but does so via a tea.Cmd off the
// update loop (never a synchronous call that could freeze on an unreachable peer).
func TestRemoteStopKeyStopsPeerAndDetaches(t *testing.T) {
	var stops int32
	hit := make(chan struct{}, 1)
	srv := stopCountingServer(t, &stops, hit)
	defer srv.Close()

	m := interactionModel(screenRemoteCtl)
	m.remote = control.NewClient(srv.URL, "", "acct")

	_, cmd := m.handleRemoteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	if m.remote != nil {
		t.Fatal("S did not detach the remote client")
	}
	if m.screen != screenMenu {
		t.Fatalf("screen = %v, want screenMenu", m.screen)
	}
	if cmd == nil {
		t.Fatal("S must return an async stop-and-detach cmd")
	}
	// The cmd runs off the update loop and discards its result.
	if msg := cmd(); msg != nil {
		t.Fatalf("stop-detach cmd returned %v, want nil (result discarded)", msg)
	}
	select {
	case <-hit:
	case <-time.After(time.Second):
		t.Fatal("peer never received /stop from the S key cmd")
	}
	if n := atomic.LoadInt32(&stops); n != 1 {
		t.Fatalf("peer received %d /stop, want exactly 1", n)
	}
}

// TestRepeatShuffleKeysDoNotMutateLocalWhenRemote pins item 3: while driving a
// peer, the r/z keys must forward to the peer and leave the LOCAL queue's
// repeat/shuffle untouched (the peer's reported state drives the display).
func TestRepeatShuffleKeysDoNotMutateLocalWhenRemote(t *testing.T) {
	m := interactionModel(screenList) // not the remote screen: global keys own r/z
	m.q.Set(testTracks(3), 0)
	m.remote = control.NewClient("http://127.0.0.1:0", "", "acct") // cmd never executed

	repBefore := m.q.Repeat()
	shufBefore := m.q.Shuffle()

	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("r while driving a peer must forward a cmd to it")
	}
	if m.q.Repeat() != repBefore {
		t.Fatalf("r mutated local repeat (%v -> %v) while driving a peer", repBefore, m.q.Repeat())
	}

	_, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	if cmd == nil {
		t.Fatal("z while driving a peer must forward a cmd to it")
	}
	if m.q.Shuffle() != shufBefore {
		t.Fatalf("z mutated local shuffle (%v -> %v) while driving a peer", shufBefore, m.q.Shuffle())
	}
}

// TestPlaybackKeysRouteToPeerWhenRemote pins item 4: on the remote lyrics/
// now-playing screens the transport keys must target the peer, not the local
// player. The model has a nil player, so any LOCAL playback path would panic —
// returning a peer cmd instead proves the key routed to the peer.
func TestPlaybackKeysRouteToPeerWhenRemote(t *testing.T) {
	keys := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{' '}}, // play/pause
		{Type: tea.KeyRunes, Runes: []rune{'n'}}, // next
		{Type: tea.KeyRunes, Runes: []rune{'p'}}, // prev
		{Type: tea.KeyRunes, Runes: []rune{'s'}}, // stop
		{Type: tea.KeyLeft},                      // seek back
		{Type: tea.KeyRight},                     // seek forward
		{Type: tea.KeyRunes, Runes: []rune{'+'}}, // volume up
		{Type: tea.KeyRunes, Runes: []rune{'-'}}, // volume down
	}
	for _, k := range keys {
		// Fresh model per key: nil player means a local playback path panics.
		m := interactionModel(screenLyrics)
		m.remote = control.NewClient("http://127.0.0.1:0", "", "acct")
		m.remoteState = control.State{Volume: 0.5, PositionMS: 30000}
		_, cmd := m.handleKey(k)
		if cmd == nil {
			t.Fatalf("key %q while driving a peer returned no cmd (routed locally?)", k.String())
		}
	}
}
