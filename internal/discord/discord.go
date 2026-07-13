// Package discord publishes the now-playing track to Discord as Rich Presence
// ("Listening to …") over Discord's local IPC socket. It needs a Discord
// application client id (configure via $OPENDEEZER_DISCORD_APP_ID or
// ~/.config/opendeezer/discord-app-id.txt); with none set it is a no-op.
//
// Connection is best-effort and lazy: all IPC work runs on a dedicated worker
// goroutine fed by Update, so the caller (the Bubble Tea update loop) never
// blocks on a dial or a wedged Discord. Works on macOS/Linux (unix socket);
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

	odlog "github.com/Cycl0o0/OpenDeezer/v2/internal/log"
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
	opPing      int32 = 3
	opPong      int32 = 4
)

const (
	ipcTimeout       = 2 * time.Second  // dial / read READY / write frame
	reconnectBackoff = 15 * time.Second // gap between failed connect attempts
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
	r := &richPresence{
		appID: appID,
		pid:   os.Getpid(),
		wake:  make(chan struct{}, 1),
		done:  make(chan struct{}),
	}
	go r.run()
	return r
}

type richPresence struct {
	appID string
	pid   int

	mu     sync.Mutex
	latest State
	hasNew bool
	closed bool
	wake   chan struct{} // buffered(1): a new state or close is pending
	done   chan struct{} // closed when the worker goroutine exits

	// The following are owned by the worker goroutine (run/push/connect/shutdown)
	// or, for conn writes, additionally guarded by wmu.
	wmu         sync.Mutex // serialises writes to conn (worker + PONG from drain)
	conn        net.Conn
	nonce       int
	lastKey     string
	lastStart   int64     // unix secs of the last sent "start" timestamp (0 when not playing)
	nextConnect time.Time // don't dial again before this (reconnect backoff)
	warnedNoIPC bool
}

// Update stores the latest state and wakes the worker. It never blocks on IPC.
func (r *richPresence) Update(s State) {
	r.mu.Lock()
	if !r.closed {
		r.latest = s
		r.hasNew = true
	}
	r.mu.Unlock()
	r.signal()
}

func (r *richPresence) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// run is the worker loop: it applies the latest state to Discord and handles
// shutdown. All blocking IPC (dial, handshake, writes) happens here, off the
// caller's goroutine.
func (r *richPresence) run() {
	defer close(r.done)
	for range r.wake {
		r.mu.Lock()
		closed := r.closed
		s := r.latest
		hasNew := r.hasNew
		r.hasNew = false
		r.mu.Unlock()

		if closed {
			r.shutdown()
			return
		}
		if hasNew {
			r.push(s)
		}
	}
}

// push (re)connects if needed and sends the activity, deduping unchanged states.
func (r *richPresence) push(s State) {
	// Throttle: Discord rate-limits activity updates. The key covers everything
	// that affects the rendered presence except the timestamps; a bare position
	// change (seek, repeat-one restart) is caught separately by positionJumped.
	key := throttleKey(s)
	if key == r.lastKey && !r.positionJumped(s) {
		return
	}

	if r.conn == nil {
		if time.Now().Before(r.nextConnect) {
			return // backing off after a recent failure
		}
		if err := r.connect(); err != nil {
			r.nextConnect = time.Now().Add(reconnectBackoff)
			return
		}
	}

	var payload []byte
	if s.Status == "stopped" || s.Title == "" {
		payload = r.clearFrame()
	} else {
		payload = r.activityFrame(s)
	}
	if err := r.writeConn(opFrame, payload); err != nil {
		r.drop()
		return
	}
	r.lastKey = key
}

// throttleKey identifies a presence-visible state; timestamps are excluded.
func throttleKey(s State) string {
	return s.Status + "|" + s.Title + "|" + s.Artist + "|" + s.Album + "|" +
		strconv.FormatInt(s.DurationMS, 10)
}

// positionJumped reports whether the reported position has drifted from where
// Discord's own extrapolation (from the last sent start) would put it, i.e. a
// seek or a repeat-one restart that the throttle key alone would miss.
func (r *richPresence) positionJumped(s State) bool {
	if s.Status != "playing" || r.lastStart == 0 {
		return false
	}
	expectedMS := (time.Now().Unix() - r.lastStart) * 1000
	diff := s.PositionMS - expectedMS
	if diff < 0 {
		diff = -diff
	}
	return diff > 2000
}

