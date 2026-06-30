// Package connect implements OpenDeezer Connect: LAN discovery and remote
// control of OpenDeezer devices, in both directions.
//
// OpenDeezer Connect is symmetric — this package exposes both sides:
//
//   - out — discover and control other devices: [Discover] + [RemoteClient]
//   - in  — be discoverable and controllable:   [Host] (and the lower-level
//     [Advertise] if you manage your own control server)
//
// Transport: IPv4 UDP multicast (group 239.255.42.99, port 7655) with a
// directed-broadcast fallback so it works on networks that filter multicast.
// Only LAN/loopback sources are answered; no credentials are exchanged during
// discovery.
//
// # Out: discover devices
//
//	devices, err := connect.Discover(2*time.Second, 0)
//	for _, d := range devices {
//	    fmt.Printf("%s at %s (client: %s)\n", d.Name, d.Addr, d.Client)
//	}
//
// # Out: drive a device
//
//	rc := connect.NewRemoteClient(devices[0].Addr, token, "")
//	st, _ := rc.PlayPause()
//	fmt.Println("state:", st.State)
//
// # In: be a controllable device
//
// [Host] ties a control endpoint together with LAN advertising, so this
// process becomes discoverable and accepts the same commands a [RemoteClient]
// sends:
//
//	host := connect.NewHost(
//	    connect.HostConfig{
//	        Control: connect.Config{Addr: ":7654", SameAccountOnly: true},
//	        Name:    "My Player", Client: "myapp", Version: "1.0",
//	    },
//	    func() connect.State { return snapshot() },
//	    func() connect.Account { return connect.Account{UserID: uid, Name: name} },
//	    connect.Commands{PlayPause: player.TogglePause, Stop: player.Stop},
//	    dzClient, // browse routes; nil to disable
//	)
//	host.Start()
//	defer host.Close()
//
// # In: advertise only (custom control server)
//
//	resp, _ := connect.Advertise(func() connect.AdvertiseInfo {
//	    return connect.AdvertiseInfo{Name: "My Player", Client: "myapp", Version: "1.0"}
//	}, controlPort)
//	defer resp.Close()
package connect
