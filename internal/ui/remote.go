package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Cycl0o0/OpenDeezer/internal/control"
	tea "github.com/charmbracelet/bubbletea"
)

// normalizePeer turns user input ("host", "host:port", "http://host:port") into
// a base URL + host:port, defaulting the port to 7654.
func normalizePeer(addr string) (base, hostport string) {
	addr = strings.TrimSpace(addr)
	addr = strings.TrimPrefix(addr, "http://")
	addr = strings.TrimPrefix(addr, "https://")
	addr = strings.TrimRight(addr, "/")
	if addr == "" {
		return "", ""
	}
	if !strings.Contains(addr, ":") {
		addr += ":7654"
	}
	return "http://" + addr, addr
}

// remoteConnectCmd connects to a peer's control API: verify with /whoami, grab
// initial status. Auth uses the local token (if any) plus our own Deezer user id
// for same-account auth.
func (m *Model) remoteConnectCmd(addr string) tea.Cmd {
	base, hostport := normalizePeer(addr)
	if base == "" {
		return func() tea.Msg { return remoteConnMsg{err: fmt.Errorf("enter a host or host:port")} }
	}
	token := LoadControl().Token
	account := m.client.UserID()
	return func() tea.Msg {
		rc := control.NewClient(base, token, account)
		who, err := rc.Whoami()
		if err != nil {
			return remoteConnMsg{err: err}
		}
		st, _ := rc.Status() // best-effort initial snapshot
		return remoteConnMsg{client: rc, addr: hostport, name: who.Name, state: st}
	}
}

// remotePollCmd fetches the peer's current status.
func remotePollCmd(rc *control.Client) tea.Cmd {
	return func() tea.Msg {
		st, err := rc.Status()
		return remoteStateMsg{state: st, err: err}
	}
}

// remoteCmd runs a peer command and reports the resulting status.
func remoteCmd(call func() (control.State, error)) tea.Cmd {
	return func() tea.Msg {
		st, err := call()
		return remoteStateMsg{state: st, err: err}
	}
}

// handleRemoteKey drives the connected peer from the remote-control screen.
func (m *Model) handleRemoteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rc := m.remote
	if rc == nil {
		m.screen = screenMenu
		return m, nil
	}
	switch msg.String() {
	case "esc", "backspace":
		m.remote = nil
		m.remoteState = control.State{}
		m.screen = screenMenu
		m.status = "Disconnected from remote"
		return m, nil
	case "ctrl+c", "Q":
		m.player.Stop()
		if m.media != nil {
			m.media.Close()
		}
		if m.ctrl != nil {
			m.ctrl.Close()
		}
		return m, tea.Quit
	case " ":
		return m, remoteCmd(rc.PlayPause)
	case "n":
		return m, remoteCmd(rc.Next)
	case "p":
		return m, remoteCmd(rc.Prev)
	case "s":
		return m, remoteCmd(rc.Stop)
	case "r":
		return m, remoteCmd(rc.CycleRepeat)
	case "z":
		return m, remoteCmd(rc.ToggleShuffle)
	case "+", "=":
		v := clamp01(m.remoteState.Volume + 0.1)
		return m, remoteCmd(func() (control.State, error) { return rc.SetVolume(v) })
	case "-", "_":
		v := clamp01(m.remoteState.Volume - 0.1)
		return m, remoteCmd(func() (control.State, error) { return rc.SetVolume(v) })
	case "left":
		ms := m.remoteState.PositionMS - 10000
		if ms < 0 {
			ms = 0
		}
		return m, remoteCmd(func() (control.State, error) { return rc.Seek(ms) })
	case "right":
		ms := m.remoteState.PositionMS + 10000
		return m, remoteCmd(func() (control.State, error) { return rc.Seek(ms) })
	}
	return m, nil
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// remoteEntryView is the connect screen (address input).
func (m *Model) remoteEntryView() string {
	lines := []string{
		"📡 Remote control — drive another OpenDeezer client",
		"",
		"Peer address (host or host:port, default port 7654):",
		"  " + m.search.View(),
		"",
		"The peer must have its control API enabled (OPENDEEZER_CONTROL=:7654),",
		"and be on the same Deezer account (or share a token).",
		"",
		"enter connect · esc cancel",
	}
	if m.status != "" {
		lines = append(lines, "", m.status)
	}
	return padTo(lines, max(1, m.height-footerHeight))
}

// remoteCtlView shows the connected peer's playback + remote key hints.
func (m *Model) remoteCtlView() string {
	st := m.remoteState
	name := m.remoteName
	if name == "" {
		name = m.remoteAddr
	}
	track := "—"
	if st.Track != nil {
		track = st.Track.Title
		if st.Track.Artist != "" {
			track += " — " + st.Track.Artist
		}
	}
	state := st.State
	if state == "" {
		state = "unknown"
	}
	repeat := st.Repeat
	if repeat == "" {
		repeat = "off"
	}
	lines := []string{
		"📡 Remote: " + name + "  (" + m.remoteAddr + ")",
		"",
		"State:  " + state,
		"Track:  " + track,
		"Time:   " + fmtMS(st.PositionMS) + " / " + fmtMS(st.DurationMS),
		"Volume: " + strconv.Itoa(int(st.Volume*100+0.5)) + "%" +
			"   Repeat: " + repeat + "   Shuffle: " + boolLabel(st.Shuffle),
		"",
		"space play/pause · n next · p prev · s stop · ←/→ seek ±10s · +/- volume",
		"r repeat · z shuffle · esc disconnect",
	}
	if m.status != "" {
		lines = append(lines, "", m.status)
	}
	return padTo(lines, max(1, m.height-footerHeight))
}

func boolLabel(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
