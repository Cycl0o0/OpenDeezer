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
	"strconv"
	"unsafe"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/Cycl0o0/OpenDeezer/internal/config"
	"github.com/Cycl0o0/OpenDeezer/internal/control"
)

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

	startNew := func(addr string) *control.Server {
		s := control.New(
			control.Config{Addr: addr, WebRemote: true},
			engineState, engineAccount, engineCommands(), c,
		)
		s.SetVersion(coreVersion)
		s.SetClientInfo(id, dev)
		if err := s.Start(); err != nil {
			return nil
		}
		return s
	}

	if srv != nil {
		if !config.IsLoopbackAddr(srv.Addr()) {
			// Already LAN-reachable; just activate pairing.
			srv.EnablePairing()
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
		mu.Lock()
		ctrlSrv = newSrv
		mu.Unlock()
		newSrv.EnablePairing()
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
	mu.Lock()
	ctrlSrv = newSrv
	mu.Unlock()
	newSrv.EnablePairing()
}
