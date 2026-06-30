package control_test

import (
	"testing"

	"github.com/Cycl0o0/OpenDeezer/sdk/control"
	sdkdeezer "github.com/Cycl0o0/OpenDeezer/sdk/deezer"
)

func TestNewClient(t *testing.T) {
	c := control.NewClient("http://127.0.0.1:9999", "", "")
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
}

func TestNewServerStartStop(t *testing.T) {
	dz := sdkdeezer.New("test-arl")
	srv := control.NewServer(
		// 127.0.0.1:0 → loopback, ephemeral port (passes the security check).
		control.Config{Addr: "127.0.0.1:0"},
		func() control.State { return control.State{State: "stopped"} },
		func() control.Account { return control.Account{} },
		control.Commands{},
		dz,
	)
	if srv == nil {
		t.Fatal("NewServer returned nil")
	}
	if err := srv.Start(); err != nil {
		t.Fatal("Start:", err)
	}
	defer srv.Close()

	addr := srv.Addr()
	if addr == "" {
		t.Error("Addr should be non-empty after Start")
	}
	t.Log("control server listening at", addr)
}

func TestNewServerNilDeezer(t *testing.T) {
	// Passing nil for the Deezer client is valid — browse endpoints return 503.
	srv := control.NewServer(
		control.Config{Addr: "127.0.0.1:0"},
		func() control.State { return control.State{State: "stopped"} },
		func() control.Account { return control.Account{} },
		control.Commands{},
		nil,
	)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	srv.Close()
}
