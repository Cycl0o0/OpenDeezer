package control

import (
	internalcontrol "github.com/Cycl0o0/OpenDeezer/internal/control"
	sdkdeezer "github.com/Cycl0o0/OpenDeezer/sdk/deezer"
)

// ---- type aliases ----

// Config configures a [Server].
//
//   - Addr            — listen address (e.g. "127.0.0.1:7654" or ":7654")
//   - Token           — bearer token; set to enable token auth
//   - SameAccountOnly — when Token is empty, require the controller to prove it
//                       is logged into the same Deezer account
//   - WebRemote       — serve the phone web-remote SPA at GET /remote and
//                       use pairing (6-digit code) as the auth mechanism
type Config = internalcontrol.Config

// Commands are the playback actions a [Server] dispatches. Set only the
// functions your application implements; nil entries cause the corresponding
// endpoint to be a no-op.
type Commands = internalcontrol.Commands

// State is the playback snapshot returned by GET /status and all mutation
// endpoints. It is also the return type of all [Client] mutation methods.
type State = internalcontrol.State

// Track is a now-playing or queue entry in a [State].
type Track = internalcontrol.Track

// Account is the controlled client's Deezer identity, provided to [NewServer]
// via a snapshot callback. UserID is the credential in same-account auth mode.
type Account = internalcontrol.Account

// Whoami is the unauthenticated identity returned by GET /whoami. It carries
// the account display Name but never the UserID (which is the credential in
// same-account mode).
type Whoami = internalcontrol.Whoami

// ---- Server ----

// Server hosts the OpenDeezer remote-control API. Construct one with
// [NewServer]; call [Server.Start] to bind the port; call [Server.Close] when
// done.
//
// Server is safe for concurrent use once started.
type Server struct {
	s *internalcontrol.Server
}

// NewServer builds a control server.
//
//   - cfg     — listen address and auth mode
//   - status  — called on each request; must return a race-free snapshot of
//               the current playback state
//   - account — called on each request; must return the logged-in identity
//   - cmds    — the actions the server can dispatch to your player
//   - dz      — Deezer client used to serve GET /search and GET /playlists;
//               pass nil to disable browse endpoints
func NewServer(
	cfg Config,
	status func() State,
	account func() Account,
	cmds Commands,
	dz *sdkdeezer.Client,
) *Server {
	inner := sdkdeezer.Unwrap(dz) // nil-safe: returns nil when dz is nil
	return &Server{s: internalcontrol.New(cfg, status, account, cmds, inner)}
}

// Start binds the port and begins serving in a background goroutine.
func (s *Server) Start() error { return s.s.Start() }

// Close stops the server and releases the port.
func (s *Server) Close() { s.s.Close() }

// Addr returns the actual listen address (valid after [Server.Start]).
func (s *Server) Addr() string { return s.s.Addr() }

// SetVersion records the app version reported by GET /whoami.
func (s *Server) SetVersion(v string) { s.s.SetVersion(v) }

// SetClientInfo records the client/platform id and human device label for
// GET /whoami (e.g. "myapp", "My Player v1.0").
func (s *Server) SetClientInfo(client, device string) { s.s.SetClientInfo(client, device) }

// EnablePairing mints a fresh 6-digit pairing code, activates the pairing
// flow, and returns the code. Display it to the user; they enter it in the
// phone web remote at http://<addr>/remote. Each call resets the code.
func (s *Server) EnablePairing() string { return s.s.EnablePairing() }

// DisablePairing clears the pairing code. Existing valid session tokens remain
// usable for their remaining TTL (12 hours).
func (s *Server) DisablePairing() { s.s.DisablePairing() }

// PairingActive reports whether a pairing code is currently active.
func (s *Server) PairingActive() bool { return s.s.PairingActive() }

// PairingCode returns the current 6-digit code, or an empty string when
// pairing is not active.
func (s *Server) PairingCode() string { return s.s.PairingCode() }

// ---- Client ----

// Client talks to a [Server] (or any compatible control endpoint) over HTTP.
// All mutation methods return the post-command playback snapshot. The snapshot
// may lag the command by one server tick; poll GET /status if you need the
// settled state.
//
// Client is safe for concurrent use.
type Client struct {
	c *internalcontrol.Client
}

// NewClient builds a control client.
//
//   - base      — server URL, e.g. "http://192.168.1.5:7654"
//   - token     — X-OpenDeezer-Token value; "" to omit
//   - accountID — X-OpenDeezer-Account value for same-account auth; "" to omit
func NewClient(base, token, accountID string) *Client {
	return &Client{c: internalcontrol.NewClient(base, token, accountID)}
}

// Whoami fetches the server's identity and auth mode. This endpoint is
// unauthenticated and is safe to call before supplying credentials.
func (c *Client) Whoami() (Whoami, error) { return c.c.Whoami() }

// Status returns the current playback snapshot.
func (c *Client) Status() (State, error) { return c.c.Status() }

// PlayPause toggles play/pause on the server.
func (c *Client) PlayPause() (State, error) { return c.c.PlayPause() }

// Next skips to the next track.
func (c *Client) Next() (State, error) { return c.c.Next() }

// Prev jumps to the previous track.
func (c *Client) Prev() (State, error) { return c.c.Prev() }

// Stop halts playback.
func (c *Client) Stop() (State, error) { return c.c.Stop() }

// Restart seeks to position 0 in the current track.
func (c *Client) Restart() (State, error) { return c.c.Restart() }

// SeekMS seeks to ms milliseconds from the start of the current track.
func (c *Client) SeekMS(ms int64) (State, error) { return c.c.Seek(ms) }

// SetVolume sets the volume (0.0 = silent, 1.0 = full).
func (c *Client) SetVolume(v float64) (State, error) { return c.c.SetVolume(v) }

// SetRepeat sets the repeat mode: "off", "all", or "one".
func (c *Client) SetRepeat(mode string) (State, error) { return c.c.SetRepeat(mode) }

// SetShuffle enables (true) or disables (false) shuffle.
func (c *Client) SetShuffle(on bool) (State, error) { return c.c.SetShuffle(on) }

// PlayTrack instructs the server to play the track with the given Deezer id.
func (c *Client) PlayTrack(id string) (State, error) { return c.c.PlayTrack(id) }

// PlayPlaylist instructs the server to play the playlist with the given id.
func (c *Client) PlayPlaylist(id string) (State, error) { return c.c.PlayPlaylist(id) }
