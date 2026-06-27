# Changelog

All notable changes to OpenDeezer are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
