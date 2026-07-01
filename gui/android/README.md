# OpenDeezer (Android)

Native **Kotlin / Jetpack Compose / Material 3** front-end for OpenDeezer — *an
open source reimplementation of Deezer*. The whole engine (login, browse,
Blowfish decrypt, decode, playback, OpenDeezer Connect) is the Go core bound to
an Android library (`app/libs/odmobile.aar`) via **gomobile** and called
in-process through the `odmobile.Odmobile` static API; this layer is UI only.

## Look

- **Material 3, dark-first:** a Deezer-purple (`#A238FF`) accent on a deep
  purple-black surface, a `NavHost` shell, lazy track lists, album/artist art
  carousels and a persistent bottom now-playing bar that expands into a full
  player.
- **Compose-only:** every screen is a composable; the only XML is the manifest,
  the launcher icon and a thin host theme.

## Features

- **Login** via an in-app `WebView` (`deezer.com/login`) that reads the `arl`
  cookie with `CookieManager` and calls `Odmobile.init(arl)`, plus a manual
  "paste ARL" fallback. The ARL is persisted (app-private `SharedPreferences`)
  for auto-login.
- **Free-account gate:** non-premium accounts get a "Premium required" screen.
- **Home:** Liked Songs · My Playlists · Flow · Charts · Podcasts · Search · Queue.
- **Browse:** tracks (tap to play), albums/artists/playlists carousels, artist
  top tracks, playlist & album tracks, podcast shows → episodes.
- **Player:** bottom bar + full now-playing screen (large art, seek, volume,
  like, lyrics, format), driven by a ~500 ms poll of the engine that auto-advances
  the in-app queue on `finishedCount`.
- **Synced lyrics** (current line highlighted by position), **Queue** view,
  **Search**, **Settings** (quality Normal/High/HiFi, ReplayGain, gapless,
  crossfade, plus the remote toggles below and an update check).
- **OpenDeezer Connect:** a cast button opens a device picker from
  `discoverDevices(700)` (name · type · version, plus a "This device" entry);
  `connectDevice` / `disconnectDevice` route playback to another OpenDeezer.
- **Remote settings** (in Settings): *Make this device reachable* advertises the
  phone as an OpenDeezer Connect host (`connectHostSetEnabled`), and *Phone
  Remote* serves the browser remote page (QR + code). Both persist in
  `SharedPreferences` and are re-applied after login (`AppViewModel.applyRemoteHosts`).

## Android TV

A second Gradle flavor (`tv`) reuses the same engine, `AppViewModel` and
`PlayerController` behind a **D-pad-driven, 10-foot Compose UI** on the leanback
launcher:

- `src/tv/` holds `TvActivity` + a focusable browse UI (Flow / Made-for-you /
  Charts / Albums / Playlists shelves), search, album/playlist detail lists and
  a persistent now-playing bar with focusable transport buttons. Focus scales and
  outlines the selected card so it reads from across the room.
- No extra dependencies — plain Compose `foundation` + `material3` with
  `clickable`/`onFocusChanged` for D-pad focus (no `androidx.tv`).
- The `mobile` flavor keeps the touch app unchanged. Each flavor supplies its own
  launcher manifest (`src/mobile/`, `src/tv/`); shared code lives in `src/main/`.
- App id `fr.cyclooo.opendeezer.tv`, so it installs alongside the phone app.

## Build & run

```sh
gui/android/build.sh
# -> app/build/outputs/apk/mobile/debug/app-mobile-debug.apk  (phone/tablet)
# -> app/build/outputs/apk/tv/debug/app-tv-debug.apk          (Android TV)
```

`build.sh` (1) installs gomobile/gobind, (2) binds the Go engine
(`gomobile bind -target=android -androidapi 24 -o gui/android/app/libs/odmobile.aar ./mobile`),
and (3) runs `./gradlew --no-daemon assembleMobileDebug assembleTvDebug`. CI
(`.github/workflows/android.yml`) does the same. Build a single flavor with
`assembleMobileDebug` or `assembleTvDebug`.

### Prerequisites

- **Go 1.24+**, **JDK 17**, the **Android SDK** (compileSdk 34) and an **NDK**
  (gomobile needs it; CI uses `ndk;26.3.11579264`).
- Point Gradle at your SDK via `gui/android/local.properties`
  (`sdk.dir=/path/to/Android/sdk`) or the `ANDROID_SDK_ROOT` env var.
- First Gradle run downloads Gradle 8.7 through the committed wrapper.

## Toolchain

- Gradle **8.7** · Android Gradle Plugin **8.5.2** · Kotlin **2.0.20**
  (`org.jetbrains.kotlin.plugin.compose`) · Compose BOM **2024.09.03**.
- `compileSdk`/`targetSdk` **34**, `minSdk` **24**, JDK **17**.
- Coil (`io.coil-kt:coil-compose`) loads `artworkUrl`s directly;
  Navigation Compose drives routing.

## Architecture

```
app/src/main/java/fr/cyclooo/opendeezer/
  MainActivity.kt        Compose host
  OpenDeezerApp.kt       auth gate + NavHost + bottom player bar + connect dialog
  AppViewModel.kt        login/account state, owns the PlayerController
  Routes.kt              navigation routes
  engine/Engine.kt       suspend facade over the gomobile Odmobile static API
  engine/Models.kt       data models + org.json parsers (match the wire shapes)
  player/PlayerController.kt  in-app queue, 500 ms poll, finishedCount auto-advance
  data/Prefs.kt          ARL + remote-host toggle persistence
  ui/                    theme, components (Artwork/TrackRow/PlayerBar) and screens

app/src/mobile/          phone launcher manifest (.MainActivity)
app/src/tv/              Android TV: TvActivity + D-pad Compose UI, leanback manifest, banner
```

Package id `fr.cyclooo.opendeezer` (`.tv` suffix for the TV flavor). Author: Cycl0o0.
