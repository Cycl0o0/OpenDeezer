//go:build windows

package discord

import (
	"fmt"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
)

// dialIPC finds and connects to Discord's IPC named pipe.
func dialIPC() (net.Conn, error) {
	for i := 0; i < 10; i++ {
		p := fmt.Sprintf(`\\?\pipe\discord-ipc-%d`, i)
		timeout := 2 * time.Second
		if c, err := winio.DialPipe(p, &timeout); err == nil {
			return c, nil
		}
	}
	return nil, errNoIPC
}
