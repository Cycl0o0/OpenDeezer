# OpenDeezer

**An open source reimplementation of Deezer.** Log in with your Deezer ARL,
browse your liked songs, playlists and search, and stream — the track is
downloaded, Blowfish stripe-decrypted, decoded and played **locally** (MP3, or
FLAC on HiFi). Your ARL never leaves your machine except in the requests it
makes to Deezer.

One Go engine does the whole streaming path (login, decrypt, decode, playback);
six native front-ends sit on top of it. By **Cycl0o0**.

## Clients

| Client | Stack | Builds |
|--------|-------|--------|
| **Terminal (TUI)** | Go · Bubble Tea | linux · macOS · windows (amd64/arm64) |
| **macOS** | SwiftUI · Liquid Glass (macOS 26) | universal (Apple Silicon + Intel) `gui/macos` |
| **Linux (unified)** | auto-picks GTK4 or Qt6 by desktop | x86_64 · aarch64 `gui/linux` |
| **GNOME** | GTK4 · libadwaita | x86_64 · aarch64 `gui/gnome` |
| **KDE** | Qt6 Widgets · Breeze | x86_64 · aarch64 `gui/kde` |
| **Windows** | WinUI 3 · C++/WinRT · Fluent | x64 `gui/windows` |

The **unified Linux** client is one `opendeezer` command that auto-selects the
native toolkit for your desktop (Qt/Breeze on KDE-family, GTK4/libadwaita
elsewhere) — LibreOffice-style. The standalone `gui/gnome` / `gui/kde` binaries
are also available if you prefer one toolkit.

Prebuilt binaries for everything are on the [Releases](../../releases) page.

## Features

- **Quality tiers** — Normal (MP3 128), High (MP3 320), **HiFi (FLAC lossless)**;
  HiFi auto-falls-back to MP3 when your account or the track isn't entitled.
- Liked songs, playlists, search; shuffle, repeat, seek, volume.
- **OS media controls + now-playing** — MPRIS on Linux (GNOME/KDE/TUI media keys
  + overlays), Now Playing + media keys on macOS, SMTC on Windows.
- **Background playback** / close-to-tray in the GUIs.
- Shows the **actual output format** that's playing (e.g. "FLAC · lossless").
- Album art (truecolor half-blocks in the TUI; native everywhere else).
- Settings persisted to `~/.config/opendeezer/`; ARL stays local.

## Install

Download a binary from [Releases](../../releases), or build the TUI:

```sh
make build          # -> ./opendeezer   (or: go build -o opendeezer ./cmd/opendeezer)
./opendeezer -save-arl <your-arl>   # writes ~/.config/opendeezer/arl.txt (0600)
./opendeezer
```

Or pass it inline: `DEEZER_ARL=<your-arl> ./opendeezer`. For the GUIs, see each
`gui/<platform>/README.md` for build steps.

Your ARL is the `arl` cookie from an authenticated `deezer.com` browser session.
Treat it like a password — it grants access to your account.

## Requirements

- A Deezer **Premium** account (HiFi tier for FLAC).
- Building from source: **Go 1.24+** and a working audio device.
- **Linux**: ALSA dev headers (`libasound2-dev`); plus the toolkit dev packages
  for the GUIs (GTK4/libadwaita/json-glib, and/or Qt6).
- **macOS GUI**: macOS 26 (Tahoe) + Xcode 26 for the Liquid Glass APIs.
- **Windows GUI**: Windows 10 1809+/11, Visual Studio 2022 + Windows App SDK,
  and MinGW-w64 (Go cgo builds the engine DLL).
- TUI album art needs a 256-color or truecolor terminal.

## TUI controls

| Key | Action | | Key | Action |
|-----|--------|-|-----|--------|
| ↑/↓ | move | | z | toggle shuffle |
| enter | open / play | | r | cycle repeat (off→all→one) |
| esc / ⌫ | back | | +/- | volume |
| space | play / pause | | ←/→ | seek ±10s |
| n / p | next / prev | | h | quality (Normal→High→HiFi) |
| / | search | | c | now-playing + art |
| s | stop | | ? | credits · q quit |

## How it works

```
ARL ─login (gw-light)→ browse (gw + public REST)
                     → resolve track → encrypted CDN URL (MP3 128/320 or FLAC)
                     → HTTP GET → Blowfish BF_CBC_STRIPE decrypt
                     → MP3 (go-mp3) / FLAC (mewkiz) decode → PCM out (oto)
```

- `internal/deezer` — login, browse, track→URL resolve, the stripe decryptor.
- `internal/audio` — decrypt + MP3/FLAC decode + seekable playback.
- `internal/mpris` — Linux MPRIS media controls.
- `internal/ui` — the Bubble Tea TUI.
- `corelib` — the engine exposed as a C ABI (`-buildmode=c-archive` for
  macOS/Linux, `-buildmode=c-shared` DLL for Windows) so the native GUIs link it.

## The fine print

Personal/educational use, your own Premium account, your own risk. It reaches
Deezer the unofficial way and decrypts your own entitled content locally, which
almost certainly breaks Deezer's terms for third-party apps. Not affiliated with
Deezer. AGPL-3.0.
