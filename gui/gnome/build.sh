#!/usr/bin/env bash
# Build the GNOME front-end end-to-end:
#   1. compile the Go engine to a C static archive (lib/libdeezercore.{a,h})
#   2. configure + compile the GTK4/libadwaita app with meson + ninja
#   3. drop the binary next to this script as ./opendeezer-gnome
#
# Usage:  cd gui/gnome && ./build.sh && ./opendeezer-gnome
#
# Build deps (Debian/Ubuntu):
#   golang gcc pkg-config meson ninja-build gettext \
#   libgtk-4-dev libadwaita-1-dev libjson-glib-dev libasound2-dev \
#   libwebkitgtk-6.0-dev    # embedded Deezer login (pulls in libsoup-3.0-dev)
# (gettext provides msgfmt/xgettext, required by meson's i18n module for the
#  po/ message catalogs and the localized .desktop file.)
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"   # repo root: module github.com/Cycl0o0/OpenDeezer

echo "==> [1/3] building Go core archive (corelib -> lib/libdeezercore.a)"
mkdir -p "$HERE/lib"
( cd "$ROOT" && CGO_ENABLED=1 go build -buildmode=c-archive \
    -o gui/gnome/lib/libdeezercore.a ./corelib )

echo "==> [2/3] configuring meson"
if [ ! -d "$HERE/build" ]; then
  meson setup "$HERE/build" "$HERE"
else
  meson setup --reconfigure "$HERE/build" "$HERE"
fi

echo "==> [3/3] compiling"
meson compile -C "$HERE/build"

cp "$HERE/build/opendeezer-gnome" "$HERE/opendeezer-gnome"
echo "==> done: $HERE/opendeezer-gnome"
