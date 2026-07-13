package ui

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/Cycl0o0/OpenDeezer/v3/internal/control"
	"github.com/Cycl0o0/OpenDeezer/v3/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/v3/internal/history"
	tea "github.com/charmbracelet/bubbletea"
)

// controlQueueEditMsg is a queue mutation from the control API. Unlike the
// fire-and-forget controlCmdMsg commands, these must report success/failure
// back to the HTTP handler (the server maps an error to 409 Conflict), so the
// hook blocks on reply until the update loop has applied the edit. The queue is
// only ever touched on the update loop — same single-threading as the UI keys.
type controlQueueEditMsg struct {
	kind  string       // add | jump | remove | move
	track deezer.Track // for add (already fetched off-loop)
	next  bool         // for add: insert after current instead of appending
	i, j  int          // indices (move uses both)
	reply chan<- error // buffered(1); the update loop never blocks sending
}

// controlReplyTimeout bounds how long a control HTTP handler waits for the
// update loop to apply a queue edit (the loop normally answers within a tick).
const controlReplyTimeout = 5 * time.Second

// buildControlCommands assembles the control-API callback set shared by the
// main control server and the web-remote server. Thread-safety contract:
//
//   - simple playback commands marshal onto the Bubble Tea loop via send
//     (fire-and-forget, exactly like the media-key bridge);
//   - queue edits go through send too, but carry a reply channel so the HTTP
//     handler can surface a real error (see controlQueueEditMsg);
//   - network fetches (track/album/mix resolution) run on the HTTP goroutine —
//     the deezer.Client is safe for concurrent use (the server already calls
//     Search/Playlists from handlers) — and only the resulting track list is
//     handed to the update loop;
//   - HistoryRecent reads the history store directly: the store is internally
//     locked (one writer + concurrent readers by design).
func buildControlCommands(send func(tea.Msg), client *deezer.Client, hist *history.Store) control.Commands {
	queueEdit := func(msg controlQueueEditMsg, reply chan error) error {
		send(msg)
		select {
		case err := <-reply:
			return err
		case <-time.After(controlReplyTimeout):
			return errors.New("timed out waiting for the player")
		}
	}
	edit := func(kind string, i, j int) error {
		reply := make(chan error, 1)
		return queueEdit(controlQueueEditMsg{kind: kind, i: i, j: j, reply: reply}, reply)
	}
	playNow := func(fetch func() ([]deezer.Track, error)) error {
		if client == nil {
			return errors.New("not available")
		}
		ts, err := fetch()
		if err != nil {
			return err
		}
		if len(ts) == 0 {
			return errors.New("no tracks found")
		}
		send(playNowMsg{tracks: ts})
		return nil
	}
	return control.Commands{
		PlayPause:     func() { send(controlCmdMsg{kind: "playpause"}) },
		Next:          func() { send(controlCmdMsg{kind: "next"}) },
		Prev:          func() { send(controlCmdMsg{kind: "prev"}) },
		Stop:          func() { send(controlCmdMsg{kind: "stop"}) },
		Restart:       func() { send(controlCmdMsg{kind: "restart"}) },
		CycleRepeat:   func() { send(controlCmdMsg{kind: "repeat"}) },
		ToggleShuffle: func() { send(controlCmdMsg{kind: "shuffle"}) },
		// SET variants: a GUI/web/SDK controller can command an absolute repeat
		// mode / shuffle state (not just a cycle/toggle). Applied on the update
		// loop like the Cycle/Toggle kinds so the TUI honors BOTH command forms.
		SetRepeat:    func(mode string) { send(controlCmdMsg{kind: "repeat-set", mode: mode}) },
		SetShuffle:   func(on bool) { send(controlCmdMsg{kind: "shuffle-set", on: on}) },
		Seek:         func(ms int64) { send(controlCmdMsg{kind: "seek", ms: ms}) },
		SetVolume:    func(v float64) { send(controlCmdMsg{kind: "volume", vol: v}) },
		PlayTrack:    func(id string) { send(controlCmdMsg{kind: "playtrack", id: id}) },
		PlayPlaylist: func(id string) { send(controlCmdMsg{kind: "playplaylist", id: id}) },
		SetSleepTimer: func(minutes int, eot bool) {
			send(controlCmdMsg{kind: "sleep", ms: int64(minutes), eot: eot})
		},
		CancelSleepTimer: func() { send(controlCmdMsg{kind: "sleepcancel"}) },

		QueueAdd: func(id string, next bool) error {
			if client == nil {
				return errors.New("not available")
			}
			t, err := client.Track(id)
			if err != nil {
				return err
			}
			reply := make(chan error, 1)
			return queueEdit(controlQueueEditMsg{kind: "add", track: t, next: next, reply: reply}, reply)
		},
		QueueJump:   func(i int) error { return edit("jump", i, 0) },
		QueueRemove: func(i int) error { return edit("remove", i, 0) },
		QueueMove:   func(from, to int) error { return edit("move", from, to) },
		PlayAlbum: func(id string) error {
			return playNow(func() ([]deezer.Track, error) { return client.AlbumTracks(id) })
		},
		PlayMixTrack: func(id string) error {
			return playNow(func() ([]deezer.Track, error) { return client.TrackMix(id) })
		},
		PlayMixArtist: func(id string) error {
			return playNow(func() ([]deezer.Track, error) { return client.ArtistMix(id) })
		},
		HistoryRecent: func(n int) (json.RawMessage, error) {
			if hist == nil {
				return json.RawMessage(`[]`), nil
			}
			es, err := hist.Recent(n)
			if err != nil {
				return nil, err
			}
			if len(es) == 0 {
				return json.RawMessage(`[]`), nil
			}
			return json.Marshal(es)
		},
	}
}

