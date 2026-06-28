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
	"time"

	"github.com/Cycl0o0/OpenDeezer/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/internal/control"
	"github.com/Cycl0o0/OpenDeezer/internal/discovery"
	odlog "github.com/Cycl0o0/OpenDeezer/internal/log"
)

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
				ID: t.ID, Name: t.Title, ArtistLine: t.Artist, AlbumName: t.Album,
				Explicit: t.Explicit, DurationMS: t.DurationMS,
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
	devs, err := discovery.Discover(time.Duration(ms) * time.Millisecond)
	if devs == nil {
		devs = []discovery.Device{}
	}
	return jsonStr(devs, err)
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

// DZDisconnectDevice returns control to local playback.
//
//export DZDisconnectDevice
func DZDisconnectDevice() {
	mu.Lock()
	if remoteStop != nil {
		close(remoteStop)
		remoteStop = nil
	}
	remoteCli = nil
	remoteSt = control.State{}
	remoteAddr = ""
	mu.Unlock()
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
			if remoteCli == rc { // still the active device
				remoteSt = st
			}
			mu.Unlock()
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
