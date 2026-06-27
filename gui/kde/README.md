# OpenDeezer (KDE)

Native KDE / Qt6 Widgets front-end for OpenDeezer — *an open source
reimplementation of Deezer*. The whole engine (login, browse, Blowfish decrypt,
MP3 decode, ALSA playback) is the Go core compiled to a C static archive
(`lib/libdeezercore.a`) and linked in-process; this layer is UI only.

## Look

- **Breeze-native:** a plain `QMainWindow` with a `QListWidget` sidebar, a
  `QStackedWidget` of content pages and a `QTableWidget` track list. Qt6 Widgets
  follow the system Breeze `QStyle` on Plasma automatically.
- **Deezer purple (`#A238FF`):** applied as scoped QSS on the accent widgets
  only (seek + volume sliders, play button, shuffle toggle) — the rest of the
  app keeps the native style.
- **Transport bar:** cover thumbnail · now-playing · prev / play-pause / next ·
  position + seek + duration · shuffle / repeat · volume.

## Build & run

```sh
cd gui/kde
./build.sh          # builds the Go archive, then the Qt app
./opendeezer-kde
```

`build.sh` first runs `go build -buildmode=c-archive` from the repo root to emit
`lib/libdeezercore.{a,h}`, then configures and builds with CMake.

### Dependencies (Debian/Ubuntu/KDE Neon)

```sh
sudo apt install build-essential cmake pkg-config qt6-base-dev \
                 qt6-webengine-dev libasound2-dev
# plus Go 1.26+ (https://go.dev/dl) — `go` must be on PATH
```

`qt6-base-dev` provides Qt6 Widgets + Concurrent; `qt6-webengine-dev` provides
Qt6 WebEngineWidgets for the embedded Deezer login (`QWebEngineView`);
`libasound2-dev` is needed at link time because the Go engine (via `oto/v3`)
links ALSA (`-lasound`). At runtime the host just needs `libasound.so.2` and the
Qt6 WebEngine runtime (`libqt6webenginewidgets6` / `qt6-qtwebengine`).

## Login

On first launch a **Log in with Deezer** dialog opens an embedded webview at the
Deezer web login; once you sign in, the app captures the `arl` session cookie
automatically and writes it to `~/.config/opendeezer/arl.txt` so the next launch
auto-logs-in — no copy/paste needed. A manual ARL field is offered as a fallback.

Login also still reads an existing ARL from `$DEEZER_ARL`, or
`~/.config/opendeezer/arl.txt` (legacy `~/.config/deezertui/arl.txt` is also
accepted) — same as the TUI. If a stored ARL is stale, the login dialog reopens.

## Architecture

```
gui/kde/
  src/main.cpp        QApplication entry point
  src/mainwindow.h    MainWindow + wire models (Track/Album/Playlist)
  src/mainwindow.cpp  sidebar, pages, transport, threading, polling, cover art
  CMakeLists.txt      Qt6 Widgets+Concurrent, links lib/libdeezercore.a
  build.sh            builds the Go archive first, then the app
  lib/                generated libdeezercore.{a,h} (git-ignored)
```

The C API is defined in `../../corelib/deezercore.go`
(`go build -buildmode=c-archive`). Every blocking `DZ*` call runs on a worker via
`QtConcurrent::run`; results are marshalled back to the GUI thread with
`QMetaObject::invokeMethod(..., Qt::QueuedConnection)`. A single 300 ms `QTimer`
polls cheap player state (position, play/pause icon) and auto-advances when
`DZFinishedCount()` increments.

## License

AGPL-3.0. By Cycl0o0.
