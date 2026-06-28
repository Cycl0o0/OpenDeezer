// Package config centralizes OpenDeezer's user configuration (env vars +
// ~/.config/opendeezer files) for the bits shared between the TUI and the GUI
// engine (corelib): the control API and Discord Rich Presence settings.
package config

import (
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
	dir, err := Dir()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
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
	case v == "0" || strings.EqualFold(v, "off"):
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

// LoadDiscordAppID returns the Discord application id for Rich Presence, from
// $OPENDEEZER_DISCORD_APP_ID or ~/.config/opendeezer/discord-app-id.txt. Empty
// disables the feature.
func LoadDiscordAppID() string {
	if v := strings.TrimSpace(os.Getenv("OPENDEEZER_DISCORD_APP_ID")); v != "" {
		return v
	}
	return readFile("discord-app-id.txt")
}
