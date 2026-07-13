package main

// OpenDeezer Connect: discover other OpenDeezer instances on the LAN and route
// this client's playback to a chosen one (like Spotify Connect). When a device
// is connected, the playback exports below forward to its control API and read
// its status, so the native GUI's existing transport UI drives the remote device
// with only a small picker added.

/*
#include <stdlib.h>
*/
import "C"

import (
	"net"
	"strconv"
	"time"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/config"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/control"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/discovery"
	odlog "github.com/Cycl0o0/OpenDeezer/v2/internal/log"
)

// selfControlPort is this instance's control API port (0 if disabled), used to
// filter our own responder out of discovery results.
func selfControlPort() int {
	// Capture the shared server pointer under mu (every other reader/writer of
	// ctrlSrv does): reading it unlocked races the settings-toggle writers and a
	// nil store between the check and Addr() would panic across the cgo boundary.
	mu.Lock()
	srv := ctrlSrv
	mu.Unlock()
	if srv == nil {
		return 0
	}
	_, port, err := net.SplitHostPort(srv.Addr())
	if err != nil {
		return 0
	}
	p, _ := strconv.Atoi(port)
	return p
}

var (
	remoteCli  *control.Client // non-nil => playback routed to a remote device
	remoteSt   control.State
	remoteAddr string
	remoteStop chan struct{}
)

// routedRemote returns the connected device's client (nil if playing locally).
func routedRemote() *control.Client {
	mu.Lock()
	defer mu.Unlock()
	return remoteCli
}

func remoteSnapshot() control.State {
	mu.Lock()
	defer mu.Unlock()
	return remoteSt
}

// DZSetClientInfo overrides the client id + device label this instance reports
// over discovery and /whoami (e.g. "macos", "OpenDeezer for macOS"). Call it
// BEFORE DZInit; it has no effect afterwards.
//
//export DZSetClientInfo
func DZSetClientInfo(client, device *C.char) {
	mu.Lock()
	if c := C.GoString(client); c != "" {
		clientID = c
	}
	if d := C.GoString(device); d != "" {
		deviceLabel = d
	}
	mu.Unlock()
}

// DZNowPlayingJSON returns the track actually playing right now as a jTrack:
// when routed to a device it is the remote's current track (so the controller's
// now-playing stays in sync); otherwise the local current track (which also
// reflects tracks started via the control API). "{}" when nothing is playing.
//
//export DZNowPlayingJSON
func DZNowPlayingJSON() *C.char {
	if routedRemote() != nil {
		if t := remoteSnapshot().Track; t != nil {
			return jsonStr(jTrack{
				ID: t.ID, Name: t.Title, ArtistLine: t.Artist, ArtistID: t.ArtistID,
				AlbumName: t.Album, Explicit: t.Explicit, DurationMS: t.DurationMS,
			}, nil)
		}
		return jsonStr(map[string]any{}, nil)
	}
	if cur := currentTrack(); cur.ID != "" {
		return jsonStr(toJTrack(cur), nil)
	}
	return jsonStr(map[string]any{}, nil)
}

// DZDiscoverDevices broadcasts a LAN probe and returns the OpenDeezer devices
// found, as a JSON array of {name, addr}. timeoutMS bounds the wait (~600ms is a
// good default).
//
//export DZDiscoverDevices
func DZDiscoverDevices(timeoutMS C.int) *C.char {
	ms := int(timeoutMS)
	if ms <= 0 {
		ms = 600
	}
	devs, err := discovery.Discover(time.Duration(ms)*time.Millisecond, selfControlPort(), config.PeerHostPorts()...)
	if err != nil {
		// A discovery error is expected/partial (e.g. no multicast on a VPN); don't
		// discard the configured unicast peers merged in below by returning an error
		// object — that would leave the picker empty despite reachable devices.
		odlog.Debug("discover: %v", err)
	}
	if devs == nil {
		devs = []discovery.Device{}
	}
	devs = mergeConfiguredPeers(devs, time.Duration(ms)*time.Millisecond)
	return jsonStr(devs, nil)
}

