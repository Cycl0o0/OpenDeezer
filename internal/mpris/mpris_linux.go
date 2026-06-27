//go:build linux

package mpris

import (
	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

const (
	busName  = "org.mpris.MediaPlayer2.opendeezer"
	objPath  = "/org/mpris/MediaPlayer2"
	rootIff  = "org.mpris.MediaPlayer2"
	playIff  = "org.mpris.MediaPlayer2.Player"
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
	c := &linuxController{conn: conn, cmds: cmds}

	conn.Export(rootObj{cmds}, objPath, rootIff)
	conn.Export(playerObj{cmds}, objPath, playIff)

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
}

func (c *linuxController) spec() map[string]map[string]*prop.Prop {
	ro := func(v interface{}) *prop.Prop { return &prop.Prop{Value: v, Writable: false, Emit: prop.EmitFalse} }
	em := func(v interface{}) *prop.Prop { return &prop.Prop{Value: v, Writable: false, Emit: prop.EmitTrue} }
	return map[string]map[string]*prop.Prop{
		rootIff: {
			"CanQuit":             ro(true),
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
	if c.props == nil {
		return
	}
	meta := map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath(objPath + "/track/cur")),
		"mpris:length":  dbus.MakeVariant(s.LengthUS),
		"xesam:title":   dbus.MakeVariant(s.Title),
		"xesam:artist":  dbus.MakeVariant([]string{s.Artist}),
		"xesam:album":   dbus.MakeVariant(s.Album),
	}
	if s.ArtURL != "" {
		meta["mpris:artUrl"] = dbus.MakeVariant(s.ArtURL)
	}
	status := s.Status
	if status == "" {
		status = "Stopped"
	}
	c.props.SetMust(playIff, "Metadata", meta)
	c.props.SetMust(playIff, "PlaybackStatus", status)
	c.props.SetMust(playIff, "Position", s.PositionUS)
}

func (c *linuxController) Close() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// ---- exported method objects ----

type rootObj struct{ cmds Commands }

func (rootObj) Raise() *dbus.Error { return nil }
func (o rootObj) Quit() *dbus.Error {
	if o.cmds.Stop != nil {
		o.cmds.Stop()
	}
	return nil
}

type playerObj struct{ cmds Commands }

func call(fn func()) *dbus.Error {
	if fn != nil {
		fn()
	}
	return nil
}

func (o playerObj) PlayPause() *dbus.Error { return call(o.cmds.PlayPause) }
func (o playerObj) Play() *dbus.Error      { return call(o.cmds.PlayPause) }
func (o playerObj) Pause() *dbus.Error     { return call(o.cmds.PlayPause) }
func (o playerObj) Next() *dbus.Error      { return call(o.cmds.Next) }
func (o playerObj) Previous() *dbus.Error  { return call(o.cmds.Prev) }
func (o playerObj) Stop() *dbus.Error      { return call(o.cmds.Stop) }
func (o playerObj) Seek(offsetUS int64) *dbus.Error {
	if o.cmds.Seek != nil {
		o.cmds.Seek(offsetUS)
	}
	return nil
}
func (o playerObj) SetPosition(_ dbus.ObjectPath, posUS int64) *dbus.Error {
	if o.cmds.SetPosition != nil {
		o.cmds.SetPosition("", posUS)
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
				Properties: c.props.Introspection(playIff),
			},
		},
	}
}
