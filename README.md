# OpenDeezer

**An open-source reimplementation of Deezer.** Log in once. The GUIs sign you in
with an embedded Deezer web view, so there's no ARL to go hunting for. Then
browse your liked songs, playlists, charts, artists, and search. Every track
plays **locally**: it's streamed in, Blowfish-decrypted, decoded, and played
from memory (MP3, or FLAC on HiFi). Your ARL stays on your machine and only goes
to Deezer in the requests OpenDeezer makes for you.

One Go engine handles the whole streaming path (login, decrypt, decode,
playback). Seven native front-ends sit on top of it. By **Cycl0o0**.

## Clients

| Client | Stack | Builds |
|--------|-------|--------|
| **Terminal (TUI)** | Go · Bubble Tea | linux · macOS · windows (amd64/arm64) |
| **macOS** | SwiftUI · Liquid Glass (macOS 26) | universal (Apple Silicon + Intel) `gui/macos` |
| **Linux (unified)** | auto-picks GTK4 or Qt6 by desktop | x86_64 · aarch64 `gui/linux` |
| **GNOME** | GTK4 · libadwaita | x86_64 · aarch64 `gui/gnome` |
| **KDE** | Qt6 Widgets · Breeze | x86_64 · aarch64 `gui/kde` |
| **Windows** | WinUI 3 · C++/WinRT · Fluent | x64 `gui/windows` |
| **Android** | Kotlin · Jetpack Compose | arm64/arm/x86_64 (gomobile AAR) `gui/android` |

The **unified Linux** client is a single `opendeezer` command that picks the
native toolkit for your desktop (Qt/Breeze on KDE-family, GTK4/libadwaita
elsewhere), the way LibreOffice does. If you'd rather have one toolkit, the
standalone `gui/gnome` and `gui/kde` binaries are there too.

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

**GUIs** — launch one and click **Log in with Deezer**. An embedded web view
opens the Deezer login; once you're in, your session (ARL) is saved locally for
next time. No token to paste.

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
- **Android**: Android 7.0+ (API 24). Building needs JDK 17, the Android SDK +
  NDK, and gomobile.
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

This is engine-hosted, so it works in **every client** — the TUI *and* all native
GUIs (the same `OPENDEEZER_CONTROL` / Discord settings apply). From a GUI the
engine exposes play/pause, stop, seek, volume, restart, play-track/playlist and
status (next/prev/shuffle/repeat live in the GUI's own queue).

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
so a web page you happen to visit can't drive your playback. The server also caps
request/response sizes + sets timeouts (slowloris/DoS), and **refuses to start
unauthenticated on a non-loopback address** — a LAN bind always requires
same-account or token auth, failing closed on a misconfiguration.

> **Security note.** A Deezer user id is only *semi-private* (it appears in
> profile URLs), so same-account auth is LAN-trust grade — fine for a home
> network. On an untrusted network, set `OPENDEEZER_CONTROL_TOKEN` for a real
> secret. OpenDeezer Connect authenticates to *discovered* devices with the
> account id only (never the token), since a discovery reply is unauthenticated.

### Remote control (one client drives another)

Enable the Control API on the target (`OPENDEEZER_CONTROL=:7654`), then on
another OpenDeezer client open **📡 Remote control** from the menu, enter the
target's `host:port`, and connect. Transport keys (space/n/p/s, ←/→ seek, +/-
volume, r/z) drive the remote; the screen shows its live now-playing. Same
Deezer account auto-authenticates (or share a token).

**OpenDeezer Connect** (GUIs + TUI) auto-discovers devices on the same LAN via
UDP multicast/broadcast. That needs a network that carries multicast/broadcast —
**Tailscale/VPN meshes don't** (they're unicast-only), and some routers filter it
between Wi-Fi and Ethernet. For those, list peers explicitly so they always show
in the picker:

```sh
# one host[:port] per line (port defaults to 7654)
#   ~/.config/opendeezer/connect-peers.txt   (also read on macOS)
echo "100.78.213.67:7654" >> ~/.config/opendeezer/connect-peers.txt
# or: export OPENDEEZER_CONNECT_PEERS=100.78.213.67:7654,192.168.1.20
```

(You can always just type the address into "Enter address…" too.)

### MCP server (AI agent control)

`opendeezer-mcp` is a [Model Context Protocol](https://modelcontextprotocol.io)
server that lets an AI assistant control playback through the Control API. Build
it with `go build ./cmd/opendeezer-mcp` (or `make` builds it alongside the TUI),
enable the Control API (above), then register it with your MCP client:

```json
{
  "mcpServers": {
    "opendeezer": {
      "command": "/path/to/opendeezer-mcp",
      "env": {
        "OPENDEEZER_CONTROL_URL": "http://127.0.0.1:7654",
        "OPENDEEZER_CONTROL_TOKEN": "your-token-if-set"
      }
    }
  }
}
```

Tools: `get_status`, `play_pause`, `next`, `prev`, `stop`, `restart`,
`cycle_repeat`, `toggle_shuffle`, `set_volume`, `seek`, `search`,
`list_playlists`, `play_track`, `play_playlist`.

## Discord Rich Presence

Show what you're listening to on your Discord profile. **Off by default** — it
needs a Discord application id (create one at the [Discord Developer
Portal](https://discord.com/developers/applications); optionally upload an art
asset named `opendeezer`):

```sh
# Linux:  ~/.config/opendeezer/discord-app-id.txt
# macOS:  ~/Library/Application Support/opendeezer/discord-app-id.txt
echo your-application-id > ~/.config/opendeezer/discord-app-id.txt
# (env var also works for the TUI: export OPENDEEZER_DISCORD_APP_ID=...)
```

With Discord running, your now-playing track appears as "Listening to …" with a
live progress bar. macOS/Linux only (Windows pending). If Discord isn't running
it's silently skipped.

> **GUI users (esp. macOS):** apps launched from Finder/Activities do **not**
> inherit your shell environment, so set the id via the **file** above, not the
> env var. The config file is read from the platform config dir *and*
> `~/.config/opendeezer/` (so either path works). Check
> `~/Library/Application Support/opendeezer/opendeezer.log` (macOS) — it logs
> `rich presence enabled (app …)` / `connected` once it's working.

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
- `mobile` — the engine exposed for **gomobile** (`Odmobile` AAR) so the Android
  app drives it from Kotlin.

## Build from source

Clone the repo and build whichever client you want. They all use the same Go
engine (`corelib`) underneath; each `build.sh` / `build.ps1` compiles the engine
first, then the native app.

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

**Android app** — Go, JDK 17, Android SDK + NDK, and gomobile. `build.sh` binds
the engine to an `Odmobile` AAR, then Gradle assembles the APK:
```sh
cd gui/android && ./build.sh    # -> app/build/outputs/apk/debug/*.apk
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
OpenDeezer plays music, it doesn't rip it. Each track is decrypted and decoded
**in memory** to play, and never written to disk. Saving decrypted files would
be piracy, so it doesn't. Play your own entitled music, the same as the official
app.

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
also get lightweight native clients (including a terminal one) with no telemetry,
on platforms the official app doesn't always cover.

**Is it open source?**
Yes, AGPL-3.0. Read it, build it, audit exactly what it does.

## The fine print

Personal/educational use, your own Premium account, your own risk. It reaches
Deezer the unofficial way and decrypts your own entitled content locally, which
almost certainly breaks Deezer's terms for third-party apps. Not affiliated with
Deezer. AGPL-3.0.
