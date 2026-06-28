# OpenDeezer

**An open source reimplementation of Deezer.** Log in once (the GUIs sign you in
through an embedded Deezer web view — no ARL hunting), then browse your liked
songs, playlists, charts, artists and search, and stream — each track is
streamed, Blowfish stripe-decrypted, decoded and played **locally**, in memory
(MP3, or FLAC on HiFi). Your ARL never leaves your machine except in the requests
it makes to Deezer.

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

**Browse & discover**
- Liked songs, your playlists, and full **search** — tracks, artists, albums, playlists.
- **Charts** — global top tracks, albums, artists and playlists.
- **Artist pages** — top tracks, discography and related artists.
- **Synced lyrics** — karaoke-style, line-by-line; plain-text fallback.
- **Deezer Flow** — your personalized, endless track stream.
- **Podcasts** — search shows, browse episodes, play.

**Library editing**
- **Like / unlike** tracks; **add to playlist**; **create / rename / delete** playlists.

**Playback**
- **Quality tiers** — Normal (MP3 128), High (MP3 320), **HiFi (FLAC lossless)**;
  HiFi auto-falls-back to MP3 when your account or the track isn't entitled.
- **Gapless** transitions, **crossfade**, **ReplayGain** loudness normalization.
- **Output-device selection** (powered by the malgo/miniaudio backend).
- Shuffle, repeat (off/all/one), seek, volume; **resume** the last track on launch.
- Shows the **actual output format** that's playing (e.g. "FLAC · lossless").
- **OS media controls + now-playing** — MPRIS on Linux (GNOME/KDE/TUI media keys
  + overlays), Now Playing + media keys on macOS, SMTC on Windows.

**Accounts & UX**
- **One-click login** — sign in via the embedded Deezer web view; the ARL is
  captured automatically (manual ARL entry still available).
- Shows your **account tier** after login; a clear "ARL expired" re-login prompt.
- **Background playback** / close-to-tray in the GUIs.
- Album art (truecolor half-blocks in the TUI; native everywhere else).
- TUI extras: queue view, lyrics, help screen, themes, vim keys, resume.
- Settings persisted to `~/.config/opendeezer/`; ARL stays local.

## Install

Download a binary from [Releases](../../releases).

**GUIs** — just launch and click **Log in with Deezer**: an embedded web view
opens the Deezer login, and once you're in, your session (ARL) is captured
automatically and saved locally for next time. No manual token needed.

**Terminal (TUI)** — build it and provide your ARL:

```sh
make build          # -> ./opendeezer   (or: go build -o opendeezer ./cmd/opendeezer)
./opendeezer -save-arl <your-arl>   # writes ~/.config/opendeezer/arl.txt (0600)
./opendeezer
```

Or pass it inline: `DEEZER_ARL=<your-arl> ./opendeezer`. For the GUIs, see each
`gui/<platform>/README.md` for build steps. A Homebrew formula is in
`packaging/homebrew/`.

Your ARL is the `arl` cookie from an authenticated `deezer.com` browser session
(the GUI web-login grabs it for you). Treat it like a password — it grants
access to your account.

## Requirements

