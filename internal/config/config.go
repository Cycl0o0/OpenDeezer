// Package config centralizes OpenDeezer's user configuration (env vars +
// ~/.config/opendeezer files) for the bits shared between the TUI and the GUI
// engine (corelib): the control API and Discord Rich Presence settings.
package config

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// Dir is ~/.config/opendeezer (platform UserConfigDir + "opendeezer").
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "opendeezer"), nil
}

func readFile(name string) string {
	// Primary: the platform config dir (macOS: ~/Library/Application Support).
	if dir, err := Dir(); err == nil {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	// Fallback: ~/.config/opendeezer (Linux-style), so a file placed there still
	// works on macOS where UserConfigDir differs.
	if home, err := os.UserHomeDir(); err == nil {
		if b, err := os.ReadFile(filepath.Join(home, ".config", "opendeezer", name)); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return ""
}

// Control holds the control-API settings (remote control + MCP).
type Control struct {
	Enabled     bool
	Addr        string // host:port; "" -> 127.0.0.1:7654
	Token       string // bearer token ("" = no auth, localhost only)
	SameAccount bool   // require a matching Deezer account when no token (LAN)
}

// LoadControl reads the control-API config from $OPENDEEZER_CONTROL ("1"/addr) +
// $OPENDEEZER_CONTROL_TOKEN, else ~/.config/opendeezer/{control.txt,control-token.txt}.
func LoadControl() Control {
	c := Control{Addr: "127.0.0.1:7654"}
	v := strings.TrimSpace(os.Getenv("OPENDEEZER_CONTROL"))
	if v == "" {
		v = readFile("control.txt")
	}
	switch {
	case v == "":
		return c
	case v == "1" || strings.EqualFold(v, "on") || strings.EqualFold(v, "true"):
		c.Enabled = true
	case v == "0" || strings.EqualFold(v, "off") || strings.EqualFold(v, "false") || strings.EqualFold(v, "no"):
		c.Enabled = false
	default:
		c.Enabled = true
		c.Addr = v // an explicit host:port
	}
	c.Token = strings.TrimSpace(os.Getenv("OPENDEEZER_CONTROL_TOKEN"))
	if c.Token == "" {
		c.Token = readFile("control-token.txt")
	}
	// LAN bind + no token => default to same-account auth.
	if c.Enabled && c.Token == "" && !isLoopbackAddr(c.Addr) {
		c.SameAccount = true
	}
	if v := strings.TrimSpace(os.Getenv("OPENDEEZER_CONTROL_SAMEACCOUNT")); v != "" {
		c.SameAccount = v == "1" || strings.EqualFold(v, "on") || strings.EqualFold(v, "true")
	}
	return c
}

// writeFile writes contents to a file under Dir(), creating the directory if
// needed. Unlike readFile it only ever targets the primary (platform) config
// dir — there's exactly one place a setting should be written to.
func writeFile(name, contents string) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// Write atomically (temp file + rename) so a crash or a concurrent reader in
	// another client never sees a truncated/torn file — an emptied
	// control-token.txt would silently downgrade the LAN control API from bearer
	// auth to same-account auth.
	tmp, err := os.CreateTemp(dir, name+".tmp*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after a successful rename
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(contents); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filepath.Join(dir, name))
}

// SaveControlEnabled persists whether the control API starts automatically, so
// a Settings UI can flip it without editing env vars or config files by hand.
// addr is the bind address to remember while enabled (typically the current
// LoadControl().Addr); pass "" to disable.
func SaveControlEnabled(enabled bool, addr string) error {
	v := ""
	if enabled {
		v = strings.TrimSpace(addr)
	}
	return writeFile("control.txt", v)
}

// SaveControlToken persists the control-API bearer token. "" clears it, which
// falls back to same-account auth on a LAN bind.
func SaveControlToken(token string) error {
	return writeFile("control-token.txt", strings.TrimSpace(token))
}

// IsLoopbackAddr reports whether a host:port binds only the loopback interface.
func IsLoopbackAddr(addr string) bool { return isLoopbackAddr(addr) }

// isLoopbackAddr reports whether a host:port binds only the loopback interface.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	switch host {
	case "", "0.0.0.0", "::":
		return false // wildcard = all interfaces
	case "localhost":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// LoadPeers returns manually-configured Connect peer addresses (host[:port]),
// from $OPENDEEZER_CONNECT_PEERS (comma-separated) and
// ~/.config/opendeezer/connect-peers.txt (one per line). These are merged into
// the device picker alongside LAN discovery, so Connect works over networks that
// carry no multicast/broadcast (e.g. Tailscale/VPN — unicast-only meshes).
func LoadPeers() []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !strings.HasPrefix(s, "#") && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, p := range strings.Split(os.Getenv("OPENDEEZER_CONNECT_PEERS"), ",") {
		add(p)
	}
	for _, line := range strings.Split(readFile("connect-peers.txt"), "\n") {
		add(line)
	}
	return out
}

