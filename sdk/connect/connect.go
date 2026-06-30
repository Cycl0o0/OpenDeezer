package connect

import (
	"time"

	internalcontrol "github.com/Cycl0o0/OpenDeezer/internal/control"
	internaldiscovery "github.com/Cycl0o0/OpenDeezer/internal/discovery"
)

// ---- discovery ----

// Device is a discovered OpenDeezer instance.
//
//   - Name    — the account's display name
//   - Addr    — control API address (host:port) to pass to [NewRemoteClient]
//   - Client  — client/platform id (e.g. "tui", "macos", "gnome")
//   - Version — OpenDeezer version
type Device = internaldiscovery.Device

// AdvertiseInfo is the identity advertised to probers. The function you pass
// to [Advertise] returns one of these on each probe so changes (e.g. a
// re-login updating the display name) are reflected immediately.
type AdvertiseInfo = internaldiscovery.Info

// Responder is a running OpenDeezer Connect discovery responder. Stop it with
// Close.
type Responder = internaldiscovery.Responder

// Discover multicasts a discovery probe and returns all OpenDeezer devices
// that reply within timeout.
//
// selfPort is this device's own control port. Replies arriving from a local
// interface with that port are filtered out so you never list yourself. Pass 0
// if you are not running a control server.
func Discover(timeout time.Duration, selfPort int) ([]Device, error) {
	return internaldiscovery.Discover(timeout, selfPort)
}

// Advertise starts an OpenDeezer Connect responder on UDP port 7655. info is
// called on each incoming probe to get the current display name and version.
// controlPort is the TCP port of the local control server that controllers
// should connect to.
//
// Call Close on the returned Responder to stop advertising.
func Advertise(info func() AdvertiseInfo, controlPort int) (*Responder, error) {
	return internaldiscovery.Advertise(info, controlPort)
}

// ---- remote control ----

// State is the playback snapshot returned by [RemoteClient.Status] and all
// mutation methods.
type State = internalcontrol.State

// Track is a now-playing or queue entry in a [State].
type Track = internalcontrol.Track

// Whoami holds a device's identity and the auth mode it requires.
//
//   - Name   — account display name (not the user id)
//   - Offer  — plan name (e.g. "Deezer Premium")
//   - Auth   — "token" | "account" | "session" | "none"
//   - Client — client/platform id
//   - Device — human device label
type Whoami = internalcontrol.Whoami

// Config configures the control endpoint a [Host] exposes (the inbound side).
// It is the same type as control.Config.
type Config = internalcontrol.Config

// Commands are the playback actions an inbound controller can dispatch to this
// device via a [Host]. It is the same type as control.Commands.
type Commands = internalcontrol.Commands

// Account is this device's Deezer identity snapshot, provided to [NewHost].
// It is the same type as control.Account.
type Account = internalcontrol.Account

// RemoteClient drives a discovered OpenDeezer device via its HTTP/JSON control
// API. All mutation methods return the device's post-command playback snapshot.
//
// Thread-safe.
type RemoteClient struct {
	c *internalcontrol.Client
}

// NewRemoteClient builds a remote-control client for the device at addr
// (host:port as returned by [Device.Addr]). Provide token or accountID
// depending on the device's auth mode (see [RemoteClient.Whoami]):
//
//   - token auth     → token="<bearer>", accountID=""
//   - account auth   → token="", accountID="<your Deezer user id>"
//   - session auth   → use the session token obtained by pairing
//   - none           → token="", accountID=""
func NewRemoteClient(addr, token, accountID string) *RemoteClient {
	return &RemoteClient{c: internalcontrol.NewClient("http://"+addr, token, accountID)}
}

// Whoami fetches the device's identity and auth mode. This endpoint is
// unauthenticated and can be called before supplying credentials.
func (rc *RemoteClient) Whoami() (Whoami, error) { return rc.c.Whoami() }

// Status returns the device's current playback snapshot.
func (rc *RemoteClient) Status() (State, error) { return rc.c.Status() }

// PlayPause toggles play/pause on the device.
func (rc *RemoteClient) PlayPause() (State, error) { return rc.c.PlayPause() }

// Next skips to the next track.
func (rc *RemoteClient) Next() (State, error) { return rc.c.Next() }

// Prev jumps to the previous track.
func (rc *RemoteClient) Prev() (State, error) { return rc.c.Prev() }

// Stop halts playback.
func (rc *RemoteClient) Stop() (State, error) { return rc.c.Stop() }

// Restart seeks to position 0 in the current track.
func (rc *RemoteClient) Restart() (State, error) { return rc.c.Restart() }

// SeekMS seeks to ms milliseconds from the start of the current track.
func (rc *RemoteClient) SeekMS(ms int64) (State, error) { return rc.c.Seek(ms) }

// SetVolume sets the volume on the device (0.0 = silent, 1.0 = full).
func (rc *RemoteClient) SetVolume(v float64) (State, error) { return rc.c.SetVolume(v) }

// SetRepeat sets the repeat mode: "off", "all", or "one".
func (rc *RemoteClient) SetRepeat(mode string) (State, error) {
	return rc.c.SetRepeat(mode)
}

// SetShuffle enables (true) or disables (false) shuffle on the device.
func (rc *RemoteClient) SetShuffle(on bool) (State, error) {
	return rc.c.SetShuffle(on)
}

// PlayTrack instructs the device to play the track with the given Deezer id.
func (rc *RemoteClient) PlayTrack(id string) (State, error) {
	return rc.c.PlayTrack(id)
}

// PlayPlaylist instructs the device to play the playlist with the given id.
func (rc *RemoteClient) PlayPlaylist(id string) (State, error) {
	return rc.c.PlayPlaylist(id)
}
