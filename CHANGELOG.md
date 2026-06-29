# Changelog

All notable changes to OpenDeezer are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
