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

	"github.com/Cycl0o0/OpenDeezer/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/internal/config"
	"github.com/Cycl0o0/OpenDeezer/internal/control"
	"github.com/Cycl0o0/OpenDeezer/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/internal/discovery"
	odlog "github.com/Cycl0o0/OpenDeezer/internal/log"
)

// selfControlPort is this instance's control API port (0 if disabled), used to
// filter our own responder out of discovery results.
func selfControlPort() int {
	if ctrlSrv == nil {
		return 0
	}
	_, port, err := net.SplitHostPort(ctrlSrv.Addr())
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
	devs, err := discovery.Discover(time.Duration(ms)*time.Millisecond, selfControlPort())
	if devs == nil {
		devs = []discovery.Device{}
	}
	devs = mergeConfiguredPeers(devs)
	return jsonStr(devs, err)
}

// mergeConfiguredPeers adds manually-listed peers (config) not already found by
// discovery — querying each /whoami for its name/type/version. Lets Connect work
// over unicast-only networks (Tailscale/VPN) that carry no multicast/broadcast.
func mergeConfiguredPeers(devs []discovery.Device) []discovery.Device {
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
	for _, p := range peers {
		base, hp := config.NormalizePeer(p)
		if base == "" || seen[hp] {
			continue
		}
		seen[hp] = true
		who, err := control.NewClient(base, "", uid).Whoami()
		name := hp
		client, version := "", ""
		if err == nil {
			if who.Name != "" {
				name = who.Name
			}
			client, version = who.Client, who.Version
		}
		devs = append(devs, discovery.Device{Name: name, Addr: hp, Client: client, Version: version})
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
	if _, err := rc.Whoami(); err != nil {
		odlog.Warn("connect %s: %v", a, err)
		return 0
	}
	// Audio moves to the device: stop local playback.
	withPlayer(func(p *audio.Player) { p.Stop() })
	st, _ := rc.Status()

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
	if remoteStop != nil {
		close(remoteStop)
	}
	remoteStop = make(chan struct{})
	stop := remoteStop
	remoteCli = rc
	remoteSt = st
	remoteAddr = a
	mu.Unlock()

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

// setRemoteState caches a status returned by a command (so the UI updates
// without waiting for the next poll). No-op if we've since disconnected, so a
// late command response can't resurrect stale remote state.
func setRemoteState(st control.State) {
	mu.Lock()
	if remoteCli != nil {
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
