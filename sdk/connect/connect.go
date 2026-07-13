package connect

import (
	"context"
	"encoding/json"
	"net/url"
	"time"

	internalcontrol "github.com/Cycl0o0/OpenDeezer/v2/internal/control"
	internaldiscovery "github.com/Cycl0o0/OpenDeezer/v2/internal/discovery"
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
//
// Optional staticPeers ("host" or "host:port") are additionally probed via
// unicast, so known devices answer even on networks that filter
// multicast/broadcast (e.g. Tailscale/VPN meshes).
func Discover(timeout time.Duration, selfPort int, staticPeers ...string) ([]Device, error) {
	return internaldiscovery.Discover(timeout, selfPort, staticPeers...)
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

// EQState is a device's equalizer snapshot, returned by [RemoteClient.EQ] and
// [RemoteClient.SetEQ]. It is the same type as control.EQState.
type EQState = internalcontrol.EQState

// Event is a typed control event (a "state" snapshot or a "finished" event).
type Event = internalcontrol.Event

// EQ is the equalizer bridge a [Host] can serve at /eq — wire it via
// Host.Server().SetEQ before Start (see control.PlayerEQ for building one from
// a player). It is the same type as control.EQ.
type EQ = internalcontrol.EQ

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
//   - none           → token="", accountID=""
//
// A device reporting "session" auth (WebRemote mode) is driveable only from its
// embedded browser SPA, which authenticates with a paired session token sent in
// the X-OpenDeezer-Session header (stored in the browser's localStorage, not a
// cookie); RemoteClient has no way to supply that credential and cannot control
// such a device.
func NewRemoteClient(addr, token, accountID string) *RemoteClient {
	return &RemoteClient{c: internalcontrol.NewClient("http://"+addr, token, accountID)}
}

// Whoami fetches the device's identity and auth mode. This endpoint is
// unauthenticated and can be called before supplying credentials.
func (rc *RemoteClient) Whoami() (Whoami, error) { return rc.c.Whoami() }

// Status returns the device's current playback snapshot.
func (rc *RemoteClient) Status() (State, error) { return rc.c.Status() }

// Events subscribes to the device's playback state stream (GET /events,
// Server-Sent Events). Every subscriber receives an initial [State] snapshot
// immediately, then a snapshot on each wire-visible change — no polling. The
// returned channel is closed when ctx is cancelled or the connection to the
// device drops; there is no automatic reconnect (call Events again). Snapshots
// are buffered and coalesced: a slow consumer may skip intermediate snapshots
// but always observes the newest one. It works over LAN like every other
// method — a discovered device streams its state across the network.
func (rc *RemoteClient) Events(ctx context.Context) (<-chan State, error) {
	return rc.c.Events(ctx)
}

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

// CycleRepeat advances the device's repeat mode one step (off → all → one → off).
func (rc *RemoteClient) CycleRepeat() (State, error) { return rc.c.CycleRepeat() }

// ToggleShuffle flips shuffle on/off on the device.
func (rc *RemoteClient) ToggleShuffle() (State, error) { return rc.c.ToggleShuffle() }

// PlayTrack instructs the device to play the track with the given Deezer id.
func (rc *RemoteClient) PlayTrack(id string) (State, error) {
	return rc.c.PlayTrack(id)
}

// PlayPlaylist instructs the device to play the playlist with the given id.
func (rc *RemoteClient) PlayPlaylist(id string) (State, error) {
	return rc.c.PlayPlaylist(id)
}

// PlayAlbum instructs the device to play the album with the given Deezer id
// from its first track (replacing the queue).
func (rc *RemoteClient) PlayAlbum(id string) (State, error) {
	return rc.c.PlayAlbum(id)
}

// PlayMixTrack starts a Deezer mix (radio) seeded by the track with the given id.
func (rc *RemoteClient) PlayMixTrack(id string) (State, error) {
	return rc.c.PlayMixTrack(id)
}

// PlayMixArtist starts a Deezer mix (radio) seeded by the artist with the given id.
func (rc *RemoteClient) PlayMixArtist(id string) (State, error) {
	return rc.c.PlayMixArtist(id)
}

// QueueAdd appends the track with the given Deezer id to the device's queue,
// or inserts it right after the current track when next is true.
func (rc *RemoteClient) QueueAdd(id string, next bool) (State, error) {
	return rc.c.QueueAdd(id, next)
}

// QueueJump jumps playback to the queue entry at index (0-based).
func (rc *RemoteClient) QueueJump(index int) (State, error) {
	return rc.c.QueueJump(index)
}

// QueueRemove removes the queue entry at index (0-based).
func (rc *RemoteClient) QueueRemove(index int) (State, error) {
	return rc.c.QueueRemove(index)
}

// QueueMove moves the queue entry at position from to position to (both 0-based).
func (rc *RemoteClient) QueueMove(from, to int) (State, error) {
	return rc.c.QueueMove(from, to)
}

// SetSleepTimer arms the device's sleep timer: playback fades out and pauses
// after minutes, or when the current track ends if endOfTrack is true (minutes
// is then ignored).
func (rc *RemoteClient) SetSleepTimer(minutes int, endOfTrack bool) (State, error) {
	return rc.c.SetSleepTimer(minutes, endOfTrack)
}

// CancelSleepTimer disarms the device's sleep timer.
func (rc *RemoteClient) CancelSleepTimer() (State, error) { return rc.c.CancelSleepTimer() }

// EQ returns the device's equalizer snapshot (enabled, mono downmix, preamp,
// per-band gains, active preset and the preset/band lists). Errors when the
// device serves no equalizer bridge (its /eq endpoints are 404).
func (rc *RemoteClient) EQ() (EQState, error) { return rc.c.EQ() }

// SetEQ applies a partial equalizer update on the device. params carries any
// of the POST /eq query params — on=0|1, mono=0|1, preset=name, band=N&db=X,
// preamp=dB — and the device applies whichever are present (at least one is
// required).
func (rc *RemoteClient) SetEQ(params url.Values) (EQState, error) { return rc.c.SetEQ(params) }

// Search returns the device's raw search-results JSON for query q
// (GET /search). Errors when the device exposes no browse endpoints.
func (rc *RemoteClient) Search(q string) (json.RawMessage, error) { return rc.c.Search(q) }

// Playlists returns the device account's raw playlists JSON (GET /playlists).
// Errors when the device exposes no browse endpoints.
func (rc *RemoteClient) Playlists() (json.RawMessage, error) { return rc.c.Playlists() }

// HistoryRecent returns the device's recent-listening history as raw JSON,
// most recent first (GET /history/recent). n caps the number of entries
// (1..500). Errors when the device exposes no history.
func (rc *RemoteClient) HistoryRecent(n int) (json.RawMessage, error) {
	return rc.c.HistoryRecent(n)
}

// SetQueue pushes a whole queue to the device (POST /queue/set): tracksJSON is
// the bridge.Track JSON array and index the cursor (-1 for empty). Groundwork
// for host-owned casting. Errors when the device exposes no SetQueue.
func (rc *RemoteClient) SetQueue(tracksJSON string, index int) (State, error) {
	return rc.c.SetQueue(tracksJSON, index)
}

// EventsTyped subscribes to the device's typed event stream (GET /events):
// "state" snapshots plus explicit "finished" events. The channel closes on ctx
// cancel or when the stream drops (no auto-reconnect).
func (rc *RemoteClient) EventsTyped(ctx context.Context) (<-chan Event, error) {
	return rc.c.EventsTyped(ctx)
}
