// Package discovery provides minimal LAN discovery of OpenDeezer instances so a
// client can offer a "play on another device" picker (OpenDeezer Connect). Each
// instance with the control API enabled runs a small UDP responder advertising
// its display name + control port; a client broadcasts a probe and collects
// replies.
//
// Security: the responder only answers the exact probe magic, replies with a
// tiny fixed payload (no amplification), only to private/loopback/link-local
// sources (no internet reflection), and never carries the same-account
// credential (the Deezer user id) — only the display name. Discovery reveals
// "an OpenDeezer named X is at ip:port"; actually controlling it still requires
// passing the control API's auth.
package discovery

import (
	"encoding/json"
	"net"
	"time"
)

const (
	// Port is the UDP port the responder listens on.
	Port = 7655

	probeMagic = "OPENDEEZER_DISCOVER_v1"
	replyMagic = "OPENDEEZER_DEVICE_v1"
	maxPacket  = 512
)

// Device is a discovered OpenDeezer instance.
type Device struct {
	Name    string `json:"name"`    // account display name (not the user id)
	Addr    string `json:"addr"`    // control API host:port
	Client  string `json:"client"`  // client/platform id (tui, macos, …)
	Version string `json:"version"` // OpenDeezer version
}

// Info is the advertised identity (read per request, so re-login is reflected).
type Info struct {
	Name    string
	Client  string
	Version string
}

// reply is the JSON payload (after replyMagic) a responder sends.
type reply struct {
	Magic   string `json:"magic"`
	Name    string `json:"name"`
	Port    int    `json:"port"` // control API port; host is taken from the reply source IP
	Client  string `json:"client"`
	Version string `json:"version"`
}

// Responder advertises this instance until Close.
type Responder struct {
	conn *net.UDPConn
}

// Advertise starts the discovery responder. info supplies the current identity
// (read per request, so re-login is reflected); controlPort is the control API's
// TCP port that controllers should connect to.
func Advertise(info func() Info, controlPort int) (*Responder, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: Port})
	if err != nil {
		return nil, err
	}
	r := &Responder{conn: conn}
	go r.serve(info, controlPort)
	return r, nil
}

func (r *Responder) serve(info func() Info, controlPort int) {
	buf := make([]byte, maxPacket)
	for {
		n, src, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			return // socket closed
		}
		// Only answer the exact probe, and only to LAN sources (no reflection).
		if string(buf[:n]) != probeMagic || !isLAN(src.IP) {
			continue
		}
		in := info()
		b, _ := json.Marshal(reply{
			Magic: replyMagic, Name: in.Name, Port: controlPort,
			Client: in.Client, Version: in.Version,
		})
		_, _ = r.conn.WriteToUDP(b, src)
	}
}

// Close stops the responder.
func (r *Responder) Close() {
	if r.conn != nil {
		_ = r.conn.Close()
	}
}

// Discover broadcasts a probe and collects replies for the given timeout.
func Discover(timeout time.Duration) ([]Device, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	dst := &net.UDPAddr{IP: net.IPv4bcast, Port: Port}
	if _, err := conn.WriteToUDP([]byte(probeMagic), dst); err != nil {
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(timeout))

	seen := map[string]bool{}
	var out []Device
	buf := make([]byte, maxPacket)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			break // deadline
		}
		var rep reply
		// Bound the port to a valid range: a forged reply must not reach itoa with
		// an out-of-range value (which would overflow its fixed buffer).
		if json.Unmarshal(buf[:n], &rep) != nil || rep.Magic != replyMagic || rep.Port <= 0 || rep.Port > 65535 {
			continue
		}
		addr := net.JoinHostPort(src.IP.String(), itoa(rep.Port))
		if seen[addr] {
			continue
		}
		seen[addr] = true
		name := rep.Name
		if name == "" {
			name = addr
		}
		out = append(out, Device{Name: name, Addr: addr, Client: rep.Client, Version: rep.Version})
	}
	return out, nil
}

// isLAN reports whether ip is a private/loopback/link-local address (safe to
// answer; refuses public sources to avoid being a reflection amplifier).
func isLAN(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [6]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
