package ui

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/Cycl0o0/OpenDeezer/v3/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/v3/internal/config"
	"github.com/Cycl0o0/OpenDeezer/v3/internal/control"
	"github.com/Cycl0o0/OpenDeezer/v3/internal/i18n"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	qrcode "github.com/skip2/go-qrcode"
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
	pl := m.player
	ctrlCfg := LoadControl() // carry the configured token/same-account to the new server

	hist := m.hist
	buildCmds := func() control.Commands {
		if send == nil {
			return control.Commands{}
		}
		// Same callback set as the main control server (thread-safety contract
		// documented on buildControlCommands).
		return buildControlCommands(send, client, hist)
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
			// Carry the persisted credentials so token/same-account clients (MCP,
			// other OpenDeezer clients) keep working after the loopback rebind —
			// auth() checks the token branch before the web-remote branch.
			s := control.New(
				control.Config{
					Addr: addr, Token: ctrlCfg.Token,
					SameAccountOnly: ctrlCfg.SameAccount, WebRemote: true,
				},
				statusSnap, acctSnap, buildCmds(), client,
			)
			s.SetVersion(Version)
			s.SetClientInfo("tui", "OpenDeezer TUI")
			s.SetEQ(control.PlayerEQ(func() control.EQController {
				if pl != nil {
					return pl
				}
				return nil
			}, audio.EQPresetNames))
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
				return webRemoteMsg{errStr: i18n.T("failed to bind web remote server")}
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
			return webRemoteMsg{errStr: i18n.T("failed to bind web remote server")}
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
	if m.ctrlEditToken {
		switch msg.String() {
		case "esc":
			m.ctrlEditToken = false
			m.search.Blur()
			m.search.EchoMode = textinput.EchoNormal
			m.status = ""
			return m, nil
		case "enter":
			tok := strings.TrimSpace(m.search.Value())
			m.ctrlEditToken = false
			m.search.Blur()
			m.search.EchoMode = textinput.EchoNormal
			if err := config.SaveControlToken(tok); err != nil {
				m.status = i18n.Tf("Couldn't save token: %s", err.Error())
			} else if tok == "" {
				m.status = i18n.T("Token cleared — restart to apply")
			} else {
				m.status = i18n.T("Token saved — restart to apply")
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "a":
		cfg := LoadControl()
		enable := !cfg.Enabled
		if err := config.SaveControlEnabled(enable, cfg.Addr); err != nil {
			m.status = i18n.Tf("Couldn't save: %s", err.Error())
		} else if enable {
			m.status = i18n.Tf("Control API on (%s) — restart to apply", cfg.Addr)
		} else {
			m.status = i18n.T("Control API off — restart to apply")
		}
		return m, nil
	case "t":
		cfg := LoadControl()
		m.search.SetValue(cfg.Token)
		m.search.EchoMode = textinput.EchoPassword
		m.search.Focus()
		m.ctrlEditToken = true
		m.status = ""
		return m, nil
	case "esc", "backspace":
		m.screen = screenMenu
		m.list.Title = "OpenDeezer"
		m.list.SetItems(m.menuRows())
		m.list.ResetSelected()
		m.status = ""
		return m, nil
	case "ctrl+c", "q":
		m.shutdown()
		return m, tea.Quit
	case "enter", " ":
		if m.webRemoteActive {
			m.status = i18n.T("Disabling web remote…")
			return m, m.webRemoteDisableCmd()
		}
		if m.loading {
			return m, nil // an enable is already in flight — don't leak a second server
		}
		m.loading = true
		m.status = i18n.T("Starting web remote…")
		return m, m.webRemoteEnableCmd()
	}
	return m, nil
}

// webRemoteView renders the Web Remote screen.
func (m *Model) webRemoteView(rows int) string {
	lines := []string{
		accent.Render("📱 "+i18n.T("Web Remote")) + dim.Render("  — "+i18n.T("control from your phone")),
		"",
	}
	if !m.webRemoteActive {
		lines = append(lines,
			i18n.T("Web remote is off."),
			"",
			dim.Render(i18n.T("Press enter to enable. Your phone must be on the same Wi-Fi.")),
			"",
		)
	} else {
		lines = append(lines,
			i18n.T("Scan with your phone (same Wi-Fi), then enter the code."),
			"",
			i18n.T("Code")+":  "+accent.Render(m.webRemoteCode),
			"URL:   "+dim.Render(m.webRemoteURL),
			"",
		)
		if m.webRemoteQR != "" {
			lines = append(lines, strings.Split(strings.TrimRight(m.webRemoteQR, "\n"), "\n")...)
			lines = append(lines, "")
		}
	}

	// Control API: the persisted setting used for the remote-control feature and
	// MCP, separate from (but shareable with) the ad-hoc pairing above.
	cfg := LoadControl()
	apiStatus := i18n.T("off")
	if cfg.Enabled {
		apiStatus = i18n.T("on") + " · " + cfg.Addr
	}
	tokenStatus := i18n.T("not set")
	if cfg.Token != "" {
		tokenStatus = i18n.T("set")
	}
	lines = append(lines,
		dim.Render("Control API"),
		"  "+i18n.T("Status")+": "+apiStatus,
		"  "+i18n.T("Token")+":  "+tokenStatus,
		"",
	)

	if m.ctrlEditToken {
		lines = append(lines,
			i18n.T("Token")+": "+m.search.View(),
			dim.Render(i18n.T("enter save · esc cancel")),
		)
	} else {
		toggle := i18n.T("enter enable")
		if m.webRemoteActive {
			toggle = i18n.T("enter disable")
		}
		lines = append(lines, dim.Render(i18n.Tf("%s · a control API on/off · t set token · esc back · q quit", toggle)))
	}

	if m.status != "" {
		lines = append(lines, "", statusSty.Render(m.status))
	}
	return padTo(lines, rows)
}
