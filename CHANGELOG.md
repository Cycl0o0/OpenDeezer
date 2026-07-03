# Changelog

All notable changes to OpenDeezer are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.8.2]

### Changed
- **macOS releases are now Developer ID signed and notarized.** The release
  workflow signs the `.app` with a hardened runtime, submits it to Apple's notary
  service and staples the ticket, so Gatekeeper opens it with no "unidentified
  developer" / "damaged" prompt and no quarantine dance. Signing activates only
  when the signing secrets are present, so forks and secret-less builds still
  produce the same unsigned zip. See `docs/MACOS_SIGNING.md`.

### Fixed
- The Windows `app.manifest` assembly version had been frozen at 1.6.0.0 since the
  1.6.0 release (the version bump matched the literal current version and silently
  missed once the file drifted); it now tracks the release version, and the bump
  script rewrites it value-agnostically so it can't freeze again. The AUR
  `.SRCINFO` source URL had the same class of bug and is fixed too.

## [1.8.1]

### Added
- **"No Internet" screen** (all clients): when the engine can't reach Deezer at
  launch or while browsing — DNS failure, connection refused, host/network
  unreachable or a timeout — the TUI and every GUI (macOS, iOS, Android phone +
  TV, Windows, GNOME, KDE) now show a dedicated **No Internet** screen with a
  **Retry** action instead of dropping the user back to the login/ARL screen. The
  session is kept, so recovering connectivity and retrying resumes where you were
  rather than forcing a re-authentication. The new screen is localized in all
  seven languages.

### Changed
- The shared Go engine now **distinguishes a network outage from an expired
  ARL**. `internal/deezer` classifies transport-level failures as the new
  `ErrNoNetwork` (exported through the SDK) separately from `ErrARLExpired`, and
  the FFI bindings surface it to the native apps (`DZLoginErrorKind` in the
  c-archive, `LoginErrorKind` in the gomobile binding). Previously a launch with
  no connectivity was misreported as "invalid or expired ARL" and pushed the user
  toward re-authenticating; it now correctly reads as an outage you can retry.

## [1.8.0]

### Added
- **Full UI localization** (all clients): the whole product — the TUI, the phone
  web remote, and the macOS, iOS, Android (phone + TV), Windows, GNOME and KDE
  apps — is now translated into six new languages alongside English: 简体中文
  (`zh`), हिन्दी (`hi`), Español (`es`), Français (`fr`), العربية (`ar`) and
  Русский (`ru`). Each client follows the system language and falls back to
  English, with a per-app **Language** setting to override it (the TUI reads
  `LANG` or its 🌐 Language menu); Arabic switches the GUIs to a right-to-left
  layout. Strings are localized natively per platform — Go JSON catalogs
  (`internal/i18n`) for the TUI, an inline dictionary for the web remote,
  `.lproj`/String Catalogs on macOS/iOS, `.resw` on Windows, gettext `.po` on
  GNOME, Qt `.ts` on KDE, and `values-*` resources on Android — each with the
  plural rules the language needs and the shared brand/UI terms kept consistent
  across all of them.
- **Translation contributor guide** (`docs/TRANSLATIONS.md`): per-client steps for
  adding or fixing a language, the shared-term glossary rule, and how to build and
  verify each client.

## [1.7.0]

### Added
- **10-band graphic equalizer** (all clients): peaking-filter bands at the classic
  31 Hz – 16 kHz octave centers, ±12 dB each, with ten presets (flat, bass boost,
  bass reducer, treble boost, vocal, rock, pop, jazz, classical, electronic — any
  manual tweak becomes "custom") and a ±12 dB preamp. The DSP runs in the shared
  Go engine's realtime path (lock-free, allocation-free biquad cascade), and the
  state persists engine-side, so the TUI (`E` toggle, `ctrl+e` preset), the
  macOS/iOS/Android (phone + TV)/Windows/GNOME/KDE apps, the phone web remote and
  the control API (`GET`/`POST /eq`) all see one shared equalizer. Exposed in the
  SDK (`player.SetEQ*`) and as `get_eq`/`set_eq` MCP tools.