// mergeConfiguredPeers adds manually-listed peers (config) not already found by
// discovery — querying each /whoami for its name/type/version. Lets Connect work
// over unicast-only networks (Tailscale/VPN) that carry no multicast/broadcast.
//
// The /whoami probes run concurrently and are bounded by timeout: on a VPN mesh
// an offline peer silently drops packets, so a sequential probe would block the
// whole call on the control client's 15s HTTP timeout per dead peer. Peers that
// don't answer in time are still listed by address (without metadata) so the
// user can connect anyway — DZConnectDevice does its own Whoami before routing.
func mergeConfiguredPeers(devs []discovery.Device, timeout time.Duration) []discovery.Device {
	peers := config.LoadPeers()
	if len(peers) == 0 {
		return devs
	}
	seen := map[string]bool{}
	for _, d := range devs {
		seen[d.Addr] = true
	}
	uid := ""
	if c := curClient(); c != nil {
		uid = c.UserID()
	}
	// Collect the peers still to probe (dedup against discovery and each other).
	type probe struct{ base, hp string }
	var todo []probe
	for _, p := range peers {
		base, hp := config.NormalizePeer(p)
		if base == "" || seen[hp] {
			continue
		}
		seen[hp] = true
		todo = append(todo, probe{base, hp})
	}
	if len(todo) == 0 {
		return devs
	}
	// Probe concurrently; a buffered channel means a late responder never blocks
	// its goroutine after we've stopped collecting.
	type result struct {
		hp, name, client, version string
	}
	results := make(chan result, len(todo))
	for _, pr := range todo {
		go func(pr probe) {
			r := result{hp: pr.hp, name: pr.hp}
			if who, err := control.NewClient(pr.base, "", uid).Whoami(); err == nil {
				if who.Name != "" {
					r.name = who.Name
				}
				r.client, r.version = who.Client, who.Version
			}
			results <- r
		}(pr)
	}
	got := map[string]result{}
	deadline := time.After(timeout)
collect:
	for range todo {
		select {
		case r := <-results:
			got[r.hp] = r
		case <-deadline:
			break collect
		}
	}
	// Append in the configured peer order; peers that didn't answer get addr-only.
	for _, pr := range todo {
		if r, ok := got[pr.hp]; ok {
			devs = append(devs, discovery.Device{Name: r.name, Addr: r.hp, Client: r.client, Version: r.version})
		} else {
			devs = append(devs, discovery.Device{Name: pr.hp, Addr: pr.hp})
		}
	}
	return devs
}

// DZConnectDevice routes playback to the device at addr (host:port). Local
// playback is stopped (audio moves to the device). Returns 1 on success.
//
//export DZConnectDevice
func DZConnectDevice(addr *C.char) C.int {
	a := C.GoString(addr)
	c := curClient()
	if c == nil || a == "" {
		return 0
	}
	// Authenticate to discovered devices with the (non-secret) account id only —
	// never the control token: a discovery reply is unauthenticated and spoofable,
	// so sending the shared token would leak it to an attacker's fake device.
	rc := control.NewClient("http://"+a, "", c.UserID())
	// /whoami is served unauthenticated by design, so it can't prove the peer is
	// controllable. Require an AUTHENTICATED /status to succeed BEFORE stopping
	// local playback (mirrors mobile/odmobile.go): a token-protected or
	// different-account peer would otherwise 401 every command, leaving the user
	// with dead local audio and a "connected" UI that drives nothing. (B6)
	st, err := rc.Status()
	if err != nil {
		odlog.Warn("connect %s: %v", a, err)
		return 0
	}
	// Normalize the host's repeat: this controller keeps repeat locally and never
	// forwards it (a Repeat-All would trap the host looping the single-item queue
	// we stream it). Send SetRepeat("off") once so a previously-set host repeat
	// can't strand playback. Best-effort. (B2)
	if _, e := rc.SetRepeat("off"); e != nil {
		odlog.Debug("connect %s: normalize repeat: %v", a, e)
	}
	// Audio moves to the device: stop local playback (only now that the peer is
	// proven controllable).
	withPlayer(func(p *audio.Player) { p.Stop() })

	// Sync the engine's current-track with what's actually playing on the remote,
	// so now-playing / Discord RP / lyrics reflect the remote immediately.
	if st.Track != nil {
		setCurrentTrack(deezer.Track{
			ID: st.Track.ID, Name: st.Track.Title, DurationMS: st.Track.DurationMS,
			Artists:   []deezer.Artist{{ID: st.Track.ArtistID, Name: st.Track.Artist}},
			AlbumName: st.Track.Album, Explicit: st.Track.Explicit,
		})
	}

	mu.Lock()
	oldRc := remoteCli // switching A->B: capture A's client to stop it below
	if remoteStop != nil {
		close(remoteStop)
	}
	remoteStop = make(chan struct{})
	stop := remoteStop
	remoteCli = rc
	remoteSt = st
	remoteAddr = a
	mu.Unlock()

	// Switching devices A->B: stop the OLD device so it doesn't keep playing
	// unattended. Fire-and-forget on its own goroutine (rc.Stop is a network
	// round-trip bounded by the control client's own HTTP timeout) so the connect
	// returns immediately. (B7)
	if oldRc != nil && oldRc != rc {
		go func() { _, _ = oldRc.Stop() }()
	}

	go remotePoller(rc, stop)
	odlog.Info("connected to device %s", a)
	return 1
}

