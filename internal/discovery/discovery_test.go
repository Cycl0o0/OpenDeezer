package discovery

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestIsLAN(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":   true,
		"10.0.0.4":    true,
		"192.168.1.9": true,
		"172.16.0.1":  true,
		"169.254.1.1": true,  // link-local
		"8.8.8.8":     false, // public
		"1.1.1.1":     false,
	}
	for s, want := range cases {
		if got := isLAN(net.ParseIP(s)); got != want {
			t.Errorf("isLAN(%s) = %v, want %v", s, got, want)
		}
	}
}

func TestResponderRepliesToProbeOnly(t *testing.T) {
	r, err := Advertise(func() Info {
		return Info{Name: "Test Device", Client: "tui", Version: "1.2.3"}
	}, 7654)
	if err != nil {
		t.Skipf("cannot bind discovery port (CI sandbox?): %v", err)
	}
	defer r.Close()

	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: Port})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	// A non-probe packet must be ignored (no reply -> read times out).
	_, _ = conn.Write([]byte("garbage"))
	_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buf := make([]byte, maxPacket)
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("responder replied to a non-probe packet")
	}

	// The exact probe gets a reply with our identity.
	_, _ = conn.Write([]byte(probeMagic))
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("no reply to probe: %v", err)
	}
	var rep reply
	if err := json.Unmarshal(buf[:n], &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Magic != replyMagic || rep.Name != "Test Device" || rep.Port != 7654 ||
		rep.Client != "tui" || rep.Version != "1.2.3" {
		t.Fatalf("unexpected reply: %+v", rep)
	}
}
