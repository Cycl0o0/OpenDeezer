package main

// Web remote C exports: enable/disable the phone web remote, query pairing info,
// and generate the QR code PNG the GUI can display for easy phone pairing.

/*
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/config"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/control"
)

// DZControlConfigJSON returns the current remote-control settings for the
// Settings UI: {enabled, addr, token, lan, running}. Lets a GUI show the same
// values env vars / config files provide, so they become editable in-app.
//
//export DZControlConfigJSON
func DZControlConfigJSON() *C.char {
	cfg := config.LoadControl()
	mu.Lock()
	running := ctrlSrv != nil
	addr := cfg.Addr
	if running {
		addr = ctrlSrv.Addr()
	}
	mu.Unlock()
	if addr == "" {
		addr = "127.0.0.1:7654"
	}
	return jsonStr(map[string]any{
		"enabled": cfg.Enabled || running,
		"addr":    addr,
		"token":   cfg.Token,
		"lan":     !config.IsLoopbackAddr(addr),
		"running": running,
	}, nil)
}

// resolveControlAddr turns a UI address hint into a bindable host:port.
func resolveControlAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	switch {
	case addr == "" || addr == "1" || addr == "on" || addr == "true":
		return "127.0.0.1:7654"
	case strings.HasPrefix(addr, ":"):
		return "0.0.0.0" + addr
	default:
		return addr
	}
}

// DZSetControlConfig persists the remote-control settings to the config files the
// engine reads at startup (~/.config/opendeezer/{control.txt,control-token.txt})
// AND applies them live: (re)starts the control server when enabled (LAN binds
// require same-account auth when no token is set), stops it when disabled.
//
//export DZSetControlConfig
func DZSetControlConfig(enabled C.int, addr *C.char, token *C.char) {
	on := enabled != 0
	a := strings.TrimSpace(C.GoString(addr))
	tok := strings.TrimSpace(C.GoString(token))

	// Persist for next launch (same files env/config provide today).
	if base, err := os.UserConfigDir(); err == nil {
		dir := filepath.Join(base, "opendeezer")
		_ = os.MkdirAll(dir, 0o755)
		val := ""
		if on {
			if a != "" {
				val = a
			} else {
				val = "1"
			}
		}
		_ = os.WriteFile(filepath.Join(dir, "control.txt"), []byte(val), 0o600)
		_ = os.WriteFile(filepath.Join(dir, "control-token.txt"), []byte(tok), 0o600)
	}

	// Apply live: tear down any running server, then start fresh if enabled.
	mu.Lock()
	old := ctrlSrv
	ctrlSrv = nil
	// Clear the tracked config/account alongside the server pointer: leaving them
	// set while ctrlSrv is nil could let a later refreshControlServer rebuild from
	// a config that no longer matches the live server. (B5)
	ctrlCfg, ctrlSrvUserID = control.Config{}, ""
	c := client
	mu.Unlock()
	if old != nil {
		old.Close()
	}
	if !on {
		refreshAdvertiser() // server gone: Close the LAN responder (no ghost port) (B10)
		return
	}
	bind := resolveControlAddr(a)
	id, dev := clientInfo()
	ccfg := control.Config{Addr: bind, Token: tok, SameAccountOnly: !config.IsLoopbackAddr(bind) && tok == ""}
	srv := control.New(ccfg, engineState, engineAccount, engineCommands(), c)
	srv.SetVersion(coreVersion)
	srv.SetClientInfo(id, dev)
	srv.SetEQ(engineEQ())
	if err := srv.Start(); err != nil {
		return
	}
	// Publish the new server AND the config/account it was built from atomically,
	// so a later account refresh (refreshControlServer) rebuilds from the current
	// config instead of a stale one (which could resurrect an old token or drop
	// auth). (B5)
	mu.Lock()
	ctrlSrv = srv
	ctrlCfg = ccfg
	ctrlSrvUserID = ""
	if c != nil {
		ctrlSrvUserID = c.Account().UserID
	}
	mu.Unlock()
	refreshAdvertiser() // server rebuilt (possibly new port): re-point the responder (B10)
}

// DZWebRemoteSetEnabled enables (on!=0) or disables (on==0) the phone web
// remote. When enabling, the control server is started (or rebound) on a
// LAN-reachable address (0.0.0.0:<port>) with pairing active, so any phone on
// the same network can scan the QR and connect. When disabling, the pairing
// code is cleared; active session tokens remain valid for their remaining TTL.
// Off by default — call this explicitly to turn it on.
//
//export DZWebRemoteSetEnabled
func DZWebRemoteSetEnabled(on C.int) {
	if on != 0 {
		ensureWebRemoteServer()
	} else {
		mu.Lock()
		srv := ctrlSrv
		mu.Unlock()
		if srv != nil {
			srv.DisablePairing()
		}
	}
}

// DZWebRemoteInfoJSON returns a malloc'd JSON string (free with DZFree):
//
//	{"enabled":bool,"code":"123456","url":"http://<lanip>:<port>/remote","port":<int>}
//
// code and url are empty strings when the remote is disabled.
//
//export DZWebRemoteInfoJSON
func DZWebRemoteInfoJSON() *C.char {
	mu.Lock()
	srv := ctrlSrv
	mu.Unlock()
	if srv == nil || !srv.PairingActive() {
		b, _ := json.Marshal(map[string]any{"enabled": false, "code": "", "url": "", "port": 0})
		return C.CString(string(b))
	}
	port := webRemotePort(srv)
	url := fmt.Sprintf("http://%s:%d/remote", lanIPv4(), port)
	b, _ := json.Marshal(map[string]any{
		"enabled": true,
		"code":    srv.PairingCode(),
		"url":     url,
		"port":    port,
	})
	return C.CString(string(b))
}

// DZWebRemoteQRPNG returns a malloc'd PNG buffer (free with DZFreeBytes) for a
// QR code encoding the web remote URL, writing its size to *outLen. Returns
// nil/0 when the remote is disabled or the URL is unavailable.
//
//export DZWebRemoteQRPNG
func DZWebRemoteQRPNG(outLen *C.int) *C.uchar {
	*outLen = 0
	mu.Lock()
	srv := ctrlSrv
	mu.Unlock()
	if srv == nil || !srv.PairingActive() {
		return nil
	}
	port := webRemotePort(srv)
	url := fmt.Sprintf("http://%s:%d/remote", lanIPv4(), port)

	b, err := qrcode.Encode(url, qrcode.Medium, 512)
	if err != nil || len(b) == 0 {
		return nil
	}
	p := C.malloc(C.size_t(len(b)))
	if p == nil {
		return nil
	}
	C.memcpy(p, unsafe.Pointer(&b[0]), C.size_t(len(b)))
	*outLen = C.int(len(b))
	return (*C.uchar)(p)
}

// webRemotePort returns the TCP port the control server is listening on.
func webRemotePort(srv *control.Server) int {
	_, port, err := net.SplitHostPort(srv.Addr())
	if err != nil {
		return 7654
	}
	p, _ := strconv.Atoi(port)
	return p
}

// lanIPv4 returns the primary non-loopback IPv4 address of this machine,
// suitable for building a LAN-reachable URL. Falls back to "127.0.0.1".
func lanIPv4() string {
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip4 := ip.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}
	return "127.0.0.1"
}

// ensureWebRemoteServer guarantees the control server is running on a
// LAN-reachable (0.0.0.0) address with pairing active. Called when the user
// enables the web remote. If a loopback-only server is running it is stopped
// and a new one is bound on all interfaces using the same port.
func ensureWebRemoteServer() {
	mu.Lock()
	srv := ctrlSrv
	mu.Unlock()

	c := curClient()
	id, dev := clientInfo()

	// Preserve any configured token / same-account auth so MCP and Connect keep
	// working after the web remote rebinds the server — Server.auth() evaluates
	// the pairing session token first, so phone pairing coexists with these.
	// Dropping them would silently downgrade a token-protected control API to
	// session-only auth and 401 every MCP/Connect request until restart.
	cfg := config.LoadControl()
	ccfg := control.Config{Token: cfg.Token, SameAccountOnly: cfg.SameAccount, WebRemote: true}

	startNew := func(addr string) *control.Server {
		sc := ccfg
		sc.Addr = addr
		s := control.New(sc, engineState, engineAccount, engineCommands(), c)
		s.SetVersion(coreVersion)
		s.SetClientInfo(id, dev)
		s.SetEQ(engineEQ())
		if err := s.Start(); err != nil {
			return nil
		}
		return s
	}

	// publish records the (re)built server AND the config/account it was built
	// from atomically, so a later account refresh (refreshControlServer) rebuilds
	// from the CURRENT config (WebRemote + token/account) rather than a stale one.
	// (B5)
	publish := func(s *control.Server) {
		pc := ccfg
		pc.Addr = s.Addr()
		mu.Lock()
		ctrlSrv = s
		ctrlCfg = pc
		ctrlSrvUserID = ""
		if c != nil {
			ctrlSrvUserID = c.Account().UserID
		}
		mu.Unlock()
	}

	if srv != nil {
		if !config.IsLoopbackAddr(srv.Addr()) {
			// Already LAN-reachable; just activate pairing. Refresh the tracked
			// config to WebRemote:true so a later rebuild keeps the web remote on.
			publish(srv)
			srv.EnablePairing()
			refreshAdvertiser() // port unchanged, but keep the responder current (B10)
			return
		}
		// Loopback-only: close it and rebind on all interfaces.
		_, portStr, _ := net.SplitHostPort(srv.Addr())
		srv.Close()
		newSrv := startNew("0.0.0.0:" + portStr)
		if newSrv == nil {
			newSrv = startNew("0.0.0.0:0")
		}
		if newSrv == nil {
			return
		}
		publish(newSrv)
		newSrv.EnablePairing()
		refreshAdvertiser() // rebound on a LAN port: re-point the responder (B10)
		return
	}

	// No server yet: start one on the default control port, with :0 as fallback.
	newSrv := startNew("0.0.0.0:7654")
	if newSrv == nil {
		newSrv = startNew("0.0.0.0:0")
	}
	if newSrv == nil {
		return
	}
	publish(newSrv)
	newSrv.EnablePairing()
	refreshAdvertiser() // fresh LAN server: advertise it (B10)
}
