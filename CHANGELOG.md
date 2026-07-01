# Changelog

All notable changes to OpenDeezer are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **Android TV**: a second Gradle flavor (`tv`, app id `fr.cyclooo.opendeezer.tv`)
  ships a D-pad-driven, 10-foot Compose UI on the leanback launcher — focusable
  Flow / Made-for-you / Charts / Albums / Playlists shelves, search, album/playlist
  detail lists and a now-playing bar. Reuses the same engine, `AppViewModel` and
  `PlayerController` as the phone app; no `androidx.tv` dependency. Built with
  `assembleTvDebug` (phone app is `assembleMobileDebug`).
- **Android remote settings**: Settings now has an *OpenDeezer Connect — make this
  device reachable* toggle (advertise as a Connect host) alongside the existing
  phone remote. Both toggles persist and are re-applied after login.

## [1.5.1]

### Added
- **iOS app** (8th client): a native SwiftUI iPhone app — Apple-Music-style, with
  **Liquid Glass** (iOS 26, material fallback below), lock-screen controls, Home,
  browse/search, Connect and the phone remote. Built via a gomobile xcframework.
- **Update check**: every client checks GitHub for a newer release on launch and
  shows a dismissible "update available" notice + a "Check for updates" action.
  It only notifies and links the download — never installs anything.
- **Remote control in Settings**: the control API / phone remote is now editable
  in each desktop client's Settings (enable, LAN, token), on top of the env vars
  / config files. New engine API `DZControlConfigJSON` / `DZSetControlConfig`.

### Changed
- Reworded UI copy across all clients to be terser and more native (fewer
  marketing-y strings), and removed the "HiFi falls back to MP3" note from the
  quality options.

### Fixed
- **Home** now loads after login on GNOME/KDE (was empty when the sidebar's first
  row was current before sign-in).
- **Repeat/shuffle over OpenDeezer Connect** now take effect: the controller
  auto-advances when the remote finishes a track and applies repeat/shuffle.

## [1.5.0]

### Added
- **Home screen**: the GUIs now open on a native Home page instead of going
  straight to Liked Songs — a time-based greeting, quick-pick cards (Liked ·
  Flow · Charts · Podcasts), a "Top Tracks" rail and a "Your Playlists" rail.
  Backed by a new engine aggregator (`DZHomeJSON` / gomobile `Home()`).

