// download resolves a Deezer track id, decrypts the audio stream, and saves
// it to a local file. Works with MP3 (128/320) and FLAC depending on your
// account tier.
//
// Usage: DEEZER_ARL=<your_arl> go run ./examples/download <track_id_or_url>
//
// Example:
//
//	DEEZER_ARL=$ARL go run ./examples/download 3135556
//	DEEZER_ARL=$ARL go run ./examples/download https://www.deezer.com/track/3135556
package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	dz "github.com/Cycl0o0/OpenDeezer/sdk/deezer"
)

func main() {
	arl := os.Getenv("DEEZER_ARL")
	if arl == "" {
		log.Fatal("DEEZER_ARL environment variable is not set")
	}
	if len(os.Args) < 2 {
		log.Fatal("usage: download <track_id_or_url>")
	}

	trackID := dz.TrackIDOf(os.Args[1])
	if trackID == "" {
		log.Fatalf("could not extract a track id from %q", os.Args[1])
	}

	// Authenticate.
	client := dz.New(arl)
	if err := client.Login(); err != nil {
		log.Fatalf("login: %v", err)
	}
	client.SetQuality(dz.QualityHigh) // prefer MP3 320; falls back if not entitled

	acc := client.Account()
	fmt.Printf("Logged in as %s (%s)\n", acc.Name, acc.Offer)

	// Fetch track metadata so we know the title and format for the filename.
	track, err := client.Track(trackID)
	if err != nil {
		log.Fatalf("track metadata: %v", err)
	}
	fmt.Printf("Track: %s — %s\n", track.Name, track.ArtistLine())

	// Resolve the CDN URL and decryption info.
	plan, err := client.PrepareStream(trackID)
	if err != nil {
		log.Fatalf("prepare stream: %v", err)
	}
	fmt.Printf("Format: %s\n", dz.FormatLabel(plan.Format))

	// Build a filename from the track title and format.
	ext := "mp3"
	if strings.Contains(strings.ToUpper(plan.Format), "FLAC") {
		ext = "flac"
	}
	safe := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, track.Name+" - "+track.ArtistLine())
	filename := safe + "." + ext

	f, err := os.Create(filename)
	if err != nil {
		log.Fatalf("create %s: %v", filename, err)
	}
	defer f.Close()

	fmt.Printf("Downloading to %s ...\n", filename)

	// DownloadTrack fetches, decrypts (BF_CBC_STRIPE) and writes the audio.
	// This is the whole decode/download flow in one call.
	if err := dz.DownloadTrack(plan, f); err != nil {
		log.Fatalf("download: %v", err)
	}

	info, _ := f.Stat()
	fmt.Printf("Done. Wrote %d KB\n", info.Size()/1024)
}
