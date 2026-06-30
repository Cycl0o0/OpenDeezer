package connect

import (
	"net"
	"strconv"

	sdkcontrol "github.com/Cycl0o0/OpenDeezer/sdk/control"
	sdkdeezer "github.com/Cycl0o0/OpenDeezer/sdk/deezer"
)

// This file is the inbound ("in") side of OpenDeezer Connect: making this
// process a device that other OpenDeezer clients can discover on the LAN and
// drive. It pairs a control server (the thing a [RemoteClient] talks to) with a
// discovery responder (the thing [Discover] finds), so becoming a Connect
// device is a single call.
//
// The two directions of OpenDeezer Connect are symmetric:
//
//   - out — discover and control other devices: [Discover] + [RemoteClient]
//   - in  — be discoverable and controllable:   [Host] + [Advertise]

// HostConfig configures a [Host]: how the control endpoint binds and
// authenticates, plus the identity advertised over LAN discovery.
type HostConfig struct {
	// Control is the control-endpoint configuration (listen address and auth
	// mode). For a LAN-reachable device, bind a non-loopback address such as
	// ":7654" and set Token or SameAccountOnly.
	Control Config

	// Name is the advertised display name. When an account snapshot is provided
	// to [NewHost] and reports a non-empty name, that name is used instead so a
	// re-login is reflected automatically.
	Name string
	// Client is the advertised client/platform id (e.g. "myapp").
	Client string
	// Version is the advertised OpenDeezer/app version.
	Version string
}

// Host is the inbound side of OpenDeezer Connect: a control endpoint other
// devices can drive, advertised on the LAN so [Discover] finds it. It is the
// mirror image of [RemoteClient] (which drives a host) and pairs with
// [Advertise]/[Discover].
//
// Construct one with [NewHost], call [Host.Start] to bind and advertise, and
// [Host.Close] to stop. Host is safe for concurrent use once started.
type Host struct {
	srv     *sdkcontrol.Server
	resp    *Responder
	account func() Account
	name    string
	client  string
	version string
}

// NewHost builds a Connect host.
//
//   - cfg     — control bind/auth + advertised identity
//   - status  — playback snapshot provider (race-free reads)
//   - account — logged-in identity provider; its Name is preferred for the
//               advertised name. Pass nil to advertise cfg.Name only.
//   - cmds    — the actions remote controllers can dispatch (same command set
//               a [RemoteClient] sends)
//   - dz      — Deezer client for the endpoint's browse routes; nil to disable
func NewHost(
	cfg HostConfig,
	status func() State,
	account func() Account,
	cmds Commands,
	dz *sdkdeezer.Client,
) *Host {
	srv := sdkcontrol.NewServer(cfg.Control, status, account, cmds, dz)
	srv.SetClientInfo(cfg.Client, cfg.Version) // device label defaults to client id
	srv.SetVersion(cfg.Version)
	return &Host{
		srv:     srv,
		account: account,
		name:    cfg.Name,
		client:  cfg.Client,
		version: cfg.Version,
	}
}

// Server returns the underlying control server so callers can enable web-remote
// pairing ([sdkcontrol.Server.EnablePairing]), read the listen address, or
// override the device label.
func (h *Host) Server() *sdkcontrol.Server { return h.srv }

// Addr returns the control endpoint's listen address (valid after [Host.Start]).
func (h *Host) Addr() string { return h.srv.Addr() }

// Start binds the control endpoint and begins advertising it on the LAN via
// OpenDeezer Connect (UDP port 7655). The advertised control port is taken
// from the endpoint's actual listen address.
//
// Advertising is only useful when the control endpoint binds a LAN-reachable
// (non-loopback) address; a loopback bind is advertised but reachable only on
// this machine.
func (h *Host) Start() error {
	if err := h.srv.Start(); err != nil {
		return err
	}
	resp, err := Advertise(h.advertiseInfo, portOf(h.srv.Addr()))
	if err != nil {
		// The control endpoint is up; surface the advertising error but keep it
		// serving so a caller on a multicast-less network can still reach this
		// device by address.
		return err
	}
	h.resp = resp
	return nil
}

// Close stops advertising and shuts down the control endpoint.
func (h *Host) Close() {
	if h.resp != nil {
		h.resp.Close()
		h.resp = nil
	}
	h.srv.Close()
}

// advertiseInfo is called on each discovery probe. It prefers the live account
// name (so a re-login updates what controllers see) and falls back to the
// configured name.
func (h *Host) advertiseInfo() AdvertiseInfo {
	name := h.name
	if h.account != nil {
		if n := h.account().Name; n != "" {
			name = n
		}
	}
	return AdvertiseInfo{Name: name, Client: h.client, Version: h.version}
}

// portOf extracts the numeric TCP port from a host:port address (0 on failure).
func portOf(addr string) int {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(p)
	return n
}
