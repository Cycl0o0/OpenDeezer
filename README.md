# OpenDeezer

**An open source reimplementation of Deezer.** Log in with your Deezer ARL,
browse your liked songs, playlists and search, and stream — the track is
downloaded, Blowfish stripe-decrypted, MP3-decoded and played **locally**. Your
ARL never leaves your machine except in the requests it makes to Deezer.

One Go engine does the whole streaming path (login, decrypt, decode, playback);
several native front-ends sit on top of it.

## Clients

| Client | Stack | Status |
|--------|-------|--------|
| **Terminal (TUI)** | Go · Bubble Tea | ✅ |
| **macOS** | SwiftUI · Liquid Glass (macOS 26) | ✅ `gui/macos` |
| **GNOME** | GTK4 · libadwaita | 🚧 `gui/gnome` |
| **KDE** | Qt6 | 🚧 `gui/kde` |

The macOS/GNOME/KDE apps link the engine as a C archive (`corelib`,
`go build -buildmode=c-archive`) and are UI only. The sections below cover the
terminal client; see each `gui/<platform>/README.md` for the GUIs.

By **Cycl0o0**.

## Requirements

- Go 1.24+
- A Deezer **Premium** account
- A working audio output device
- On **Linux**, ALSA dev headers to build (`sudo apt install libasound2-dev`).
  macOS and Windows need nothing extra (no cgo).
- Album art needs a **256-color or truecolor** terminal (rendered as half-blocks).

## Install

Grab a binary from the [Releases](../../releases) page, or build it:

```sh
make build          # -> ./opendeezer   (or: go build -o opendeezer ./cmd/opendeezer)
./opendeezer -save-arl <your-arl>   # writes ~/.config/opendeezer/arl.txt (0600)
./opendeezer
```

Or pass it inline: `DEEZER_ARL=<your-arl> ./opendeezer`.

Your ARL is the `arl` cookie from an authenticated `deezer.com` browser session.
Treat it like a password — it grants access to your account.

## Controls

| Key | Action |
|-----|--------|
| ↑/↓ | move |
| enter | open / play |
| esc / backspace | back |
| space | play / pause |
| n / p | next / previous |
| z | toggle shuffle |
| r | cycle repeat (off → all → one) |
| +/- | volume up / down |
| c | now-playing + album art |
| ? | credits |
| s | stop |
| / | search |
| q | quit |

## How it works

```
ARL ─login (gw-light)→ browse (gw + public REST)
                     → resolve track → encrypted CDN URL
                     → HTTP GET → Blowfish BF_CBC_STRIPE decrypt
                     → MP3 decode (go-mp3) → PCM out (oto)
```

- `internal/deezer` — login, browse, track→URL resolve, and the stripe decryptor.
- `internal/audio` — streaming decrypt + decode + playback.
- `internal/ui` — the Bubble Tea TUI and config.

## The fine print

Personal/educational use, your own Premium account, your own risk. It reaches
Deezer the unofficial way and decrypts your own entitled content locally, which
almost certainly breaks Deezer's terms for third-party apps. Not affiliated with
Deezer. AGPL-3.0.
