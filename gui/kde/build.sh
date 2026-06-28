#!/usr/bin/env sh
# Build the OpenDeezer KDE app end-to-end:
#   1. compile the Go engine to a C static archive (lib/libdeezercore.{a,h})
#   2. configure + build the Qt6 app with CMake
#   3. drop the binary at gui/kde/opendeezer-kde
#
# Usage:  cd gui/kde && ./build.sh && ./opendeezer-kde
set -eu

HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/../.." && pwd)   # repo root: module github.com/Cycl0o0/OpenDeezer

mkdir -p "$HERE/lib"

echo "==> building Go c-archive (lib/libdeezercore.a)"
# CGO is required: oto/v3 links ALSA, so libasound2-dev must be installed.
( cd "$ROOT" && CGO_ENABLED=1 go build -buildmode=c-archive \
    -o "gui/kde/lib/libdeezercore.a" ./corelib )

echo "==> configuring (cmake)"
cmake -S "$HERE" -B "$HERE/build" -DCMAKE_BUILD_TYPE=Release

echo "==> compiling (cmake --build)"
cmake --build "$HERE/build" -j "$(nproc 2>/dev/null || echo 2)"

cp "$HERE/build/opendeezer-kde" "$HERE/opendeezer-kde"
cp "$HERE/build/opendeezer-login" "$HERE/opendeezer-login"   # standalone login webview helper
echo "==> done -> $HERE/opendeezer-kde (+ opendeezer-login)"
