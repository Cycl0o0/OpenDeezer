package connect_test

import (
	"testing"
	"time"

	"github.com/Cycl0o0/OpenDeezer/sdk/connect"
)

func TestNewRemoteClient(t *testing.T) {
	rc := connect.NewRemoteClient("127.0.0.1:9999", "", "")
	if rc == nil {
		t.Fatal("NewRemoteClient returned nil")
	}
}

// TestDiscover sends a probe on the loopback and expects the call to return
// within the timeout (possibly with zero devices if nothing is running).
func TestDiscover(t *testing.T) {
	devices, err := connect.Discover(50*time.Millisecond, 0)
	if err != nil {
		t.Fatal(err)
	}
	// No assertion on count — just verify it runs without panicking.
	_ = devices
}

// TestHostStartStop verifies the inbound side: a Host binds a control endpoint
// and starts advertising, then shuts down cleanly.
func TestHostStartStop(t *testing.T) {
	h := connect.NewHost(
		connect.HostConfig{
			// Loopback + ephemeral port keeps the test self-contained and passes
			// the control server's non-loopback security check.
			Control: connect.Config{Addr: "127.0.0.1:0"},
			Name:    "Test Device",
			Client:  "test",
			Version: "0.0.1",
		},
		func() connect.State { return connect.State{State: "stopped"} },
		func() connect.Account { return connect.Account{Name: "Tester"} },
		connect.Commands{},
		nil,
	)
	if err := h.Start(); err != nil {
		t.Fatal("Start:", err)
	}
	defer h.Close()
	if h.Addr() == "" {
		t.Error("Addr should be non-empty after Start")
	}
	if h.Server() == nil {
		t.Error("Server should be accessible")
	}
	t.Log("host control endpoint at", h.Addr())
}