// DZDisconnectDevice returns control to local playback. It stops the remote
// device (so it doesn't keep playing unattended) before clearing the connection.
//
//export DZDisconnectDevice
func DZDisconnectDevice() {
	mu.Lock()
	rc := remoteCli // capture before clearing (rc.Stop is a network call — done outside lock)
	if remoteStop != nil {
		close(remoteStop)
		remoteStop = nil
	}
	remoteCli = nil
	remoteSt = control.State{}
	remoteAddr = ""
	mu.Unlock()
	if rc != nil {
		_, _ = rc.Stop() // halt the remote; ignore error (fire-and-forget)
	}
}

// DZConnectedDevice returns the connected device's address ("" if local).
//
//export DZConnectedDevice
func DZConnectedDevice() *C.char {
	mu.Lock()
	a := remoteAddr
	mu.Unlock()
	return C.CString(a)
}

// remotePoller refreshes the cached remote status once a second until stopped.
// Each poll also syncs the engine's current-track so now-playing / Discord RP /
// lyrics reflect what the remote is actually playing as it changes.
func remotePoller(rc *control.Client, stop chan struct{}) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			st, err := rc.Status()
			if err != nil {
				continue
			}
			mu.Lock()
			active := remoteCli == rc // still the active device?
			if active {
				// The remote renders single tracks we send it; it has no queue of
				// its own — THIS device drives the queue. When the remote's track
				// ends it goes playing -> stopped near the end, so bump the
				// finished counter to fire this device's auto-advance, which then
				// sends the next track (honoring repeat/shuffle). The position
				// guard keeps a user-initiated mid-track stop from auto-advancing.
				if remoteSt.State == "playing" && st.State == "stopped" &&
					remoteSt.DurationMS > 0 && remoteSt.PositionMS >= remoteSt.DurationMS-2000 {
					finished++
				}
				remoteSt = st
			}
			mu.Unlock()
			// Sync current-track outside the lock (setCurrentTrack uses its own mutex).
			if active && st.Track != nil {
				setCurrentTrack(deezer.Track{
					ID: st.Track.ID, Name: st.Track.Title, DurationMS: st.Track.DurationMS,
					Artists:   []deezer.Artist{{ID: st.Track.ArtistID, Name: st.Track.Artist}},
					AlbumName: st.Track.Album, Explicit: st.Track.Explicit,
				})
			}
		}
	}
}

// setRemoteState caches a status returned by a command issued to rc (so the UI
// updates without waiting for the next poll). It applies ONLY when rc is still
// the CURRENT remote client: a late in-flight response from a device we've
// since disconnected from — or switched away from (A while B is now active) —
// must not resurrect stale state or bump the finished counter for the wrong
// device. (B7)
func setRemoteState(rc *control.Client, st control.State) {
	mu.Lock()
	if remoteCli != nil && remoteCli == rc {
		// A command's status response can be the first observer of the remote's
		// track end (playing -> stopped near the end), so detect it here too — not
		// only in remotePoller — and bump finished to fire this device's
		// auto-advance. Otherwise the next poll sees stopped -> stopped and never
		// advances. The position guard keeps a user-initiated stop from advancing.
		if remoteSt.State == "playing" && st.State == "stopped" &&
			remoteSt.DurationMS > 0 && remoteSt.PositionMS >= remoteSt.DurationMS-2000 {
			finished++
		}
		remoteSt = st
	}
	mu.Unlock()
}

// remoteStateInt maps a control State string to the audio.State int the GUIs use.
func remoteStateInt(s string) int {
	switch s {
	case "playing":
		return int(audio.Playing)
	case "paused":
		return int(audio.Paused)
	case "loading":
		return int(audio.Loading)
	case "error":
		return int(audio.Errored)
	default:
		return int(audio.Stopped)
	}
}
