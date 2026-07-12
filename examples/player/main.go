// player streams, decrypts and plays a small queue of tracks in-process with
// gapless transitions: PrepareStream resolves each track, sdk/player plays it,
// SetOnFinish advances the queue, and Preload hands the next track to the
// engine before the current one ends so the transition has no silence.
//
// Usage: DEEZER_ARL=<your_arl> go run ./examples/player [track_id ...]
//
// Example:
//
//	DEEZER_ARL=$ARL go run ./examples/player 3135556 3135554 3135553
//
// Requires cgo and an audio output device (it will not start on a headless
// machine). Without DEEZER_ARL it prints what it would do and exits.
package main

import (
	"fmt"
	"log"
	"os"
	"time"

	dz "github.com/Cycl0o0/OpenDeezer/v2/sdk/deezer"
	"github.com/Cycl0o0/OpenDeezer/v2/sdk/player"
)

// preloadWindowMS is how close to the end of the current track the next one is
// preloaded. Preloading late keeps bandwidth low; preloading before the final
// buffer drains keeps the transition gapless.
const preloadWindowMS = 15_000

func main() {
	// Playback needs a logged-in session: guard it behind ARL presence so the
	// example still runs (and explains itself) without credentials.
	arl := os.Getenv("DEEZER_ARL")
	if arl == "" {
		fmt.Println("DEEZER_ARL is not set — set it to your Deezer ARL cookie to hear playback.")
		fmt.Println("Get it from a browser session on deezer.com: F12 → Application → Cookies → arl.")
		fmt.Println()
		fmt.Println("  DEEZER_ARL=<your_arl> go run ./examples/player [track_id ...]")
		return
	}

	// The queue: track ids from the command line, or a short demo queue.
	ids := os.Args[1:]
	if len(ids) == 0 {
		ids = []string{"3135556", "3135554", "3135553"}
	}
	for i, raw := range ids {
		if id := dz.TrackIDOf(raw); id != "" {
			ids[i] = id
		} else {
			log.Fatalf("could not extract a track id from %q", raw)
		}
	}

	// Authenticate.
	client := dz.New(arl)
	if err := client.Login(); err != nil {
		log.Fatalf("login: %v", err)
	}
	client.SetQuality(dz.QualityHigh) // prefer MP3 320; falls back if not entitled
	acc := client.Account()
	fmt.Printf("Logged in as %s (%s)\n", acc.Name, acc.Offer)

	// Start the audio engine. This opens the system output device.
	p, err := player.NewPlayer()
	if err != nil {
		log.Fatalf("audio init (an output device is required): %v", err)
	}
	defer p.Close()
	p.SetGapless(true)
	p.SetReplayGain(true)
	p.SetVolume(0.9)

	// resolve fetches a track's metadata and stream plan in one go.
	resolve := func(id string) (*dz.StreamPlan, dz.Track, error) {
		track, err := client.Track(id)
		if err != nil {
			return nil, dz.Track{}, fmt.Errorf("track %s metadata: %w", id, err)
		}
		plan, err := client.PrepareStream(id)
		if err != nil {
			return nil, dz.Track{}, fmt.Errorf("prepare stream %s: %w", id, err)
		}
		return plan, track, nil
	}

	// The engine calls OnFinish from its own goroutine when a track ends
	// naturally — with a preloaded next track it has already swapped to it
	// gaplessly; without one it has stopped. Keep the callback non-blocking
	// and do the network work (resolve/preload) back on the main goroutine.
	finished := make(chan struct{}, 1)
	p.SetOnFinish(func() {
		select {
		case finished <- struct{}{}:
		default:
		}
	})

	// Play the first track.
	idx := 0
	plan, track, err := resolve(ids[idx])
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Playing  %s — %s [%s]\n", track.Name, track.ArtistLine(), dz.FormatLabel(plan.Format))
	if err := p.Play(plan, track.DurationMS); err != nil {
		log.Fatalf("play: %v", err)
	}

	// preloadNext resolves ids[idx+1] and hands it to the engine for a gapless
	// transition. Returns false when there is no next track (or it failed).
	preloaded := false
	preloadNext := func() {
		if preloaded || idx+1 >= len(ids) {
			return
		}
		nextPlan, nextTrack, err := resolve(ids[idx+1])
		if err != nil {
			log.Printf("preload: %v", err)
			return
		}
		p.Preload(nextPlan, nextTrack.DurationMS)
		preloaded = true
		fmt.Printf("Preloaded next: %s — %s\n", nextTrack.Name, nextTrack.ArtistLine())
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-finished:
			idx++
			if idx >= len(ids) {
				fmt.Println("\nQueue finished.")
				return
			}
			preloaded = false
			if p.State() == player.Stopped {
				// No preloaded source was ready, so the engine stopped; start
				// the next track manually. (With a successful preload the
				// engine has already swapped to it gaplessly.)
				plan, track, err = resolve(ids[idx])
				if err != nil {
					log.Fatal(err)
				}
				if err := p.Play(plan, track.DurationMS); err != nil {
					log.Fatalf("play: %v", err)
				}
			} else if track, err = client.Track(ids[idx]); err != nil {
				track = dz.Track{Name: ids[idx]}
			}
			fmt.Printf("\nPlaying  %s — %s [%s]\n", track.Name, track.ArtistLine(), p.Format())
			preloadNext()

		case <-ticker.C:
			if p.State() == player.Errored {
				log.Fatalf("playback error: %s", p.LastError())
			}
			pos, dur := p.PositionMS(), p.DurationMS()
			fmt.Printf("\r  %02d:%02d / %02d:%02d",
				pos/60000, pos/1000%60, dur/60000, dur/1000%60)
			// Preload the next track shortly before this one ends.
			if dur > 0 && dur-pos < preloadWindowMS {
				preloadNext()
			}
		}
	}
}
