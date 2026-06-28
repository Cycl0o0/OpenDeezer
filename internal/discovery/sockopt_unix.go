//go:build !windows

package discovery

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// setReusePort enables SO_REUSEADDR + SO_REUSEPORT so several OpenDeezer
// instances on one host can all bind UDP :Port and receive multicast probes.
func setReusePort(c syscall.RawConn) error {
	var serr error
	if err := c.Control(func(fd uintptr) {
		if serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); serr != nil {
			return
		}
		serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
	}); err != nil {
		return err
	}
	return serr
}

// setBroadcast enables SO_BROADCAST so the discover socket may send to the
// limited broadcast address.
func setBroadcast(c syscall.RawConn) error {
	var serr error
	if err := c.Control(func(fd uintptr) {
		serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_BROADCAST, 1)
	}); err != nil {
		return err
	}
	return serr
}