// NormalizePeer turns user input ("host", "host:port", "http://host:port") into
// a base URL + host:port, defaulting the port to 7654. Returns ("","") if empty.
func NormalizePeer(addr string) (base, hostport string) {
	addr = strings.TrimSpace(addr)
	addr = strings.TrimPrefix(addr, "http://")
	addr = strings.TrimPrefix(addr, "https://")
	addr = strings.TrimRight(addr, "/")
	if addr == "" {
		return "", ""
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		// No port present. Bracket bare IPv6 literals (which themselves contain
		// colons) before appending the default port so the result stays a valid
		// host:port — Tailscale/VPN peers are commonly IPv6.
		if ip := net.ParseIP(addr); ip != nil && ip.To4() == nil {
			addr = "[" + addr + "]"
		}
		addr += ":7654"
	}
	return "http://" + addr, addr
}

// EQ is the persisted equalizer + mono-downmix state. It is loaded and saved by
// the audio engine itself (not per client), so the TUI, every GUI and the
// control API all share one set of settings through ~/.config/opendeezer.
type EQ struct {
	Enabled  bool      `json:"enabled"`
	Mono     bool      `json:"mono"`
	PreampDB float64   `json:"preampDb"`
	GainsDB  []float64 `json:"gainsDb"`
	Preset   string    `json:"preset"`
}

// LoadEQ reads eq.json from the config dir. ok is false when the file is
// missing or unparseable (caller falls back to defaults).
func LoadEQ() (eq EQ, ok bool) {
	s := readFile("eq.json")
	if s == "" {
		return EQ{}, false
	}
	if err := json.Unmarshal([]byte(s), &eq); err != nil {
		return EQ{}, false
	}
	return eq, true
}

// SaveEQ persists the equalizer state to eq.json in the config dir.
func SaveEQ(eq EQ) error {
	b, err := json.Marshal(eq)
	if err != nil {
		return err
	}
	return writeFile("eq.json", string(b))
}

// LoadDiscordAppID returns the Discord application id for Rich Presence, from
// $OPENDEEZER_DISCORD_APP_ID or ~/.config/opendeezer/discord-app-id.txt. Empty
// disables the feature.
func LoadDiscordAppID() string {
	if v := strings.TrimSpace(os.Getenv("OPENDEEZER_DISCORD_APP_ID")); v != "" {
		return v
	}
	return readFile("discord-app-id.txt")
}

// LoadLanguage returns the persisted UI language code (e.g. "fr", "zh"), from
// $OPENDEEZER_LANG or ~/.config/opendeezer/language.txt. An empty result means
// "auto" — the caller should fall back to locale detection. Shared across every
// client so the language chosen in one place (TUI, GUI) applies everywhere.
func LoadLanguage() string {
	if v := strings.TrimSpace(os.Getenv("OPENDEEZER_LANG")); v != "" {
		return v
	}
	return readFile("language.txt")
}

// LanguageSetting returns ONLY the persisted language file (~/.config/opendeezer/
// language.txt), ignoring $OPENDEEZER_LANG. LoadLanguage lets the env var win so a
// forced locale applies everywhere at startup; the in-app Language menu, however,
// edits and displays its own persisted selection, so it must read the file alone —
// otherwise a set OPENDEEZER_LANG would freeze the menu on one entry and desync its
// label from the locale actually applied.
func LanguageSetting() string {
	return readFile("language.txt")
}

// SaveLanguage persists the UI language code. "" clears it (back to auto).
func SaveLanguage(code string) error {
	return writeFile("language.txt", strings.TrimSpace(code))
}
