// Package control exposes the OpenDeezer remote-control API: a small
// HTTP/JSON server that a controller (another OpenDeezer client, an MCP agent,
// a phone web remote) can drive, and a matching client that talks to one.
//
// # Host a control server
//
//	srv := control.NewServer(
//	    control.Config{
//	        Addr:  ":7654",      // LAN-accessible
//	        Token: "s3cr3t",     // bearer-token auth
//	    },
//	    func() control.State { return snapshot() },
//	    func() control.Account { return control.Account{UserID: uid, Name: name} },
//	    control.Commands{
//	        PlayPause: player.TogglePause,
//	        Next:      queue.Next,
//	        Prev:      queue.Prev,
//	        Stop:      player.Stop,
//	        SetVolume: player.SetVolume,
//	        Seek:      player.SeekMS,
//	    },
//	    dzClient, // pass nil to disable browse (/search, /playlists)
//	)
//	srv.SetVersion("1.0")
//	if err := srv.Start(); err != nil { log.Fatal(err) }
//	defer srv.Close()
//
// # Drive a remote server
//
//	client := control.NewClient("http://192.168.1.5:7654", "s3cr3t", "")
//	st, _ := client.Status()
//	fmt.Println(st.State, st.Track.Title)
//
// # Phone web remote
//
// When [Config.WebRemote] is true, GET /remote serves a mobile-friendly SPA.
// Pair a phone by calling [Server.EnablePairing] to get a 6-digit code, then
// entering it in the SPA. Pairing issues a session token stored in
// localStorage; no cookie is used (CSRF-safe).
//
// # Auth modes
//
//   - token     — bearer token in X-OpenDeezer-Token; set [Config.Token]
//   - account   — controller proves it is the same Deezer account;
//                 set [Config.SameAccountOnly] = true
//   - session   — phone web remote; set [Config.WebRemote] = true
//   - none      — open (safe only on localhost)
package control
