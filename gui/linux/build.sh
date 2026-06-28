#!/usr/bin/env bash
# Build the unified Linux client: both toolkit backends as shared libraries plus
# the dlopen launcher, assembled into dist/. Run dist/opendeezer — it picks the
# Qt backend on KDE-family desktops and the GTK backend elsewhere.
#
# Deps: Go 1.24+, gcc, meson+ninja, GTK4/libadwaita/json-glib dev, Qt6 dev,
#       libasound2-dev. (Same as the two standalone GUIs combined.)
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"   # repo root: module github.com/Cycl0o0/OpenDeezer
DIST="$HERE/dist"
rm -rf "$DIST"; mkdir -p "$DIST"

echo "==> [1/5] Go engine c-archive"
mkdir -p "$ROOT/gui/gnome/lib" "$ROOT/gui/kde/lib"
( cd "$ROOT" && CGO_ENABLED=1 go build -buildmode=c-archive \
    -o gui/gnome/lib/libdeezercore.a ./corelib )
cp "$ROOT/gui/gnome/lib/libdeezercore.a" "$ROOT/gui/kde/lib/libdeezercore.a"
cp "$ROOT/gui/gnome/lib/libdeezercore.h" "$ROOT/gui/kde/lib/libdeezercore.h"

echo "==> [2/5] GTK backend (libopendeezer-gtk.so)"
( cd "$ROOT/gui/gnome"
  if [ -d build ]; then meson setup --reconfigure build .; else meson setup build .; fi
  meson compile -C build )
cp "$ROOT/gui/gnome/build/libopendeezer-gtk.so" "$DIST/"

echo "==> [3/5] Qt backend (libopendeezer-qt.so)"
( cd "$ROOT/gui/kde"
  cmake -S . -B build -DCMAKE_BUILD_TYPE=Release >/dev/null
  cmake --build build -j "$(nproc 2>/dev/null || echo 2)" --target opendeezer-qt opendeezer-login )
cp "$ROOT/gui/kde/build/libopendeezer-qt.so" "$DIST/"
# Standalone login webview helper — the launcher/app spawns it (QtWebEngine
# out-of-process so it works despite the backend being dlopen'd).
cp "$ROOT/gui/kde/build/opendeezer-login" "$DIST/"

echo "==> [4/5] launcher"
cc -O2 -o "$DIST/opendeezer" "$HERE/launcher.c" -ldl -Wl,-rpath,'$ORIGIN'

echo "==> [5/5] desktop entry + icon"
cp "$HERE/org.opendeezer.OpenDeezer.desktop" "$DIST/"
cp "$ROOT/assets/icon.png" "$DIST/opendeezer.png"

echo "==> done -> $DIST  (run: $DIST/opendeezer)"