- **Mono downmix** (all clients): folds stereo to mono for single-speaker setups
  and accessibility; lives next to the equalizer everywhere, independent of it.
- **Podcast episodes now carry their show name** over every client wire
  (`podcastName`), so episode lists can show it consistently.
- **Engine-side logout** on Android/iOS: signing out now also logs the engine
  out and shuts down the Connect host/web-remote servers, so the old account's
  library and credentials are no longer reachable over the LAN after a switch.

### Fixed
An audited sweep (every finding independently verified before fixing) across the
core and all eight clients:

- **Audio engine**: seeking works again in the last ~4 s of a track (and in
  short fully-decoded tracks); seeks no longer play 200–400 ms of stale pre-seek
  audio; crossfade no longer double-counts the incoming track's opening
  (position drift + every-other-transition overlap) and applies each track's own
  ReplayGain inside the fade window; switching output devices can no longer run
  two realtime callbacks at once; unplugging the output device falls back to the
  system default (or surfaces an error) instead of wedging in "Playing"; podcasts
  encoded at rates other than 44.1 kHz are resampled instead of playing at the
  wrong speed/pitch; track downloads use a dedicated HTTP client with sane
  timeouts and are cancellable (a stalled CDN read no longer leaks a goroutine +
  full track buffer); two races around gapless advance and sleep-timer re-arm.
- **Deezer client**: expired license tokens are refreshed and real `get_url`
  errors surfaced (no more blanket "track unavailable"); share URLs with query
  strings resolve to the right track; gateway errors during login are no longer
  misreported as "ARL expired"; REST/search error envelopes are checked instead
  of decoding as empty success; IDs are JSON-escaped in gw request bodies.
- **TUI**: browsing a track list no longer clobbers the live play queue;
  enabling the Web Remote preserves the configured token (MCP/token clients kept
  working); repeated Web-Remote toggles no longer leak servers; slow track
  resolves can't override a newer selection; stale preloads after
  shuffle/repeat changes; the device picker esc-trap; footer state after an
  end-of-track sleep stop; remote-screen lyrics key and quit-cleanup.
- **Queue/config**: shuffle now plays a true permutation (no repeats-before-
  exhaustion, honors repeat-off, reshuffles on repeat-all) and records manual
  jumps in history; config writes are atomic (a crash can't truncate the control
  token and silently downgrade auth); IPv6 Connect peers work; `false`/`no`
  disable values parse correctly; log-level parsing is case-insensitive.
- **Control API**: Host-header validation blocks DNS-rebinding against the
  localhost no-auth mode.
- **MPRIS**: no longer crashes the app when the D-Bus connection drops; Pause()
  pauses instead of toggling; Seeked is emitted; per-track `mpris:trackid`;
  redundant PropertiesChanged storms stopped; Quit() works.
- **Discord**: connecting no longer blocks the player loop; the IPC socket is
  drained (no more buffer-full stalls); progress updates after seeks and on
  repeat-one; 1-character titles and empty-artist pause states render correctly.
- **macOS**: gapless/crossfade auto-advance no longer snaps the UI back to the
  previous track; Connect-routed transport controls moved off the main thread
  (no more 15 s beachballs); browsing Playlists/Search no longer wipes the play
  queue; opening Settings doesn't silently rewrite the control-API config;
  volume stays in sync with remote changes; stale gapless preloads are cleared
  on shuffle/repeat; web-login retry works after a failed ARL verify.
- **GNOME**: Play/Pause during a podcast episode pauses (instead of starting an
  unrelated queue track); Connect-routed transport + disconnect calls moved off
  the GTK main thread; now-playing metadata no longer blanks after a race;
  playlist names render literally (Pango markup injection); manual-ARL login
  re-enables after use; MPRIS Next honors shuffle/repeat; duplicate playlist
  rows from stale fetches; dead Play button after the queue finishes.
- **KDE**: two use-after-frees (podcast cover-art callbacks, login-helper crash
  path) and one at-quit crash; Connect-routed transport moved off the GUI
  thread; Settings OK no longer silently disables an active Phone Remote.