- A Deezer **Premium** account (HiFi tier for FLAC).
- Building from source: **Go 1.25+**, a C compiler, and a working audio device.
  The audio backend is [malgo](https://github.com/gen2brain/malgo) (miniaudio),
  so **cgo is required on every platform** (Linux/macOS/Windows).
- **Linux**: ALSA dev headers (`libasound2-dev`); plus the toolkit dev packages
  for the GUIs — GTK4/libadwaita/json-glib **and `libwebkitgtk-6.0-dev`** (GNOME
  web-login), and/or Qt6 **and `qt6-webengine-dev`** (KDE web-login).
- **macOS GUI**: macOS 26 (Tahoe) + Xcode 26 for the Liquid Glass APIs (the login
  web view uses the system WebKit framework — no extra dependency).
- **Windows GUI**: Windows 10 1809+/11, Visual Studio 2022 + Windows App SDK,
  MinGW-w64 (Go cgo builds the engine DLL), and the Edge **WebView2** runtime
  (preinstalled on Windows 11) for the login web view.
- TUI album art needs a 256-color or truecolor terminal.

## TUI controls

| Key | Action | | Key | Action |
|-----|--------|-|-----|--------|
| ↑/↓ or j/k | move | | z | toggle shuffle |
| g / G | top / bottom | | r | cycle repeat (off→all→one) |
| enter | open / play | | +/- | volume |
| esc / ⌫ | back | | ←/→ | seek ±10s |
| space | play / pause | | h | quality (Normal→High→HiFi) |
| n / p | next / prev | | R | toggle ReplayGain |
| f | like current track | | x | cycle crossfade |
| / | search | | ctrl+g | toggle gapless |
| l | lyrics (synced) | | d | output device |
| u | queue view | | c | now-playing + art |
| s | stop | | t | cycle theme |
| ? | help | | i | about · q quit |

Home screen entries: Liked Songs · My Playlists · ⚡ Flow · 📈 Charts ·
🎙 Podcasts · 🔍 Search (and ▶ Resume when a saved position exists).

## Remote control & automation (Control API)

OpenDeezer can expose a small HTTP/JSON API so another OpenDeezer client (remote
control) or an AI agent (MCP) can drive playback. It is **off by default**.

Enable it with an env var or a config file:

```sh
export OPENDEEZER_CONTROL=1                 # localhost only (127.0.0.1:7654)
export OPENDEEZER_CONTROL=:7654             # bind all interfaces (LAN remote)
# or: echo 1 > ~/.config/opendeezer/control.txt
```

Endpoints (reads are `GET`, mutations are `POST`):

| Method | Path | Action |
|--------|------|--------|
| GET  | `/whoami` | account name + auth mode (unauthenticated) |
| GET  | `/status` | playback snapshot (state, track, position, volume, queue…) |
| GET  | `/playlists`, `/search?q=` | browse |
| POST | `/playpause` `/next` `/prev` `/stop` `/restart` | transport |
| POST | `/repeat` `/shuffle` | cycle repeat / toggle shuffle |
| POST | `/seek?ms=` `/volume?v=` | position / volume (0..1) |
| POST | `/play/track?id=` `/play/playlist?id=` | play by id |

**Auth.** Credentials are sent via request **headers only**:

- **Account-based (default on LAN).** When bound to a non-loopback address with no
  token, a controller must prove it is logged into the **same Deezer account** by
  sending its own user id in `X-OpenDeezer-Account`. Your own devices connect with
  no token to copy; other accounts are rejected. `/whoami` deliberately does *not*
  reveal the user id (it's the credential), only the account name. This is
  LAN-trust grade — a Deezer user id is only semi-private. Disable with
  `OPENDEEZER_CONTROL_SAMEACCOUNT=0`.
- **Token (strongest).** Set `OPENDEEZER_CONTROL_TOKEN` (or
  `~/.config/opendeezer/control-token.txt`); send it in `X-OpenDeezer-Token`.
- **None.** Localhost binds with no token are open (loopback only).

Mutations require `POST` and reject requests carrying a browser `Origin` header,
so a web page you happen to visit can't drive your playback.

## How it works

```
ARL ─login (gw-light)→ browse (gw + public REST): search, charts, artists,
                       lyrics, Flow, podcasts, library writes
                     → resolve track → encrypted CDN URL (MP3 128/320 or FLAC)
                     → streaming download → Blowfish BF_CBC_STRIPE decrypt
                       (plain stream for podcast episodes)
                     → MP3 (go-mp3) / FLAC (mewkiz) decode → PCM ring
                     → malgo (miniaudio) output device → speakers
```

- `internal/deezer` — login, browse (search/charts/artists/lyrics/Flow/podcasts),
  library writes, track→URL resolve, the stripe decryptor.
- `internal/audio` — malgo backend: streaming buffer → decode → PCM ring with
  seek, ReplayGain, gapless, crossfade and output-device selection.
- `internal/queue` — the shared playback queue (shuffle/repeat/history).
- `internal/mpris` — Linux MPRIS media controls.
- `internal/log` — leveled file logging (`$OPENDEEZER_LOG`).
- `internal/ui` — the Bubble Tea TUI.
- `corelib` — the engine exposed as a C ABI (`-buildmode=c-archive` for
  macOS/Linux, `-buildmode=c-shared` DLL for Windows) so the native GUIs link it.

## Build from source

Clone the repo, then build whichever client you want — they all build the same
Go engine (`corelib`) underneath. Each `build.sh` / `build.ps1` compiles the
engine first, then the native app.

**Terminal (any OS)** — Go 1.25+ and a C compiler (the malgo audio backend needs
cgo on every platform; Linux also needs `libasound2-dev`, Windows needs
MinGW-w64):
```sh
CGO_ENABLED=1 go build -o opendeezer ./cmd/opendeezer      # or: make build
```

**macOS app** — macOS 26 (Tahoe) + Xcode 26, Go:
```sh
cd gui/macos && make app        # -> OpenDeezer.app (universal: Apple Silicon + Intel)
```

**Linux — unified** (auto-picks GTK/Qt) — `libgtk-4-dev libadwaita-1-dev
libjson-glib-dev libwebkitgtk-6.0-dev libsoup-3.0-dev qt6-base-dev
qt6-webengine-dev libasound2-dev meson ninja-build cmake` + gcc, Go:
```sh
cd gui/linux && ./build.sh && ./dist/opendeezer
```

**Linux — single toolkit:**
```sh
cd gui/gnome && ./build.sh && ./opendeezer-gnome     # GTK4 / libadwaita
cd gui/kde   && ./build.sh && ./opendeezer-kde       # Qt6 / Breeze
```

**Windows app** — Windows 10/11, Visual Studio 2022 + Windows App SDK,
MinGW-w64 (Go cgo), Go:
```powershell
cd gui\windows; .\build.ps1     # -> bin\x64\Release\OpenDeezer.exe
```

## FAQ

**How do I log in?**
In the GUIs, click **Log in with Deezer** — an embedded web view opens the real
Deezer login, and once you sign in, OpenDeezer reads the `arl` session cookie
automatically and saves it locally. Manual ARL entry is still there as a
fallback. The TUI uses `DEEZER_ARL` / `opendeezer -save-arl <arl>`.

**Does it have Flow / podcasts / charts / lyrics?**
Yes — Deezer Flow (personalized stream), podcast search + episode playback,
global charts, artist pages, and synced lyrics are all built in.

**Can I edit my library?**
Yes — like/unlike tracks, add tracks to playlists, and create/rename/delete
playlists.

**Can I choose the output device or use gapless/crossfade?**
Yes. The audio engine (malgo/miniaudio) supports output-device selection,
gapless transitions, crossfade and ReplayGain — all in settings (or TUI keys
`d` / `ctrl+g` / `x` / `R`).

**What's an ARL?**
Your Deezer session token — the `arl` cookie from a logged-in `deezer.com`
browser session. It authenticates you the same way the official app does. Treat
it like a password; it only ever lives on your own machine.

**Why does it need my Deezer login (ARL) instead of an API key?**
Deezer's public API doesn't allow full-track streaming. The only way to play
your music is the same authenticated path the official client uses, which needs
your session (the ARL).

**Why Deezer Premium only?**
Streaming full, high-quality tracks (and FLAC) is a Premium entitlement. A free
account can't stream full tracks the way OpenDeezer plays them. OpenDeezer only
plays content your own account is already entitled to.

**Why can't I download / save tracks?**
OpenDeezer is a *player*, not a ripper. It decrypts and decodes each track **in
memory** to play it — it never writes tracks to disk. Saving decrypted files
would be piracy; that's deliberately not what this does. Stream your own
entitled music, like the official app.

**Does my ARL get uploaded anywhere?**
No. Login, decrypt and decode all run on your machine; the only requests that
leave are to Deezer itself. The in-browser config generator never uploads your
token either.

**Is this legal? Will my account get banned?**
Grey zone. It reaches Deezer the unofficial way and decrypts your own entitled
content locally, which almost certainly breaks Deezer's terms for third-party
apps. Personal/educational use, your own Premium account, your own risk. Not
affiliated with Deezer.

**Does it support HiFi / FLAC?**
Yes — if your account is HiFi-entitled. Pick HiFi in settings (or press `h` in
the TUI); it streams lossless FLAC and falls back to MP3 when a track or account
isn't entitled.

**Why not just use the official app?**
Mostly because it's a reverse-engineering project and a learning exercise. You
also get lightweight, local-first native clients (including a terminal one), no
telemetry, on platforms the official app may not serve.

**Is it open source?**
Yes, AGPL-3.0. Read it, build it, audit exactly what it does.

## The fine print

Personal/educational use, your own Premium account, your own risk. It reaches
Deezer the unofficial way and decrypts your own entitled content locally, which
almost certainly breaks Deezer's terms for third-party apps. Not affiliated with
Deezer. AGPL-3.0.
