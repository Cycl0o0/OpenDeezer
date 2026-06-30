// Package deezer is the OpenDeezer SDK's Deezer API layer: account login via
// ARL, browse (favorites, playlists, albums, search, charts, Flow, artists,
// lyrics, podcasts), library write operations (like/unlike, playlist CRUD),
// stream resolution, and Blowfish BF_CBC_STRIPE decryption.
//
// # Authenticate
//
//	client := deezer.New(os.Getenv("DEEZER_ARL"))
//	if err := client.Login(); err != nil {
//	    log.Fatal(err) // errors.Is(err, deezer.ErrARLExpired) → re-auth needed
//	}
//
// # Browse
//
//	tracks, _ := client.Favorites()
//	results, _ := client.Search("Radiohead")
//	charts, _  := client.Charts("0")   // "0" = global chart
//
// # Download / decrypt a track
//
//	plan, _ := client.PrepareStream(trackID)
//	f, _    := os.Create("track." + strings.ToLower(plan.Format))
//	defer f.Close()
//	deezer.DownloadTrack(plan, f)
//
// # Decrypt an in-memory buffer
//
//	plainBytes, err := deezer.DecryptBytes(trackID, encryptedBytes)
package deezer
