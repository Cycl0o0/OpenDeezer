//go:build windows

package discovery

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// setReusePort: Windows has no SO_REUSEPORT; SO_REUSEADDR allows several
// sockets to bind the same UDP port (and receive multicast).
func setReusePort(c syscall.RawConn) error {
	var serr error
	if err := c.Control(func(fd uintptr) {
		serr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_REUSEADDR, 1)
	}); err != nil {
		return err
	}
	return serr
}

// setBroadcast enables SO_BROADCAST for the limited-broadcast fallback.
func setBroadcast(c syscall.RawConn) error {
	var serr error
	if err := c.Control(func(fd uintptr) {
		serr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_BROADCAST, 1)
	}); err != nil {
		return err
	}
	return serr
}
