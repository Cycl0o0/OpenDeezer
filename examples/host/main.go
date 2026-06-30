// host is the inbound side of OpenDeezer Connect: it makes this process a
// device that other OpenDeezer clients can discover on the LAN and control.
// It is the mirror of examples/connect (which discovers and drives a device).
//
// Usage: DEEZER_ARL=<your_arl> go run ./examples/host
//
// While running, another machine can find this device with
// connect.Discover(...) and drive it with a connect.RemoteClient. This example
// uses same-account auth, so a controller logged into the same Deezer account
// connects with no token.
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Cycl0o0/OpenDeezer/sdk/connect"
	dz "github.com/Cycl0o0/OpenDeezer/sdk/deezer"
)

func main() {
	arl := os.Getenv("DEEZER_ARL")
	if arl == "" {
		log.Fatal("DEEZER_ARL environment variable is not set")
	}

	client := dz.New(arl)
	if err := client.Login(); err != nil {
		log.Fatalf("login: %v", err)
	}
	acc := client.Account()
	fmt.Printf("Logged in as %s (%s)\n", acc.Name, acc.Offer)

	// Simulated playback state. Wire these to your real player.
	state := connect.State{State: "stopped", Volume: 1.0, Repeat: "off"}

	host := connect.NewHost(
		connect.HostConfig{
			// Bind a LAN-reachable address so other devices can connect.
			// Same-account auth: a controller proves it is the same Deezer
			// account; no shared token needed.
			Control: connect.Config{Addr: ":7654", SameAccountOnly: true},
			Name:    acc.Name, // overridden live by the account snapshot below
			Client:  "example",
			Version: "1.0.0",
		},
		func() connect.State { return state },
		func() connect.Account {
			a := client.Account()
			return connect.Account{UserID: a.UserID, Name: a.Name, Offer: a.Offer}
		},
		connect.Commands{
			PlayPause: func() {
				if state.State == "playing" {
					state.State = "paused"
				} else {
					state.State = "playing"
				}
				fmt.Println("controller: PlayPause →", state.State)
			},
			Stop:      func() { state.State = "stopped"; fmt.Println("controller: Stop") },
			SetVolume: func(v float64) { state.Volume = v; fmt.Printf("controller: Volume %.0f%%\n", v*100) },
			PlayTrack: func(id string) {
				state.State = "playing"
				state.Track = &connect.Track{ID: id, Title: "Track " + id}
				fmt.Println("controller: PlayTrack", id)
			},
		},
		client, // serves browse routes to controllers
	)

	if err := host.Start(); err != nil {
		log.Fatalf("host start: %v", err)
	}
	defer host.Close()

	fmt.Printf("\nOpenDeezer Connect host advertising on the LAN.\n")
	fmt.Printf("Control endpoint: %s\n", host.Addr())
	fmt.Println("Other devices can now discover and control this one.")
	fmt.Println("Press Ctrl-C to stop.")

	// Block until interrupted.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	fmt.Println("\nShutting down.")
}