// handleControlQueueEdit applies a control-API queue edit on the update loop
// and reports the result on the message's reply channel. Mirrors the queue-view
// keys: jump reuses queueJump, remove refuses the playing row like the UI does,
// move follows the cursor semantics of queueMove.
func (m *Model) handleControlQueueEdit(msg controlQueueEditMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var err error
	switch msg.kind {
	case "add":
		switch {
		case msg.track.ID == "":
			err = errors.New("unknown track")
		case m.episodeMode && m.q.Len() > 0:
			err = errors.New("queue is playing podcast episodes")
		default:
			if m.q.Len() == 0 {
				m.episodeMode = false
			}
			if msg.next {
				m.q.InsertAfterCurrent(msg.track)
			} else {
				m.q.Append(msg.track)
			}
			cmd = m.invalidatePreload()
		}
	case "jump":
		if msg.i < 0 || msg.i >= m.q.Len() {
			err = errors.New("queue index out of range")
		} else {
			cmd = m.queueJump(msg.i)
		}
	case "remove":
		switch {
		case msg.i < 0 || msg.i >= m.q.Len():
			err = errors.New("queue index out of range")
		case msg.i == m.q.Index():
			err = errors.New("can't remove the playing track")
		default:
			cmd = m.queueRemove(msg.i)
		}
	case "move":
		n := m.q.Len()
		if msg.i < 0 || msg.i >= n || msg.j < 0 || msg.j >= n {
			err = errors.New("queue index out of range")
		} else if msg.i != msg.j {
			cmd = m.queueMove(msg.i, msg.j)
		}
	default:
		err = errors.New("unknown queue edit")
	}
	// Ordering invariant: publish the refreshed control snapshot BEFORE the
	// reply is sent, so the snapshot store happens-before the HTTP handler's
	// receive — a handler that serializes state right after the reply must see
	// the post-edit queue, never the stale one. publishControl is nil-guarded
	// (no-op without a control server) and the reply channel is buffered, so
	// neither line can block the update loop.
	m.publishControl()
	if msg.reply != nil {
		msg.reply <- err // buffered(1) by the sender; never blocks the loop
	}
	return m, cmd
}
