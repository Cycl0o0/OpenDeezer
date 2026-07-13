package discovery

import (
	"encoding/json"
	"net"
	"sync"
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

func TestTwoRespondersShareThePort(t *testing.T) {
	// Two instances on one host must both bind UDP :Port (SO_REUSEPORT), so each
	// is discoverable — this is the same-machine GNOME+KDE case.
	r1, err := Advertise(func() Info { return Info{Name: "A", Client: "gnome"} }, 7001)
	if err != nil {
		t.Skipf("cannot bind discovery port: %v", err)
	}
	defer r1.Close()
	r2, err := Advertise(func() Info { return Info{Name: "B", Client: "kde"} }, 7002)
	if err != nil {
		t.Fatalf("second responder failed to bind the shared port: %v", err)
	}
	defer r2.Close()
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

	// The exact probe gets a well-formed reply. (We don't assert the exact
	// identity: with SO_REUSEPORT, a real OpenDeezer instance running on the dev
	// machine may share the port and answer instead — any valid reply proves the
	// responder answers probes.)
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
	if rep.Magic != replyMagic || rep.Port <= 0 || rep.Port > 65535 {
		t.Fatalf("malformed reply: %+v", rep)
	}
}

func TestDiscoverDeduplicates(t *testing.T) {
	r, err := Advertise(func() Info {
		return Info{Name: "Dedup Device", Client: "tui", Version: "1.2.3"}
	}, 7654)
	if err != nil {
		t.Skipf("cannot bind discovery port: %v", err)
	}
	defer r.Close()

	devs, err := Discover(2200*time.Millisecond, 0)
	if err != nil {
		t.Fatal(err)
	}

	seenAddr := make(map[string]bool)
	for _, d := range devs {
		if d.Name == "Dedup Device" {
			if seenAddr[d.Addr] {
				t.Errorf("duplicate device address found for Dedup Device: %s", d.Addr)
			}
			seenAddr[d.Addr] = true
		}
	}
}

func TestDiscoverStaticPeer(t *testing.T) {
	r, err := Advertise(func() Info {
		return Info{Name: "Static Peer Device", Client: "tui", Version: "1.2.3"}
	}, 7659)
	if err != nil {
		t.Skipf("cannot bind discovery port: %v", err)
	}
	defer r.Close()

	devs, err := Discover(500*time.Millisecond, 0, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, d := range devs {
		if d.Name == "Static Peer Device" {
			found = true
			break
		}
	}
	if !found {
		t.Error("failed to discover static peer via unicast probe")
	}
}

func TestIPsChanged(t *testing.T) {
	tests := []struct {
		oldIPs []string
		newIPs []string
		want   bool
	}{
		{[]string{"127.0.0.1"}, []string{"127.0.0.1"}, false},
		{[]string{"127.0.0.1"}, []string{"127.0.0.1", "192.168.1.5"}, true},
		{[]string{"127.0.0.1", "192.168.1.5"}, []string{"127.0.0.1"}, true},
		{[]string{"127.0.0.1", "192.168.1.5"}, []string{"192.168.1.5", "127.0.0.1"}, false},
		{[]string{"127.0.0.1", "192.168.1.5"}, []string{"127.0.0.1", "192.168.1.6"}, true},
		{[]string{}, []string{}, false},
	}

	for i, tc := range tests {
		got := ipsChanged(tc.oldIPs, tc.newIPs)
		if got != tc.want {
			t.Errorf("test %d: ipsChanged(%v, %v) = %v, want %v", i, tc.oldIPs, tc.newIPs, got, tc.want)
		}
	}
}

func TestB20RebindFailureRetried(t *testing.T) {
	origInterval := supervisorInterval
	origGetIPs := getLocalIPsList
	supervisorInterval = 10 * time.Millisecond
	defer func() {
		supervisorInterval = origInterval
		getLocalIPsList = origGetIPs
	}()

	var ips []string
	var getIPsMutex sync.Mutex
	getLocalIPsList = func() []string {
		getIPsMutex.Lock()
		defer getIPsMutex.Unlock()
		return ips
	}

	ips = []string{"127.0.0.1"}

	r, err := Advertise(func() Info { return Info{Name: "Retry Test", Client: "tui"} }, 7654)
	if err != nil {
		t.Skipf("cannot bind discovery port: %v", err)
	}
	defer r.Close()

	_ = r.conn.Close()

	blockConn, err := net.ListenPacket("udp4", ":7655")
	if err != nil {
		t.Fatalf("Failed to block port 7655: %v", err)
	}

	getIPsMutex.Lock()
	ips = []string{"127.0.0.1", "192.168.1.100"}
	getIPsMutex.Unlock()

	time.Sleep(50 * time.Millisecond)

	if blockConn != nil {
		_ = blockConn.Close()
	}

	time.Sleep(100 * time.Millisecond)

	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: Port})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	_, _ = conn.Write([]byte(probeMagic))
	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, maxPacket)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("Supervisor failed to recover and rebind successfully: %v", err)
	}
	var rep reply
	if err := json.Unmarshal(buf[:n], &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Name != "Retry Test" {
		t.Errorf("expected responder name 'Retry Test', got %q", rep.Name)
	}
}