// connect dials the IPC socket, handshakes, and starts the drain goroutine.
// Runs on the worker goroutine (never on the caller's).
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
	_ = conn.SetWriteDeadline(time.Now().Add(ipcTimeout))
	if err := writeFrame(conn, opHandshake, hs); err != nil {
		_ = conn.Close()
		return err
	}
	// Expect a READY dispatch; ignore the contents. Bound the read so a socket
	// that accepts but never sends READY (a stale/foreign discord-ipc-N socket)
	// can't wedge the worker.
	_ = conn.SetReadDeadline(time.Now().Add(ipcTimeout))
	if _, _, err := readFrame(conn); err != nil {
		_ = conn.Close()
		return err
	}
	_ = conn.SetReadDeadline(time.Time{}) // clear for the drain loop
	r.conn = conn
	r.lastKey = ""
	r.lastStart = 0
	r.warnedNoIPC = false
	go r.drain(conn) // read+discard responses (and PING->PONG) so the RX buffer never fills
	odlog.Info("discord: rich presence connected (app %s)", r.appID)
	return nil
}

// drain reads and discards every frame Discord sends (SET_ACTIVITY replies,
// PINGs) so the socket receive buffer never fills; it answers PING with PONG and
// exits when conn errors (dropped/closed).
func (r *richPresence) drain(conn net.Conn) {
	for {
		op, buf, err := readFrame(conn)
		if err != nil {
			return
		}
		if op == opPing {
			_ = r.writeConnTo(conn, opPong, buf)
		}
	}
}

// writeConn writes a frame to the current conn with a deadline.
func (r *richPresence) writeConn(op int32, payload []byte) error {
	return r.writeConnTo(r.conn, op, payload)
}

// writeConnTo writes to a specific conn under wmu (worker writes and the drain
// goroutine's PONGs must not interleave on the same socket).
func (r *richPresence) writeConnTo(conn net.Conn, op int32, payload []byte) error {
	if conn == nil {
		return errNoIPC
	}
	r.wmu.Lock()
	defer r.wmu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(ipcTimeout))
	return writeFrame(conn, op, payload)
}

func (r *richPresence) drop() {
	if r.conn != nil {
		_ = r.conn.Close() // also unblocks the drain goroutine
		r.conn = nil
	}
	r.lastKey = ""
	r.lastStart = 0
}

var closeTimeout = 6 * time.Second

// Close clears the presence and shuts down the worker.
func (r *richPresence) Close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	r.mu.Unlock()
	r.signal()
	select {
	case <-r.done:
	case <-time.After(closeTimeout):
	}
}

// shutdown clears the presence and closes the socket; runs on the worker.
func (r *richPresence) shutdown() {
	if r.conn != nil {
		_ = r.writeConn(opFrame, r.clearFrame())
		_ = r.writeConn(opClose, []byte("{}"))
		_ = r.conn.Close()
		r.conn = nil
	}
}

// activityFrame builds a SET_ACTIVITY payload for a now-playing track.
func (r *richPresence) activityFrame(s State) []byte {
	r.nonce++
	r.lastStart = 0
	act := map[string]any{
		"type":    2, // Listening
		"details": pad2(trim(s.Title, 128)),
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
		r.lastStart = start
		act["timestamps"] = map[string]any{"start": start, "end": start + s.DurationMS/1000}
	case s.Status == "paused":
		// Discord rejects a 1-char state; avoid the doubled "Paused · paused".
		if v := asStr(act["state"]); v != "" {
			act["state"] = v + " · paused"
		} else {
			act["state"] = "Paused"
		}
	}
	return r.setActivity(act)
}

// clearFrame builds a SET_ACTIVITY payload that removes the presence.
func (r *richPresence) clearFrame() []byte {
	r.nonce++
	r.lastStart = 0
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

// pad2 ensures a Discord string field is at least 2 chars (Discord rejects a
// 1-char details/state, which would silently drop the whole SET_ACTIVITY).
func pad2(s string) string {
	if len(s) == 1 {
		return s + " "
	}
	return s
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
