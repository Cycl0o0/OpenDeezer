package ui

import (
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/config"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/control"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/discovery"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/i18n"
	tea "github.com/charmbracelet/bubbletea"
)

// normalizePeer turns user input into a base URL + host:port (default port 7654).
func normalizePeer(addr string) (base, hostport string) {
	return config.NormalizePeer(addr)
}

// remoteConnectCmd connects to a peer's control API: verify with /whoami, grab
// initial status. Auth always uses our own Deezer user id (same-account); the
// local token is sent ONLY for a trusted (manually-typed) address — never to a
// discovered device, whose advertisement is unauthenticated and spoofable, so we
// must not leak the shared token to it.
func (m *Model) remoteConnectCmd(addr string, trusted bool) tea.Cmd {
	base, hostport := normalizePeer(addr)
	if base == "" {
		return func() tea.Msg {
			return remoteConnMsg{err: fmt.Errorf("%s", i18n.T("enter a host or host:port"))}
		}
	}
	token := ""
	if trusted {
		token = LoadControl().Token
	}
	account := m.client.UserID()
	return func() tea.Msg {
		rc := control.NewClient(base, token, account)
		who, err := rc.Whoami()
		if err != nil {
			return remoteConnMsg{err: err}
		}
		st, _ := rc.Status() // best-effort initial snapshot
		return remoteConnMsg{
			client: rc, addr: hostport, name: who.Name,
			clientType: who.Client, version: who.Version, state: st,
		}
	}
}

// discoverDevicesCmd scans the LAN for OpenDeezer Connect devices and enriches
// each with what it's currently playing (best-effort, needs same-account auth).
func (m *Model) discoverDevicesCmd() tea.Cmd {
	account := m.client.UserID()
	selfPort := 0
	if m.ctrl != nil {
		if _, port, err := net.SplitHostPort(m.ctrl.Addr()); err == nil {
			selfPort, _ = strconv.Atoi(port)
		}
	}
	return func() tea.Msg {
		devs, _ := discovery.Discover(700*time.Millisecond, selfPort, config.PeerHostPorts()...)
		devs = mergeConfiguredPeers(devs, account)
		peers := make([]peerDevice, 0, len(devs))
		for _, d := range devs {
			np := ""
			// account-only: never send the token to an unverified discovered peer.
			rc := control.NewClient("http://"+d.Addr, "", account)
			if st, err := rc.Status(); err == nil && st.Track != nil {
				np = st.Track.Title
				if st.Track.Artist != "" {
					np += " — " + st.Track.Artist
				}
			}
			peers = append(peers, peerDevice{dev: d, nowPlaying: np})
		}
		return devicesDiscoveredMsg{peers: peers}
	}
}

// mergeConfiguredPeers adds manually-listed peers (config) not found by discovery
// — querying each /whoami for name/type/version. Lets Connect reach peers over
// unicast-only networks (Tailscale/VPN) with no multicast/broadcast.
func mergeConfiguredPeers(devs []discovery.Device, account string) []discovery.Device {
	peers := config.LoadPeers()
	if len(peers) == 0 {
		return devs
	}
	seen := map[string]bool{}
	for _, d := range devs {
		seen[d.Addr] = true
	}
	for _, p := range peers {
		base, hp := config.NormalizePeer(p)
		if base == "" || seen[hp] {
			continue
		}
		seen[hp] = true
		name, client, version := hp, "", ""
		if who, err := control.NewClient(base, "", account).Whoami(); err == nil {
			if who.Name != "" {
				name = who.Name
			}
			client, version = who.Client, who.Version
		}
		devs = append(devs, discovery.Device{Name: name, Addr: hp, Client: client, Version: version})
	}
	return devs
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
		rc := m.remote
		m.remote = nil
		m.remoteState = control.State{}
		m.screen = screenMenu
		m.status = i18n.T("Disconnected from remote")
		if rc != nil {
			_, _ = rc.Stop() // halt the remote device; fire-and-forget
		}
		return m, nil
	case "ctrl+c", "Q":
		m.shutdown() // same cleanup + resume-save as the other quit paths
		return m, tea.Quit
	case " ":
		return m, remoteCmd(rc.PlayPause)
	case "l":
		// Show the connected peer's now-playing lyrics (the peer drives position).
		if m.remoteState.Track == nil {
			return m, nil
		}
		t := remoteTrack(m.remoteState.Track)
		m.toggleScreen(screenLyrics)
		if m.screen == screenLyrics && (m.lyrics == nil || m.lyricsTrack != t.ID) {
			m.lyricsTrack = t.ID
			m.loading = true
			m.status = i18n.T("Loading…")
			return m, m.lyricsCmd(t)
		}
		return m, nil
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
func (m *Model) remoteEntryView(rows int) string {
	lines := []string{
		"📡 " + i18n.T("Remote control") + " — " + i18n.T("drive another OpenDeezer client"),
		"",
		i18n.T("Peer address (host or host:port, default port 7654):"),
		"  " + m.search.View(),
		"",
		i18n.T("The peer must have its control API enabled (OPENDEEZER_CONTROL=:7654),"),
		i18n.T("and be on the same Deezer account (or share a token)."),
		"",
		i18n.T("enter connect · esc cancel"),
	}
	if m.status != "" {
		lines = append(lines, "", m.status)
	}
	return padTo(lines, rows)
}

// remoteCtlView shows the connected peer's playback + remote key hints.
func (m *Model) remoteCtlView(rows int) string {
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
	device := deviceTypeLabel(m.remoteClient)
	if m.remoteVersion != "" {
		device += " · OpenDeezer v" + m.remoteVersion
	}
	lines := []string{
		"📡 " + i18n.Tf("Connected to %s", name) + "  (" + m.remoteAddr + ")",
		i18n.T("Device") + ": " + device,
		"",
		i18n.T("State") + ":  " + i18n.T(state),
		i18n.T("Track") + ":  " + track,
		i18n.T("Time") + ":   " + fmtMS(st.PositionMS) + " / " + fmtMS(st.DurationMS),
		i18n.T("Volume") + ": " + strconv.Itoa(int(st.Volume*100+0.5)) + "%" +
			"   " + i18n.T("Repeat") + ": " + i18n.T(repeat) + "   " + i18n.T("Shuffle") + ": " + boolLabel(st.Shuffle),
		"",
		i18n.T("space play/pause · n next · p prev · s stop · ←/→ seek ±10s · +/- volume"),
		i18n.T("r repeat · z shuffle · esc disconnect"),
	}
	if m.status != "" {
		lines = append(lines, "", m.status)
	}
	return padTo(lines, rows)
}

func boolLabel(b bool) string {
	if b {
		return i18n.T("on")
	}
	return i18n.T("off")
}
