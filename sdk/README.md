# OpenDeezer SDK

The OpenDeezer SDK is a public Go library that lets third-party developers
build on the OpenDeezer engine: the Deezer API client, track decode/decrypt
and streaming, LAN device discovery (OpenDeezer Connect), and the remote
control API.

```
go get github.com/Cycl0o0/OpenDeezer
```

Import paths:

| Package | Purpose |
|---|---|
| `github.com/Cycl0o0/OpenDeezer/sdk/deezer` | Deezer API client + decode |
| `github.com/Cycl0o0/OpenDeezer/sdk/connect` | OpenDeezer Connect (LAN discovery + remote) |
| `github.com/Cycl0o0/OpenDeezer/sdk/control` | Control server + client (HTTP/JSON API) |
| `github.com/Cycl0o0/OpenDeezer/sdk/player` | In-process audio playback (cgo) |

Go docs: https://pkg.go.dev/github.com/Cycl0o0/OpenDeezer/sdk

### Control works both ways (in + out)

Everything that controls a client is available in **both directions** — you can
drive another device (out) and be driven by one (in):

| Capability | Out (control another) | In (be controlled) |
|---|---|---|
| Remote control API | `control.Client` | `control.Server` |
| OpenDeezer Connect | `connect.Discover` + `connect.RemoteClient` | `connect.Host` (+ `connect.Advertise`) |

Both sides cover the same transport command set: play/pause, next, prev, stop,
restart, seek, volume, repeat, shuffle, play-track, play-playlist.

---

## Authentication (ARL)

Deezer uses a long-lived cookie called the **ARL** (Audio Reference Link) for
authentication. Get yours from a browser session: open deezer.com, press F12,
go to Application → Cookies → `arl`.

The ARL never leaves your machine beyond the HTTPS requests made to deezer.com
and media.deezer.com.

```go
import dz "github.com/Cycl0o0/OpenDeezer/sdk/deezer"

client := dz.New(os.Getenv("DEEZER_ARL"))
if err := client.Login(); err != nil {
    // errors.Is(err, dz.ErrARLExpired) → ARL needs refreshing
    log.Fatal(err)
}
acc := client.Account()
fmt.Printf("Hello %s (%s)\n", acc.Name, acc.Offer)
```

---

## Browse

```go
// Search
results, _ := client.Search("Radiohead")
for _, t := range results.Tracks {
    fmt.Println(t.Name, "—", t.ArtistLine())
}

// Charts (global top 50)
charts, _ := client.Charts("0")

// Favorites / playlists (requires login)
tracks, _ := client.Favorites()
playlists, _ := client.Playlists()

// Artist profile
page, _ := client.ArtistProfile("27")   // Radiohead
fmt.Println(page.Artist.Name, page.Artist.NbFans, "fans")

// Lyrics
lyr, _ := client.Lyrics(trackID)
if lyr.IsSynced() {
    for _, line := range lyr.Synced {
        fmt.Printf("%d ms  %s\n", line.TimeMS, line.Text)
    }
}
```

---

## Download and decrypt a track

`PrepareStream` resolves the CDN URL and decryption key. `DownloadTrack` then
fetches, decrypts (BF_CBC_STRIPE), and writes the audio bytes to any
`io.Writer`:

```go
import (
    "os"
    dz "github.com/Cycl0o0/OpenDeezer/sdk/deezer"
)

client.SetQuality(dz.QualityHigh) // prefer MP3 320; falls back if not entitled

plan, err := client.PrepareStream(trackID)
if err != nil {
    log.Fatal(err)
}
fmt.Println("Format:", dz.FormatLabel(plan.Format)) // "MP3 · 320 kbps"

f, _ := os.Create("track.mp3")
defer f.Close()
if err := dz.DownloadTrack(plan, f); err != nil {
    log.Fatal(err)
}
```

Quality levels:

| Constant | Format | Requires |
|---|---|---|
| `dz.QualityNormal` | MP3 128 kbps | Any account |
| `dz.QualityHigh` | MP3 320 kbps | Premium |
| `dz.QualityLossless` | FLAC | HiFi |

Deezer falls back to the highest quality the account is entitled to
automatically.

### Decrypt an in-memory buffer

```go
plain, err := dz.DecryptBytes(trackID, encryptedBytes)
```

---

## OpenDeezer Connect (LAN discovery + remote)

OpenDeezer Connect is **symmetric** — the SDK exposes both directions:

| Direction | What | API |
|---|---|---|
| **out** | discover + control other devices | `connect.Discover`, `connect.RemoteClient` |
| **in** | be discoverable + controllable | `connect.Host` (or `connect.Advertise` for the discovery half alone) |

### Out — discover and drive a device

```go
import "github.com/Cycl0o0/OpenDeezer/sdk/connect"

// Find devices (2-second probe window).
devices, _ := connect.Discover(2*time.Second, 0)
for _, d := range devices {
    fmt.Printf("%s at %s\n", d.Name, d.Addr)
}

// Drive the first device (same-account auth — no token needed).
rc := connect.NewRemoteClient(devices[0].Addr, "", myUserID)
st, _ := rc.PlayPause()
fmt.Println("state:", st.State)
```

