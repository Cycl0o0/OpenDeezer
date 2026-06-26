# DeezerGUI (macOS)

Native SwiftUI front-end for DeezerTUI — an Apple-Music-style interface with a
Deezer-purple theme. The whole engine (login, browse, Blowfish decrypt, MP3
decode, playback) is the Go core compiled to a C static archive
(`Clib/libdeezercore.a`) and linked in-process; the Swift layer is UI only.

## Look

- **Theme:** Deezer "Electric Violet" `#A238FF` accent on a deep purple-black
  `#14041E`, defined in `Palette.swift`.
- **Layout:** source-list sidebar (Search / Library) with a pinned account row,
  hero headers on playlists & liked songs (artwork + Play/Shuffle), numbered
  track rows with hover-to-play and a now-playing tint, an album-art library
  grid, and a floating Apple-Music-style player bar (transport · now-playing
  scrubber · volume).

## Build & run

```sh
cd gui/macos
make run          # builds the Go archive + app bundle, then opens Deezer.app
```

Targets: `make corelib` (Go → `Clib/libdeezercore.a`), `make build`
(`swift build -c release`), `make app` (assemble `Deezer.app`), `make run`.

Needs Xcode/Swift 5.9+ and Go 1.24+. ARL is read from `$DEEZER_ARL` or
`~/.config/deezertui/arl.txt` (same as the TUI).

## Architecture

```
Sources/DeezerGUI/
  App.swift        sidebar, detail routing, hero header, track table, grid, search
  PlayerBar.swift  floating transport bar
  AppState.swift   @MainActor store; polls the engine; queue + shuffle/repeat
  Bridge.swift     thin Swift wrapper over the C API (DZ* functions)
  Models.swift     Codable wire models (match corelib JSON)
  Palette.swift    Deezer brand colors
Clib/              module map + the generated libdeezercore.{a,h}
```

The C API is defined in `../../corelib/deezercore.go`
(`go build -buildmode=c-archive`).
