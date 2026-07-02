//go:build linux

package mpris

import (
	"strconv"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

const (
	busName = "org.mpris.MediaPlayer2.opendeezer"
	objPath = "/org/mpris/MediaPlayer2"
	rootIff = "org.mpris.MediaPlayer2"
	playIff = "org.mpris.MediaPlayer2.Player"

	// seekTolUS is how far the reported position may drift from free-running
	// playback before we treat it as a seek and emit the Seeked signal.
	seekTolUS = 2 * 1_000_000
)

// New connects to the session bus and exports the MPRIS interfaces. On any
// failure (no session bus, name taken) it returns a no-op controller.
func New(cmds Commands) Controller {
	conn, err := dbus.SessionBus()
	if err != nil {
		return noop{}
	}
	reply, err := conn.RequestName(busName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		return noop{}
	}
	c := &linuxController{conn: conn, cmds: cmds, lastStatus: "Stopped"}

	conn.Export(rootObj{c}, objPath, rootIff)
	conn.Export(playerObj{c}, objPath, playIff)

	props, err := prop.Export(conn, objPath, c.spec())
	if err != nil {
		_ = conn.Close()
		return noop{}
	}
	c.props = props

	conn.Export(introspect.NewIntrospectable(c.node()), objPath,
		"org.freedesktop.DBus.Introspectable")
	return c
}

type linuxController struct {
	conn  *dbus.Conn
	cmds  Commands
	props *prop.Properties

	mu          sync.Mutex
	dead        bool            // set once the bus connection is gone; Update becomes a no-op
	lastMetaKey string          // change-detection for Metadata
	lastStatus  string          // last published PlaybackStatus; also drives Play/Pause semantics
	trackID     dbus.ObjectPath // current mpris:trackid; SetPosition ignores stale ids
	lastPosUS   int64           // last reported position, for Seeked discontinuity detection
	lastPosTime time.Time       // wall clock at lastPosUS
}

func (c *linuxController) spec() map[string]map[string]*prop.Prop {
	ro := func(v interface{}) *prop.Prop { return &prop.Prop{Value: v, Writable: false, Emit: prop.EmitFalse} }
	em := func(v interface{}) *prop.Prop { return &prop.Prop{Value: v, Writable: false, Emit: prop.EmitTrue} }
	return map[string]map[string]*prop.Prop{
		rootIff: {
			// CanQuit only if the app wired a real quit command; otherwise Quit
			// cannot honour its contract (stop the process) so we must not claim it.
			"CanQuit":             ro(c.cmds.Quit != nil),
			"CanRaise":            ro(false),
			"HasTrackList":        ro(false),
			"Identity":            ro("OpenDeezer"),
			"DesktopEntry":        ro("opendeezer"),
			"SupportedUriSchemes": ro([]string{}),
			"SupportedMimeTypes":  ro([]string{}),
		},
		playIff: {
			"PlaybackStatus": em("Stopped"),
			"Metadata":       em(map[string]dbus.Variant{}),
			"Position":       ro(int64(0)),
			"Rate":           ro(1.0),
			"MinimumRate":    ro(1.0),
			"MaximumRate":    ro(1.0),
			"Volume":         ro(1.0),
			"CanGoNext":      ro(true),
			"CanGoPrevious":  ro(true),
			"CanPlay":        ro(true),
			"CanPause":       ro(true),
			"CanSeek":        ro(true),
			"CanControl":     ro(true),
		},
	}
}

// Update publishes the current playback state to the desktop.
func (c *linuxController) Update(s State) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.props == nil || c.dead || !c.conn.Connected() {
		return
	}

	status := s.Status
	if status == "" {
		status = "Stopped"
	}

	trackID := dbus.ObjectPath(objPath + "/track/cur")
	if id := sanitizeID(s.TrackID); id != "" {
		trackID = dbus.ObjectPath(objPath + "/track/" + id)
	}
	trackChanged := trackID != c.trackID

	meta := map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(trackID),
		"mpris:length":  dbus.MakeVariant(s.LengthUS),
		"xesam:title":   dbus.MakeVariant(s.Title),
		"xesam:artist":  dbus.MakeVariant([]string{s.Artist}),
		"xesam:album":   dbus.MakeVariant(s.Album),
	}
	if s.ArtURL != "" {
		meta["mpris:artUrl"] = dbus.MakeVariant(s.ArtURL)
	}

	// Emit Metadata/PlaybackStatus only when they actually change: both are
	// EmitTrue and godbus emits PropertiesChanged on every SetMust regardless of
	// whether the value differs, which would spam clients once per tick.
	metaKey := string(trackID) + "\x00" + s.Title + "\x00" + s.Artist + "\x00" +
		s.Album + "\x00" + s.ArtURL + "\x00" + strconv.FormatInt(s.LengthUS, 10)
	if metaKey != c.lastMetaKey {
		c.setProp(playIff, "Metadata", meta)
		c.lastMetaKey = metaKey
	}
	if status != c.lastStatus {
		c.setProp(playIff, "PlaybackStatus", status)
	}
	prevStatus := c.lastStatus
	c.lastStatus = status
	c.trackID = trackID

	// Position is EmitFalse (stored, never signalled). The spec requires a Seeked
	// signal on discontinuities, so emit one when the position jumps away from
	// where free-running playback would have reached, or on a track change.
	now := time.Now()
	expected := c.lastPosUS
	if prevStatus == "Playing" {
		expected += int64(now.Sub(c.lastPosTime) / time.Microsecond)
	}
	c.setProp(playIff, "Position", s.PositionUS)
	if !c.dead {
		diff := s.PositionUS - expected
		if diff < 0 {
			diff = -diff
		}
		if trackChanged || diff > seekTolUS {
			_ = c.conn.Emit(objPath, playIff+".Seeked", s.PositionUS)
		}
	}
	c.lastPosUS = s.PositionUS
	c.lastPosTime = now
}

