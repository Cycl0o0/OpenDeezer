// connect discovers OpenDeezer devices on the local network and sends a
// play/pause command to the first one found.
//
// Usage: DEEZER_ARL=<your_arl> go run ./examples/connect
//
// The connecting device must share the same Deezer account (same-account auth)
// or you must supply a bearer token. This example uses same-account auth.
package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Cycl0o0/OpenDeezer/sdk/connect"
	dz "github.com/Cycl0o0/OpenDeezer/sdk/deezer"
)

func main() {
	arl := os.Getenv("DEEZER_ARL")
	if arl == "" {
		log.Fatal("DEEZER_ARL environment variable is not set")
	}

	// Login to get our own user id (needed for same-account auth).
	client := dz.New(arl)
	if err := client.Login(); err != nil {
		log.Fatalf("login: %v", err)
	}
	myID := client.UserID()
	fmt.Printf("My Deezer user id: %s\n", myID)

	// Discover OpenDeezer instances on the LAN.
	fmt.Println("Discovering devices (2 s)...")
	devices, err := connect.Discover(2*time.Second, 0)
	if err != nil {
		log.Fatalf("discover: %v", err)
	}
	if len(devices) == 0 {
		fmt.Println("No devices found. Make sure another OpenDeezer instance is running with the control API enabled.")
		return
	}

	fmt.Printf("Found %d device(s):\n", len(devices))
	for _, d := range devices {
		fmt.Printf("  %s at %s (%s %s)\n", d.Name, d.Addr, d.Client, d.Version)
	}

	// Connect to the first device using same-account auth (no token needed).
	target := devices[0]
	fmt.Printf("\nConnecting to %s at %s\n", target.Name, target.Addr)
	rc := connect.NewRemoteClient(target.Addr, "", myID)

	// Check the device's identity and auth mode.
	who, err := rc.Whoami()
	if err != nil {
		log.Fatalf("whoami: %v", err)
	}
	fmt.Printf("Device: %s (%s), auth=%s\n", who.Name, who.Offer, who.Auth)

	// Fetch the current playback state.
	st, err := rc.Status()
	if err != nil {
		log.Fatalf("status: %v", err)
	}
	fmt.Printf("State: %s", st.State)
	if st.Track != nil {
		fmt.Printf(", playing: %s — %s", st.Track.Title, st.Track.Artist)
	}
	fmt.Println()

	// Toggle play/pause.
	fmt.Println("Sending PlayPause...")
	st, err = rc.PlayPause()
	if err != nil {
		log.Fatalf("playpause: %v", err)
	}
	fmt.Printf("New state: %s\n", st.State)
}
