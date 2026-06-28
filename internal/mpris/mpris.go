// Package mpris exposes the player over the MPRIS D-Bus interface so Linux
// desktops (GNOME/KDE media overlays, media keys) show and control playback.
// On non-Linux it degrades to a no-op (see mpris_other.go).
package mpris

// State is a snapshot of what is playing, pushed by the UI on every change.
type State struct {
	Status     string // "Playing" | "Paused" | "Stopped"
	TrackID    string
	Title      string
	Artist     string
	Album      string
	ArtURL     string
	LengthUS   int64 // track length in microseconds
	PositionUS int64 // playback position in microseconds
}

// Commands are invoked by the desktop (media keys / overlay) and must drive the
// app's existing playback actions. Any may be nil.
type Commands struct {
	PlayPause   func()
	Next        func()
	Prev        func()
	Stop        func()
	Seek        func(offsetUS int64)              // relative
	SetPosition func(trackID string, posUS int64) // absolute
}

// Controller publishes State to the desktop and is closed on shutdown.
type Controller interface {
	Update(State)
	Close()
}

// noop is the controller used when MPRIS is unavailable (non-Linux, or no bus).
type noop struct{}

func (noop) Update(State) {}
func (noop) Close()       {}