### In — be a controllable device

`connect.Host` ties a control endpoint together with LAN advertising. The
device accepts the exact command set a `RemoteClient` sends:

```go
host := connect.NewHost(
    connect.HostConfig{
        Control: connect.Config{Addr: ":7654", SameAccountOnly: true},
        Name:    "My Player", Client: "myapp", Version: "1.0",
    },
    func() connect.State { return currentState() },
    func() connect.Account {
        a := client.Account()
        return connect.Account{UserID: a.UserID, Name: a.Name, Offer: a.Offer}
    },
    connect.Commands{
        PlayPause: player.TogglePause,
        Stop:      player.Stop,
        SetVolume: player.SetVolume,
        PlayTrack: playByID,
    },
    client, // browse routes; nil to disable
)
host.Start()
defer host.Close()
// host.Server().EnablePairing() to also accept the phone web remote.
```

If you already run your own control server, advertise the discovery half alone:

```go
resp, _ := connect.Advertise(func() connect.AdvertiseInfo {
    return connect.AdvertiseInfo{Name: "My Player", Client: "myapp", Version: "1.0"}
}, controlPort)
defer resp.Close()
```

### Auth modes for RemoteClient

Check `Whoami.Auth` on the target device to know which credential to supply:

| Auth | What to pass |
|---|---|
| `"token"` | `token="<bearer-token>"` |
| `"account"` | `accountID="<your Deezer user id>"` |
| `"session"` | Use `control.NewClient` and pair via GET /remote |
| `"none"` | Empty strings |

---

## Remote control (server = in, client = out)

The control API is also symmetric: `control.Server` hosts a controllable
endpoint (in), `control.Client` drives one (out).

### Control server (in)

Host a controllable endpoint that phones, AI agents, or other OpenDeezer
clients can drive.

```go
import "github.com/Cycl0o0/OpenDeezer/sdk/control"

srv := control.NewServer(
    control.Config{
        Addr:  ":7654",
        Token: "my-secret-token",
    },
    func() control.State { return currentState() },
    func() control.Account {
        a := client.Account()
        return control.Account{UserID: a.UserID, Name: a.Name, Offer: a.Offer}
    },
    control.Commands{
        PlayPause: player.TogglePause,
        Next:      queue.Next,
        Stop:      player.Stop,
        Seek:      player.SeekMS,
        SetVolume: player.SetVolume,
    },
    client, // for GET /search and GET /playlists; nil to disable
)
srv.SetVersion("1.0")
srv.Start()
defer srv.Close()
```

### Phone web remote

Enable pairing to let a phone control playback via a browser:

```go
srv := control.NewServer(
    control.Config{Addr: ":7654", WebRemote: true},
    ...
)
srv.Start()
code := srv.EnablePairing() // display this 6-digit code to the user
fmt.Printf("Open http://192.168.1.X:7654/remote — code: %s\n", code)
```

### Control client (out)

```go
c := control.NewClient("http://192.168.1.5:7654", "my-secret-token", "")
st, _ := c.Status()
fmt.Println(st.State, st.Track.Title)
c.SetVolume(0.8)
c.SeekMS(30000)
c.PlayTrack("3135556")
```

---

## In-process playback (cgo)

The `sdk/player` package wraps the audio engine (miniaudio/malgo or oto,
selected by build tag). It requires cgo. Omit this import if you only need
API access, search, or download/decrypt.

```go
import (
    "github.com/Cycl0o0/OpenDeezer/sdk/player"
    dz "github.com/Cycl0o0/OpenDeezer/sdk/deezer"
)

p, _ := player.NewPlayer()
defer p.Close()

p.SetReplayGain(true)
p.SetVolume(0.9)

// Play a track.
plan, _ := client.PrepareStream(trackID)
p.Play(plan, track.DurationMS)

// Advance queue when track ends.
p.SetOnFinish(func() { /* load next */ })

// Gapless: preload the next track before this one ends.
nextPlan, _ := client.PrepareStream(nextTrackID)
p.Preload(nextPlan, nextTrack.DurationMS)
```

---

## Examples

Runnable examples are in [`examples/`](../examples/):

| Directory | Direction | What it shows |
|---|---|---|
| `examples/search` | — | Login + search + print results |
| `examples/download` | — | Login → PrepareStream → DownloadTrack → file |
| `examples/connect` | out | Discover LAN devices + send PlayPause |
| `examples/host` | in | Be a discoverable, controllable Connect device |
| `examples/remote-server` | in | Host a control server + poll it via the client |

Run any example with:

```sh
DEEZER_ARL=<your_arl> go run ./examples/search "Daft Punk"
DEEZER_ARL=<your_arl> go run ./examples/download 3135556
DEEZER_ARL=<your_arl> go run ./examples/connect
DEEZER_ARL=<your_arl> go run ./examples/host
DEEZER_ARL=<your_arl> go run ./examples/remote-server
```
