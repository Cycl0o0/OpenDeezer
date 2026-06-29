package ui

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/Cycl0o0/OpenDeezer/internal/control"
	qrcode "github.com/skip2/go-qrcode"
	tea "github.com/charmbracelet/bubbletea"
)

// webRemoteMsg is the result of enabling or disabling the phone web remote.
type webRemoteMsg struct {
	srv          *control.Server // new/active server; nil when disabling
	replacedCtrl bool            // true when srv replaced the old m.ctrl (loopback rebind)
	enabled      bool
	code         string // 6-digit pairing code
	url          string // http://<lan-ip>:<port>/remote
	qr           string // terminal QR block from qrcode.ToSmallString
	errStr       string
}

// webRemoteEnableCmd starts the phone web remote, binding LAN-reachably if
// needed and enabling pairing. Mirrors corelib/webremote.go ensureWebRemoteServer.
func (m *Model) webRemoteEnableCmd() tea.Cmd {
	// Capture everything we need before the closure runs off the update loop.
	existingCtrl := m.ctrl
	existingWR := m.webRemoteSrv
	send := m.ctrlSend
	statusFn := m.ctrlState.Load
	acctFn := m.acctSnap.Load
	client := m.client

	buildCmds := func() control.Commands {
		if send == nil {
			return control.Commands{}
		}
		return control.Commands{
			PlayPause:     func() { send(controlCmdMsg{kind: "playpause"}) },
			Next:          func() { send(controlCmdMsg{kind: "next"}) },
			Prev:          func() { send(controlCmdMsg{kind: "prev"}) },
			Stop:          func() { send(controlCmdMsg{kind: "stop"}) },
			Restart:       func() { send(controlCmdMsg{kind: "restart"}) },
			CycleRepeat:   func() { send(controlCmdMsg{kind: "repeat"}) },
			ToggleShuffle: func() { send(controlCmdMsg{kind: "shuffle"}) },
			Seek:          func(ms int64) { send(controlCmdMsg{kind: "seek", ms: ms}) },
			SetVolume:     func(v float64) { send(controlCmdMsg{kind: "volume", vol: v}) },
			PlayTrack:     func(id string) { send(controlCmdMsg{kind: "playtrack", id: id}) },
			PlayPlaylist:  func(id string) { send(controlCmdMsg{kind: "playplaylist", id: id}) },
		}
	}

	statusSnap := func() control.State {
		if p := statusFn(); p != nil {
			return *p
		}
		return control.State{State: "stopped"}
	}
	acctSnap := func() control.Account {
		if p := acctFn(); p != nil {
			return *p
		}
		return control.Account{}
	}

	return func() tea.Msg {
		// Case 1: dedicated web-remote server already LAN-reachable.
		if existingWR != nil && !isLoopbackAddr(existingWR.Addr()) {
			existingWR.EnablePairing()
			return buildWebRemoteMsg(existingWR, false)
		}
		// Case 2: existing control server is already on a LAN address.
		if existingCtrl != nil && !isLoopbackAddr(existingCtrl.Addr()) {
			existingCtrl.EnablePairing()
			return buildWebRemoteMsg(existingCtrl, false)
		}

		startNew := func(addr string) *control.Server {
			s := control.New(
				control.Config{Addr: addr, WebRemote: true},
				statusSnap, acctSnap, buildCmds(), client,
			)
			s.SetVersion(Version)
			s.SetClientInfo("tui", "OpenDeezer TUI")
			if err := s.Start(); err != nil {
				return nil
			}
			return s
		}

		// Case 3: existing control server is loopback — close and rebind LAN.
		if existingCtrl != nil {
			_, portStr, _ := net.SplitHostPort(existingCtrl.Addr())
			existingCtrl.Close()
			newSrv := startNew("0.0.0.0:" + portStr)
			if newSrv == nil {
				newSrv = startNew("0.0.0.0:0")
			}
			if newSrv == nil {
				return webRemoteMsg{errStr: "failed to bind web remote server"}
			}
			newSrv.EnablePairing()
			return buildWebRemoteMsg(newSrv, true) // replaced ctrl
		}

		// Case 4: no server yet — start one.
		newSrv := startNew("0.0.0.0:7654")
		if newSrv == nil {
			newSrv = startNew("0.0.0.0:0")
		}
		if newSrv == nil {
			return webRemoteMsg{errStr: "failed to bind web remote server"}
		}
		newSrv.EnablePairing()
		return buildWebRemoteMsg(newSrv, true)
	}
}