// setProp publishes a property, recovering from godbus's SetMust panic (which
// fires when the session bus is gone, e.g. the desktop logged out). The first
// failure marks the controller dead so later Updates become no-ops.
func (c *linuxController) setProp(iface, property string, v interface{}) {
	defer func() {
		if recover() != nil {
			c.dead = true
		}
	}()
	c.props.SetMust(iface, property, v)
}

// status returns the last published PlaybackStatus (for Play/Pause semantics).
func (c *linuxController) status() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastStatus
}

// matchesTrack reports whether id refers to the currently playing track.
func (c *linuxController) matchesTrack(id dbus.ObjectPath) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return id == c.trackID
}

func (c *linuxController) Close() {
	c.mu.Lock()
	c.dead = true // stop any queued Update from touching the closing connection
	c.mu.Unlock()
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// sanitizeID keeps only characters valid in a D-Bus object-path element so a
// track id can be embedded in mpris:trackid.
func sanitizeID(id string) string {
	b := make([]byte, 0, len(id))
	for i := 0; i < len(id); i++ {
		ch := id[i]
		if ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch == '_' {
			b = append(b, ch)
		}
	}
	return string(b)
}

// ---- exported method objects ----

type rootObj struct{ c *linuxController }

func (rootObj) Raise() *dbus.Error { return nil }
func (o rootObj) Quit() *dbus.Error {
	if o.c.cmds.Quit != nil {
		o.c.cmds.Quit()
	}
	return nil
}

type playerObj struct{ c *linuxController }

func call(fn func()) *dbus.Error {
	if fn != nil {
		fn()
	}
	return nil
}

func (o playerObj) PlayPause() *dbus.Error { return call(o.c.cmds.PlayPause) }

// Play/Pause must be idempotent per the MPRIS spec (Pause does nothing when
// already paused, Play nothing when already playing). The app only exposes a
// toggle, so consult the last known status and toggle only when it would move
// in the requested direction.
func (o playerObj) Play() *dbus.Error {
	if o.c.status() != "Playing" {
		return call(o.c.cmds.PlayPause)
	}
	return nil
}
func (o playerObj) Pause() *dbus.Error {
	if o.c.status() == "Playing" {
		return call(o.c.cmds.PlayPause)
	}
	return nil
}
func (o playerObj) Next() *dbus.Error     { return call(o.c.cmds.Next) }
func (o playerObj) Previous() *dbus.Error { return call(o.c.cmds.Prev) }
func (o playerObj) Stop() *dbus.Error     { return call(o.c.cmds.Stop) }
func (o playerObj) Seek(offsetUS int64) *dbus.Error {
	if o.c.cmds.Seek != nil {
		o.c.cmds.Seek(offsetUS)
	}
	return nil
}
func (o playerObj) SetPosition(trackID dbus.ObjectPath, posUS int64) *dbus.Error {
	// Ignore stale seeks aimed at a track that already advanced.
	if !o.c.matchesTrack(trackID) {
		return nil
	}
	if o.c.cmds.SetPosition != nil {
		o.c.cmds.SetPosition("", posUS)
	}
	return nil
}

// OpenUri must keep this exact name: it maps to the MPRIS D-Bus method
// org.mpris.MediaPlayer2.Player.OpenUri via godbus reflection.
func (playerObj) OpenUri(string) *dbus.Error { return nil } //nolint:staticcheck // ST1003: D-Bus method name fixed by MPRIS spec

// node is the introspection data so desktops can discover the interfaces.
func (c *linuxController) node() *introspect.Node {
	return &introspect.Node{
		Name: objPath,
		Interfaces: []introspect.Interface{
			introspect.IntrospectData,
			prop.IntrospectData,
			{
				Name: rootIff,
				Methods: []introspect.Method{
					{Name: "Raise"}, {Name: "Quit"},
				},
				Properties: c.props.Introspection(rootIff),
			},
			{
				Name: playIff,
				Methods: []introspect.Method{
					{Name: "Next"}, {Name: "Previous"}, {Name: "Pause"},
					{Name: "PlayPause"}, {Name: "Stop"}, {Name: "Play"},
					{Name: "Seek", Args: []introspect.Arg{{Name: "Offset", Type: "x", Direction: "in"}}},
					{Name: "SetPosition", Args: []introspect.Arg{
						{Name: "TrackId", Type: "o", Direction: "in"},
						{Name: "Position", Type: "x", Direction: "in"},
					}},
					{Name: "OpenUri", Args: []introspect.Arg{{Name: "Uri", Type: "s", Direction: "in"}}},
				},
				Signals: []introspect.Signal{
					{Name: "Seeked", Args: []introspect.Arg{{Name: "Position", Type: "x", Direction: "out"}}},
				},
				Properties: c.props.Introspection(playIff),
			},
		},
	}
}
