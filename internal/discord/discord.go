// Package discord publishes the now-playing track to Discord as Rich Presence
// ("Listening to …") over Discord's local IPC socket. It needs a Discord
// application client id (configure via $OPENDEEZER_DISCORD_APP_ID or
// ~/.config/opendeezer/discord-app-id.txt); with none set it is a no-op.
//
// Connection is best-effort and lazy: if Discord isn't running it silently does
// nothing and retries on the next update. Works on macOS/Linux (unix socket);
// Windows (named pipe) is currently a no-op.
package discord

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	odlog "github.com/Cycl0o0/OpenDeezer/internal/log"
)

// State is a now-playing snapshot pushed by the UI.
type State struct {
	Status     string // "playing" | "paused" | "stopped"
	Title      string
	Artist     string
	Album      string
	PositionMS int64
	DurationMS int64
}

// Presence publishes State to Discord and is closed on shutdown.
type Presence interface {
	Update(State)
	Close()
}

// IPC opcodes.
const (
	opHandshake int32 = 0
	opFrame     int32 = 1
	opClose     int32 = 2
)

var errNoIPC = errors.New("discord: no IPC socket")

// noop is used when no app id is configured or on unsupported platforms.
type noop struct{}

func (noop) Update(State) {}
func (noop) Close()       {}

// New returns a Presence for the given Discord application id. An empty id yields
// a no-op (feature disabled).
func New(appID string) Presence {
	if appID == "" {
		return noop{}
	}
	return &richPresence{appID: appID, pid: os.Getpid()}
}

type richPresence struct {
	appID string
	pid   int

	mu          sync.Mutex
	conn        net.Conn
	nonce       int
	lastKey     string
	closed      bool
	warnedNoIPC bool
}

// Update pushes the state to Discord, (re)connecting if needed. Errors are
// swallowed — the connection is dropped and retried on the next call.
func (r *richPresence) Update(s State) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}

	// Throttle: Discord rate-limits activity updates. "Listening" timestamps let
	// Discord animate progress on its own, so we only resend when the track or
	// play/pause state changes.
	key := s.Status + "|" + s.Title + "|" + s.Artist
	if key == r.lastKey {
		return
	}

	if r.conn == nil {
		if err := r.connect(); err != nil {
			return // Discord not available; try again next update
		}
	}

	var payload []byte
	if s.Status == "stopped" || s.Title == "" {
		payload = r.clearFrame()
	} else {
		payload = r.activityFrame(s)
	}
	if err := writeFrame(r.conn, opFrame, payload); err != nil {
		r.drop()
		return
	}
	r.lastKey = key
}

// connect dials the IPC socket and performs the handshake. Caller holds r.mu.
func (r *richPresence) connect() error {
	conn, err := dialIPC()
	if err != nil {
		// Common case: Discord isn't running. Log once at debug to avoid spam.
		if !r.warnedNoIPC {
			r.warnedNoIPC = true
			odlog.Debug("discord: no IPC socket (is Discord running?): %v", err)
		}
		return err
	}
	hs, _ := json.Marshal(map[string]any{"v": 1, "client_id": r.appID})
	if err := writeFrame(conn, opHandshake, hs); err != nil {
		_ = conn.Close()
		return err
	}
	// Expect a READY dispatch; ignore the contents.
	if _, _, err := readFrame(conn); err != nil {
		_ = conn.Close()
		return err
	}
	r.conn = conn
	r.lastKey = ""
	r.warnedNoIPC = false
	odlog.Info("discord: rich presence connected (app %s)", r.appID)
	return nil
}

func (r *richPresence) drop() {
	if r.conn != nil {
		_ = r.conn.Close()
		r.conn = nil
	}
	r.lastKey = ""
}

// Close clears the presence and closes the socket.
func (r *richPresence) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn != nil {
		_ = writeFrame(r.conn, opFrame, r.clearFrame())
		_ = writeFrame(r.conn, opClose, []byte("{}"))
		_ = r.conn.Close()
		r.conn = nil
	}
	r.closed = true
}

// activityFrame builds a SET_ACTIVITY payload for a now-playing track.
func (r *richPresence) activityFrame(s State) []byte {
	r.nonce++
	act := map[string]any{
		"type":    2, // Listening
		"details": trim(s.Title, 128),
		"assets": map[string]any{
			"large_image": "opendeezer",
			"large_text":  firstNonEmpty(s.Album, "OpenDeezer"),
		},
	}
	if s.Artist != "" {
		act["state"] = trim("by "+s.Artist, 128)
	}
	switch {
	case s.Status == "playing" && s.DurationMS > 0:
		start := time.Now().Unix() - s.PositionMS/1000
		act["timestamps"] = map[string]any{"start": start, "end": start + s.DurationMS/1000}
	case s.Status == "paused":
		act["state"] = firstNonEmpty(asStr(act["state"]), "Paused") + " · paused"
	}
	return r.setActivity(act)
}

// clearFrame builds a SET_ACTIVITY payload that removes the presence.
func (r *richPresence) clearFrame() []byte {
	r.nonce++
	return r.setActivity(nil)
}

func (r *richPresence) setActivity(activity any) []byte {
	b, _ := json.Marshal(map[string]any{
		"cmd":   "SET_ACTIVITY",
		"args":  map[string]any{"pid": r.pid, "activity": activity},
		"nonce": strconv.Itoa(r.nonce),
	})
	return b
}

// ---- framing ----

func writeFrame(w io.Writer, op int32, payload []byte) error {
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:4], uint32(op))
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readFrame(r io.Reader) (int32, []byte, error) {
	var hdr [8]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	op := int32(binary.LittleEndian.Uint32(hdr[0:4]))
	n := binary.LittleEndian.Uint32(hdr[4:8])
	if n > 1<<20 {
		return 0, nil, errors.New("discord: frame too large")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	return op, buf, nil
}

// ---- helpers ----

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func asStr(v any) string {
	s, _ := v.(string)
	return s
}