// webRemoteDisableCmd disables pairing on the active web remote server without
// closing it (existing session tokens remain valid for their TTL).
func (m *Model) webRemoteDisableCmd() tea.Cmd {
	srv := m.webRemoteSrv
	if srv == nil {
		srv = m.ctrl
	}
	return func() tea.Msg {
		if srv != nil {
			srv.DisablePairing()
		}
		return webRemoteMsg{enabled: false}
	}
}

// buildWebRemoteMsg constructs a webRemoteMsg from a running, pairing-active server.
func buildWebRemoteMsg(srv *control.Server, replacedCtrl bool) webRemoteMsg {
	if srv == nil || !srv.PairingActive() {
		return webRemoteMsg{enabled: false, replacedCtrl: replacedCtrl}
	}
	_, portStr, _ := net.SplitHostPort(srv.Addr())
	port, _ := strconv.Atoi(portStr)
	url := fmt.Sprintf("http://%s:%d/remote", lanIPv4(), port)
	code := srv.PairingCode()
	qr := ""
	if q, err := qrcode.New(url, qrcode.Medium); err == nil {
		qr = q.ToSmallString(false)
	}
	return webRemoteMsg{
		srv: srv, replacedCtrl: replacedCtrl,
		enabled: true, code: code, url: url, qr: qr,
	}
}

// lanIPv4 returns the primary non-loopback IPv4 address for building a
// LAN-reachable URL. Falls back to "127.0.0.1".
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

// handleWebRemoteKey drives the Web Remote screen.
func (m *Model) handleWebRemoteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace":
		m.screen = screenMenu
		m.list.Title = "OpenDeezer"
		m.list.SetItems(m.menuRows())
		m.list.ResetSelected()
		m.status = ""
		return m, nil
	case "ctrl+c", "q":
		m.saveResume()
		m.player.Stop()
		if m.media != nil {
			m.media.Close()
		}
		if m.discord != nil {
			m.discord.Close()
		}
		if m.ctrl != nil {
			m.ctrl.Close()
		}
		if m.webRemoteSrv != nil && m.webRemoteSrv != m.ctrl {
			m.webRemoteSrv.Close()
		}
		if m.advertiser != nil {
			m.advertiser.Close()
		}
		return m, tea.Quit
	case "enter", " ":
		if m.webRemoteActive {
			m.status = "Disabling web remote…"
			return m, m.webRemoteDisableCmd()
		}
		m.loading = true
		m.status = "Starting web remote…"
		return m, m.webRemoteEnableCmd()
	}
	return m, nil
}

// webRemoteView renders the Web Remote screen.
func (m *Model) webRemoteView() string {
	lines := []string{
		accent.Render("📱 Web Remote") + dim.Render("  — control from your phone"),
		"",
	}
	if !m.webRemoteActive {
		lines = append(lines,
			"Web remote is off.",
			"",
			dim.Render("Press enter to enable. Your phone must be on the same Wi-Fi."),
			"",
			dim.Render("enter enable · esc back · q quit"),
		)
	} else {
		lines = append(lines,
			"Scan with your phone (same Wi-Fi), then enter the code.",
			"",
			"Code:  "+accent.Render(m.webRemoteCode),
			"URL:   "+dim.Render(m.webRemoteURL),
			"",
		)
		if m.webRemoteQR != "" {
			for _, line := range strings.Split(strings.TrimRight(m.webRemoteQR, "\n"), "\n") {
				lines = append(lines, line)
			}
			lines = append(lines, "")
		}
		lines = append(lines, dim.Render("enter disable · esc back · q quit"))
	}
	if m.status != "" {
		lines = append(lines, "", statusSty.Render(m.status))
	}
	return padTo(lines, max(1, m.height-footerHeight))
}