- **Windows**: media keys / SMTC overlay work on .NET 8 (interop used an
  interface type that throws on .NET 5+); Connect-routed transport moved off the
  UI thread; concurrent track starts serialized (no more wrong-track races with
  stale preloads); Settings apply-on-LostFocus no longer kills an active Phone
  Remote; stale search/navigation responses can't overwrite newer pages.
- **Android**: playback survives backgrounding (foreground media service +
  MediaSession + audio focus — pauses for calls, resumes after); Podcasts work
  (wire-contract mismatch made every list come back blank); Connect-routed
  controls no longer freeze the UI (ANR); sign-out isn't undone by a stale
  WebView cookie; double-advance race after manual track selection; TV error
  states show a Retry instead of a blank screen; queue empty-state layout.
- **iOS**: audio session handling reworked — playback recovers after phone
  calls/Siri/other apps (interruption + route-change handling), the session
  activates on play instead of at launch (no longer pauses other apps' music on
  open), and the engine's output is suspended when idle so the app can actually
  sleep in the background; repeat-one no longer halts at end of track; Connect
  discovery works on physical devices (multicast entitlement); podcast episodes
  show real durations; artwork fetches no longer block transport controls;
  manual track selection can't be skipped by a stale finish signal; image cache
  is bounded and responds to memory pressure.
- **MCP server**: JSON-RPC compliance (parse-error responses, notification
  handling, non-zero exit on stdin failure).
- **SDK**: `DownloadTrackContext` (cancellation + timeouts); Connect host
  startup no longer leaks a server on partial failure; same-account auth and
  device-label documentation/behavior fixes; example data races fixed.
- **Packaging/CI**: AUR/Homebrew/winget/Flatpak manifests track releases again
  (were pinned at 1.0.0); Android release APKs support a stable signing key via
  repo secrets (upgrades across releases); KDE flatpak launches from the app
  menu; unified-build icon names; release-checksum job no longer emits a stale
  self-reference on re-runs; `make cross` Windows target fixed for the cgo
  audio engine.
- **Version reporting**: the desktop GUIs' embedded engine and the MCP server
  reported 1.5.2 on 1.6.0 builds (perpetual false "update available" banner);
  all version constants now track the release.

## [1.6.0]

### Added
- **Sleep timer** (all clients): a core-owned countdown that pauses playback
  after a chosen interval — Off / 15 / 30 / 45 / 60 minutes, or **at the end of
  the current track**. It fades the audio out smoothly over the last few seconds
  before pausing. Because the timer runs on the audio engine's own clock in the
  shared Go core, every client shows the same behaviour: the TUI (`T` to cycle),
  the phone web remote (a Sleep button), and native controls in the macOS, iOS,
  Android (phone + TV), Windows, GNOME and KDE apps. Also reachable over the
  control API (`POST /sleep`) and the SDK.
- **Perceptual volume taper**: the volume control now follows a cubic (perceptual)
  curve instead of a linear one, so the slider feels natural across its whole
  range instead of jumping to near-full loudness in the bottom third. The public
  0–1 volume API is unchanged, so every client inherits the fix with no UI work.
- **Anti-click micro-fades**: a ~12 ms ramp is applied after a playback
  discontinuity (track start, resume, seek/scrub) to eliminate the click/pop that
  cutting into a fresh waveform used to produce. Pure core polish; all clients.

### Fixed
- **ReplayGain with gapless**: after a gapless track change the engine kept
  applying the *previous* track's ReplayGain, so every gaplessly-advanced track
  played at the wrong loudness. The per-track gain is now recomputed on the swap.
- **ReplayGain toggled mid-track**: enabling ReplayGain during playback now takes
  effect immediately instead of only on the next track.
- **Shuffle/repeat vs. gapless preload (TUI)**: toggling shuffle or repeat after
  the next track had already been preloaded could desync the queue pointer from
  the audio (footer/now-playing/lyrics/MPRIS showed a different track than was
  playing). The finish handler now advances deterministically to the preloaded
  track, and toggling shuffle/repeat invalidates and re-issues the preload.