### Security
- **Continuous fuzzing**: native Go fuzz harnesses for the BF_CBC_STRIPE decrypt
  and FLAC decode paths, wired into CI via ClusterFuzzLite (OSS-Fuzz's engine).
  Added `SECURITY.md` (report to security@cyclooo.fr) + `docs/FUZZING.md`.

## [1.2.0]

### Changed
- **Native player bars**: the now-playing/transport bars now feel like real
  native audio players — native platform icons instead of text/emoji, with
  tooltips.
  - **KDE**: Breeze theme icons throughout (like → `emblem-favorite`, lyrics/
    artist/shuffle as icons, "Repeat: Off/All/One" text → a stateful repeat icon,
    📡 → `network-wireless`, "Vol" → a volume icon), and the explicit emoji → a
    small "E" badge.
  - **Windows**: a Groove-Music-style transport — cover + title/artist on the
    left, the controls centred with play/pause as a filled accent circle and the
    seek bar + times directly under it, and lyrics/artist/connect/volume on the
    right (Repeat/Lyrics/Artist now Segoe Fluent icons).
  - **GNOME**: already native; added the missing transport tooltips.

## [1.0.2]

### Added
- **Public Go SDK**: the engine is now a public library you can build on —
  `sdk/deezer` (Deezer API + track decode/download), `sdk/connect` (OpenDeezer
  Connect LAN discovery + drive/host a device), `sdk/control` (control server +
  client and phone web remote), and `sdk/player` (in-process playback, cgo).
  Symmetric in/out APIs, runnable `examples/`, and full docs in `sdk/README.md`.

## [1.0.1]

### Added
- **Phone web remote**: control playback from your phone's browser on the same
  Wi-Fi, paired with a QR + 6-digit code. Opt-in, LAN-only; transport +
  now-playing (play/pause, prev/next, seek, volume, repeat, shuffle). On every
  client (TUI + all GUIs).

### Fixed
- **OpenDeezer Connect — disconnect**: disconnecting a device now stops playback
  on it instead of leaving it playing unattended.
- **OpenDeezer Connect — Artist/Lyrics**: now resolve to the track actually
  playing on the connected device (previously showed the wrong track, or nothing).
- **OpenDeezer Connect — repeat/shuffle**: changes are now forwarded to the
  connected device.
- **Podcasts**: playing an episode after a song now shows the episode's
  now-playing info (title / show / artwork) instead of the previous track's.

## [0.6.0]

### Added
- **Premium-only enforcement**: Free accounts are now blocked behind a clear
  "account not supported — subscribe to Deezer Premium" message (TUI + all GUIs).
- **Explicit "E" badge** on tracks across every list (TUI + all GUIs), parsed
  from Deezer's explicit-content flag.
- **Re-login / switch account** on demand in all four GUIs.

### Changed
- **macOS GUI audio backend → oto**: malgo's CoreAudio callback was unreliable
  inside the c-archive GUI (choppy MP3/FLAC); the macOS GUI now uses oto (smooth).
  Output-device selection is malgo-only, so it's unavailable in the macOS GUI;
  the TUI and GNOME/KDE/Windows keep it.
- Playback now buffers the full track + prebuffers ~2s before starting, fixing
  the choppy intro / streaming glitches.

### Fixed
- **KDE login**: the Deezer web login runs in a separate `opendeezer-login`
  helper process (QtWebEngine out-of-process), so it works in the dlopen'd
  unified launcher and can't crash the app (manual ARL remains a fallback).

## [0.5.0]

### Fixed
- **macOS GUI choppy audio**: malgo's CoreAudio period defaulted to ~10ms, so Go
  GC pauses in the GUI process delayed the realtime audio callback and underran
  the device. Use a larger device period (~400ms) so playback coasts through GC
  pauses. (Confirmed fixed on macOS.)
- **KDE login web view**: clicking "Log in with Deezer" closed the window —
  QtWebEngine's GPU process crashes on Wayland/KDE. Force software GPU
  (`QTWEBENGINE_CHROMIUM_FLAGS=--disable-gpu`) so the login web view starts; the
  view also no longer collapses to 0px. Added a File → "Log in / Switch account…"
  action to reach login when already signed in. Manual ARL remains a fallback.

## [0.4.1]

### Fixed
- **Choppy audio** (reported on macOS): the PCM ring did a full-buffer memmove
  under its lock on every audio callback, starving the decoder and underrunning
  the buffer. Replaced with a true circular buffer + a lock-free (atomic) audio
  callback, with ~4s of buffer headroom.
- **KDE login window never appeared**: `startLogin()` ran inside the MainWindow
  constructor and exec'd the modal login dialog (a nested event loop) before the
  window was shown, blocking construction. It now runs after the event loop starts.

## [0.4.0]

### Added
- **One-click login**: each GUI embeds a Deezer web view (WKWebView / WebKitGTK /
  QtWebEngine / WebView2) that captures the `arl` cookie after sign-in — no more
  pasting an ARL by hand (manual entry kept as a fallback).
- **Library editing**: like/unlike tracks; add/remove playlist tracks; create,
  rename and delete playlists. (gw `favorite_song.*` / `playlist.*`.)
- **Deezer Flow** — personalized endless stream.
- **Podcasts** — search shows, list episodes, play (plain/unencrypted stream).
- **Artist pages** surfaced from search/charts; **charts** now show albums,
  artists and playlists (not just tracks); search returns artists.
- **New audio engine (malgo / miniaudio)**: streaming buffer (faster start),
  **output-device selection**, **gapless** transitions, **crossfade**
  (experimental), seek and ReplayGain. Replaces oto (now cgo on every platform).
- New C API: write ops, `DZFlowJSON`, podcast + audio-device + gapless/crossfade
  exports; `DZSearchJSON` now includes artists.

### Notes
- Output-device selection required the audio-backend swap to malgo; playback
  paths are runtime-tested by users (CI compiles all platforms incl. cgo).
- Packaging: AUR, Flatpak and winget manifests added (alongside Homebrew).

## [0.3.0]

### Added
- **Shared playback queue** (`internal/queue`): shuffle / repeat (off·all·one) /
  prev-history are now defined once and unit-tested, used by the TUI and exposed
  for frontends instead of being re-implemented per UI.
- **Account tier detection**: login now parses the plan name and HQ/HiFi
  entitlements. The TUI shows "Logged in as <name> · <offer>" and warns when the
  selected quality exceeds the plan. New C API: `DZAccountJSON`.
- **Expired-ARL handling**: `deezer.ErrARLExpired` distinguishes a dead cookie
  from a network error, with a clear re-login prompt in the TUI.
- **Charts**: global top tracks / albums / artists / playlists via REST `/chart`.
  TUI menu entry + `DZChartsJSON`.
- **Artist profiles**: top tracks, discography and related artists via REST
  `/artist/*`. Artist results in search; `DZArtistTopJSON` / `DZArtistProfileJSON`.
- **Lyrics** (synced when available) via `song.getLyrics`. TUI lyrics screen
  (key `l`) that auto-scrolls/highlights with playback; `DZLyricsJSON`.
- **ReplayGain** loudness normalization (attenuate-only) using the track GAIN
  field. Toggle `R` in the TUI; `DZSetReplayGain` / `DZReplayGain`.
- **Resume playback**: the last track + position is saved and offered as a
  "Resume" entry on the home screen.
- **Queue view** (key `u`) and **Help screen** (key `?`).
- **Vim keys**: `j`/`k` move, `g`/`G` jump to top/bottom.
- **Themes**: cycle color schemes with `t` (deezer · ocean · sunset · mono · matrix).
- **Podcast-ready playback**: the player can play plain (unencrypted) CDN streams.
- **Leveled file logging** (`internal/log`), level via `$OPENDEEZER_LOG`, written
  to `opendeezer.log` (never stdout, so the TUI is unaffected).
- **CI**: build · vet · `go test -race` + coverage · golangci-lint · govulncheck,
  plus Dependabot for Go modules and GitHub Actions.

### Notes
- Fuzzy search was already provided by the Bubbles list default filter (`/`).
- Native GUI wiring for the new C API functions (Swift/Qt/GTK/WinUI) is pending.

## [0.2.0]
- 6 clients (TUI + macOS/GNOME/KDE/unified-Linux/Windows GUIs), unified Linux
  launcher, HiFi/FLAC, OS media controls, settings, output info, seek/quality keys.
