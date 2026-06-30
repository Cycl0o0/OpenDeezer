// remote-server starts an OpenDeezer control server that a phone web remote or
// another OpenDeezer client can drive. It does not play audio locally; it is a
// minimal demonstration of the server API.
//
// Usage: DEEZER_ARL=<your_arl> go run ./examples/remote-server
//
// Once running:
//   - GET  http://localhost:7654/whoami    — identity + auth mode (no auth)
//   - GET  http://localhost:7654/status    — playback snapshot (needs token)
//   - POST http://localhost:7654/playpause — toggle play/pause (needs token)
//
// This example uses bearer-token auth with a hardcoded demo token. In
// production, generate a random token or enable same-account / web-remote
// (pairing) auth via [control.Config].
package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Cycl0o0/OpenDeezer/sdk/control"
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

	// Simulated playback state. Replace with your real player.
	state := control.State{
		State:  "stopped",
		Volume: 1.0,
		Repeat: "off",
	}

	srv := control.NewServer(
		control.Config{
			Addr:  "127.0.0.1:7654",
			Token: "demo-token", // send X-OpenDeezer-Token: demo-token
		},
		func() control.State { return state },
		func() control.Account {
			a := client.Account()
			return control.Account{UserID: a.UserID, Name: a.Name, Offer: a.Offer}
		},
		control.Commands{
			PlayPause: func() {
				if state.State == "playing" {
					state.State = "paused"
				} else {
					state.State = "playing"
				}
				fmt.Println("PlayPause →", state.State)
			},
			Stop: func() {
				state.State = "stopped"
				state.Track = nil
				fmt.Println("Stop")
			},
			SetVolume: func(v float64) {
				state.Volume = v
				fmt.Printf("Volume → %.0f%%\n", v*100)
			},
			PlayTrack: func(id string) {
				state.State = "playing"
				state.Track = &control.Track{
					ID: id, Title: "Track " + id, Artist: "Unknown",
				}
				fmt.Printf("PlayTrack %s\n", id)
			},
		},
		client, // enables GET /search and GET /playlists
	)

	srv.SetVersion("1.0.0-example")
	srv.SetClientInfo("example", "Remote Server Example")

	if err := srv.Start(); err != nil {
		log.Fatalf("start: %v", err)
	}
	defer srv.Close()

	fmt.Printf("\nControl server listening at http://%s\n", srv.Addr())
	fmt.Println("Token: demo-token  (set X-OpenDeezer-Token header)")
	fmt.Println()
	fmt.Println("Try:")
	fmt.Printf("  curl http://%s/whoami\n", srv.Addr())
	fmt.Printf("  curl -H 'X-OpenDeezer-Token: demo-token' http://%s/status\n", srv.Addr())
	fmt.Printf("  curl -X POST -H 'X-OpenDeezer-Token: demo-token' http://%s/playpause\n", srv.Addr())
	fmt.Println()

	// Poll and print state changes every 2 seconds via the control client.
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	remote := control.NewClient("http://"+srv.Addr(), "demo-token", "")
	for range ticker.C {
		st, err := remote.Status()
		if err != nil {
			continue
		}
		track := "(no track)"
		if st.Track != nil {
			track = st.Track.Title + " — " + st.Track.Artist
		}
		fmt.Printf("[poll] state=%s vol=%.0f%% track=%s\n",
			st.State, st.Volume*100, track)
	}
}
