// Package sdk is the OpenDeezer public SDK — a curated Go API for third-party
// developers who want to build on the OpenDeezer engine without forking the
// application itself.
//
// # Sub-packages
//
//   - [sdk/deezer]  — Deezer API client (login, browse, stream resolution, decrypt)
//   - [sdk/connect] — OpenDeezer Connect (LAN device discovery, remote playback)
//   - [sdk/control] — control server + client (HTTP/JSON remote-control API)
//   - [sdk/player]  — in-process audio playback (cgo required: miniaudio or oto)
//
// # Install
//
//	go get github.com/Cycl0o0/OpenDeezer
//
// # Quick start
//
//	import (
//	    dz "github.com/Cycl0o0/OpenDeezer/sdk/deezer"
//	)
//
//	client := dz.New(os.Getenv("DEEZER_ARL"))
//	if err := client.Login(); err != nil {
//	    log.Fatal(err)
//	}
//	results, _ := client.Search("Daft Punk")
//	for _, t := range results.Tracks {
//	    fmt.Println(t.Name, "-", t.ArtistLine())
//	}
//
// # ARL authentication
//
// Deezer uses a long-lived cookie called the ARL (Audio Reference Link) for
// authentication. Obtain yours from a browser session (Application > Cookies >
// arl on deezer.com) and pass it to [sdk/deezer.New]. The ARL never leaves
// your machine beyond the HTTPS requests made to Deezer.
package sdk
