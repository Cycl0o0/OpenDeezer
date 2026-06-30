// search demonstrates authenticating with an ARL and running a query.
// Usage: DEEZER_ARL=<your_arl> go run ./examples/search <query>
package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	dz "github.com/Cycl0o0/OpenDeezer/sdk/deezer"
)

func main() {
	arl := os.Getenv("DEEZER_ARL")
	if arl == "" {
		log.Fatal("DEEZER_ARL environment variable is not set")
	}
	query := strings.Join(os.Args[1:], " ")
	if query == "" {
		query = "Daft Punk"
	}

	client := dz.New(arl)
	if err := client.Login(); err != nil {
		log.Fatalf("login: %v", err)
	}

	acc := client.Account()
	fmt.Printf("Logged in as %s (%s)\n\n", acc.Name, acc.Offer)

	start := time.Now()
	results, err := client.Search(query)
	if err != nil {
		log.Fatalf("search %q: %v", query, err)
	}
	fmt.Printf("Search %q — %v\n", query, time.Since(start).Round(time.Millisecond))

	fmt.Printf("\nTracks (%d)\n", len(results.Tracks))
	for i, t := range results.Tracks {
		if i >= 5 {
			break
		}
		sec := t.DurationMS / 1000
		fmt.Printf("  %s — %s [%s] (%d:%02d)\n",
			t.Name, t.ArtistLine(), t.AlbumName, sec/60, sec%60)
	}

	fmt.Printf("\nAlbums (%d)\n", len(results.Albums))
	for i, a := range results.Albums {
		if i >= 3 {
			break
		}
		artists := make([]string, len(a.Artists))
		for j, ar := range a.Artists {
			artists[j] = ar.Name
		}
		fmt.Printf("  %s — %s\n", a.Name, strings.Join(artists, ", "))
	}

	fmt.Printf("\nArtists (%d)\n", len(results.Artists))
	for i, a := range results.Artists {
		if i >= 3 {
			break
		}
		fmt.Printf("  %s (%.1fk fans)\n", a.Name, float64(a.NbFans)/1000)
	}
}