- **Connect remote auto-advance (mobile)**: when playback was routed to another
  device, a track ending was only observed by the status poller, which never
  fired the auto-advance — so remote playback halted after each track. The poller
  now detects the track-end transition and advances, matching the desktop engine.
- **Crash/data race in device discovery (macOS/GNOME/KDE/Windows)**: the shared
  control-server pointer was read without its lock while the settings toggles
  could null it, a data race that could nil-dereference and crash the GUI across
  the cgo boundary. The read is now taken under the lock. Same unlocked read fixed
  in the mobile discovery path.
- **Empty device picker on discovery error**: a transient/partial LAN discovery
  error discarded the manually-configured (VPN/Tailscale) peers too, leaving the
  picker empty. Configured peers are now always returned.
- **Redirect cap**: the Deezer HTTP client had its 10-redirect safety limit
  disabled by a custom redirect policy; the cap is restored so a misbehaving host
  can't loop until the timeout.
- **Discord RP hang**: the IPC handshake read had no deadline while holding the
  presence lock, so a stale/foreign `discord-ipc` socket could block shutdown. A
  read deadline is now set on the handshake.
- **Pairing code no longer accepted via query string** (control API): the 6-digit
  pairing credential is read from the request body only (matching the module's
  header/body-only policy), so it can't leak into proxy logs, history or Referer.
- **Crossfade allocation**: the crossfade path allocated a buffer on every
  realtime audio callback; it now reuses a scratch buffer to avoid RT-thread
  churn.
- **macOS**: switching accounts no longer stacks duplicate 0.4s polling timers or
  duplicate media-key handlers (Next/Prev used to jump two tracks after a switch).
- **iOS**: the app version was stuck at 1.5.1 in the committed Xcode project; it
  now tracks the release.
- **Android**: the Now-Playing heart now reflects the track's real favourite
  state on entry (instead of always empty), and the login WebView (phone + TV) is
  destroyed when its screen leaves composition to stop leaking the renderer.
- **Windows**: switching accounts while on Home now refreshes Home for the new
  account, and the login WebView2 is closed so it doesn't leak browser processes.
- **GNOME**: shuffle and repeat-all now actually work for local playback (they
  used to only light up the buttons); the Settings/device combo models no longer
  leak; the About/meson version is corrected.
- **KDE**: fixed two use-after-free crashes where the Settings update-check and
  the login ARL-verify could touch a dialog that had already been dismissed.

## [1.5.2]

### Added
- **Android TV**: a second Gradle flavor (`tv`, app id `fr.cyclooo.opendeezer.tv`)
  ships a D-pad-driven, 10-foot Compose UI on the leanback launcher. A
  Netflix/YouTube-style **left navigation rail** (Home / Search / Library /
  Settings) that expands with labels on focus; a cinematic **featured hero**;
  focusable poster shelves (Flow / Made-for-you / Charts / Albums / Playlists);
  album & playlist **detail** pages; a full **Settings** screen; and a now-playing
  bar with a progress bar and Material transport controls. Reuses the same engine,
  `AppViewModel` and `PlayerController` as the phone app; no `androidx.tv`
  dependency. Built with `assembleTvDebug` (phone app is `assembleMobileDebug`).
- **WebView sign-in on Android TV**: log in with your real Deezer account and the
  ARL is captured automatically — no token to type on a remote (manual-paste
  fallback kept).
- **Android remote settings** (phone + TV): Settings now has an *OpenDeezer
  Connect — make this device reachable* toggle (advertise as a Connect host, with
  the LAN address shown) and a *phone remote* toggle with QR + pairing code, plus
  a *play on another device* picker (discover / connect / disconnect). All persist
  and are re-applied after login.

### Fixed
- **Android audio settings now persist**: quality, ReplayGain, gapless and
  crossfade are saved to preferences and re-applied after login, so they no longer
  reset on relaunch (same fix as iOS in 1.5.1). Applies to the phone and TV apps.

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
