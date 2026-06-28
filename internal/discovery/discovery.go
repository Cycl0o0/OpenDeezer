// Package discovery provides LAN discovery of OpenDeezer instances so a client
// can offer a "play on another device" picker (OpenDeezer Connect). Each
// instance with the control API enabled runs a small UDP responder advertising
// its display name + control port; a client multicasts a probe and collects
// replies.
//
// Transport: IPv4 UDP multicast (group 239.255.42.99, port 7655) with a limited
// broadcast as a fallback for networks that filter multicast. The responder
// binds with SO_REUSEADDR/SO_REUSEPORT so several instances on ONE machine can
// all listen + answer.
//
// Security: the responder answers only the exact probe magic, only to
// private/loopback/link-local sources (no internet reflection), with a tiny
// fixed payload (no amplification), and never carries the same-account
// credential (the Deezer user id) — only the display name + client + version.
package discovery

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/net/ipv4"
)

const (
	// Port is the UDP port the responder listens on.
	Port = 7655

	probeMagic = "OPENDEEZER_DISCOVER_v1"
	replyMagic = "OPENDEEZER_DEVICE_v1"
	maxPacket  = 512
)

// groupIP is the admin-scoped IPv4 multicast group used for probes.
var groupIP = net.IPv4(239, 255, 42, 99)

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

// Advertise starts the discovery responder on UDP :Port (reuse-port so multiple
// instances on one host coexist) and joins the multicast group on every capable
// interface. info supplies the current identity; controlPort is the control API
// TCP port controllers should connect to.
func Advertise(info func() Info, controlPort int) (*Responder, error) {
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error { return setReusePort(c) }}
	pc, err := lc.ListenPacket(context.Background(), "udp4", ":"+strconv.Itoa(Port))
	if err != nil {
		return nil, err
	}
	conn := pc.(*net.UDPConn)
	p := ipv4.NewPacketConn(conn)
	for _, ifi := range multicastInterfaces() {
		_ = p.JoinGroup(&ifi, &net.UDPAddr{IP: groupIP})
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
		_, _ = r.conn.WriteToUDP(b, src) // unicast reply to the prober
	}
}

// Close stops the responder.
func (r *Responder) Close() {
	if r.conn != nil {
		_ = r.conn.Close()
	}
}

// Discover multicasts a probe (with a broadcast fallback) and collects replies
// for the given timeout. selfPort is this instance's own control port (0 if
// none): replies from a local address with that port are our own responder and
// are filtered out, so we never list ourselves. Other instances on the same host
// use a different control port and are kept.
func Discover(timeout time.Duration, selfPort int) ([]Device, error) {
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error { return setBroadcast(c) }}
	pc, err := lc.ListenPacket(context.Background(), "udp4", ":0") // ephemeral
	if err != nil {
		return nil, err
	}
	conn := pc.(*net.UDPConn)
	defer func() { _ = conn.Close() }()

	probe := []byte(probeMagic)
	group := &net.UDPAddr{IP: groupIP, Port: Port}
	p := ipv4.NewPacketConn(conn)
	_ = p.SetMulticastTTL(4)

	// Multicast the probe out of each capable interface.
	ifaces := multicastInterfaces()
	for i := range ifaces {
		if p.SetMulticastInterface(&ifaces[i]) == nil {
			_, _ = conn.WriteToUDP(probe, group)
		}
	}
	if len(ifaces) == 0 {
		_, _ = conn.WriteToUDP(probe, group)
	}
	// Fallback: limited broadcast (helps where multicast is filtered).
	_, _ = conn.WriteToUDP(probe, &net.UDPAddr{IP: net.IPv4bcast, Port: Port})

	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	locals := localIPs()
	seen := map[string]bool{}
	var out []Device
	buf := make([]byte, maxPacket)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			break // deadline
		}
		var rep reply
		// Bound the port so a forged reply can't produce a bad address.
		if json.Unmarshal(buf[:n], &rep) != nil || rep.Magic != replyMagic || rep.Port <= 0 || rep.Port > 65535 {
			continue
		}
		// Skip our own responder (same control port on one of our local addresses).
		if selfPort != 0 && rep.Port == selfPort && (src.IP.IsLoopback() || locals[src.IP.String()]) {
			continue
		}
		addr := net.JoinHostPort(src.IP.String(), strconv.Itoa(rep.Port))
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

// localIPs is the set of this machine's interface IPs (for self-filtering).
func localIPs() map[string]bool {
	m := map[string]bool{}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return m
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok {
			m[ipnet.IP.String()] = true
		}
	}
	return m
}

// multicastInterfaces returns up, multicast-capable interfaces.
func multicastInterfaces() []net.Interface {
	all, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.Interface
	for _, ifi := range all {
		if ifi.Flags&net.FlagUp != 0 && ifi.Flags&net.FlagMulticast != 0 {
			out = append(out, ifi)
		}
	}
	return out
}

// isLAN reports whether ip is a private/loopback/link-local address (safe to
// answer; refuses public sources to avoid being a reflection amplifier).
func isLAN(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}
