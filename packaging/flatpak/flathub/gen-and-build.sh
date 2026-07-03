#!/usr/bin/env bash
# gen-and-build.sh — finalize + test-build the OpenDeezer Flathub package on Linux
# (e.g. your OrbStack Ubuntu machine — flatpak-builder does NOT run on macOS).
#
# It (1) installs flatpak + the KDE runtime/SDK + Go SDK extension + appstream
# tools, (2) generates the offline Go module sources (go.mod.yml + vendor/),
# (3) validates the MetaInfo, (4) builds the flatpak and installs it so you can
# `flatpak run org.opendeezer.OpenDeezer.unified`.
#
# Run from the repo root:  packaging/flatpak/flathub/gen-and-build.sh
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
APP=org.opendeezer.OpenDeezer.unified
cd "$ROOT"

echo "==> [1/5] system deps (apt) + flathub remote"
sudo apt-get update
sudo apt-get install -y flatpak flatpak-builder appstream golang-go git build-essential
flatpak remote-add --if-not-exists --user flathub https://flathub.org/repo/flathub.flatpakrepo
# KDE 6.8 is built on freedesktop 24.08 -> the golang extension is //24.08.
flatpak install -y --user flathub \
  org.kde.Platform//6.8 org.kde.Sdk//6.8 org.freedesktop.Sdk.Extension.golang//24.08

echo "==> [2/5] generate offline Go module sources (go.mod.yml + vendor/)"
# flatpak-go-mod reads go.mod/go.sum, downloads each module to hash it, and writes
# go.mod.yml (a flatpak sources list) plus a vendor/ tree the build uses offline.
go install github.com/dennwc/flatpak-go-mod@latest
"$(go env GOPATH)/bin/flatpak-go-mod" "$ROOT"
# flatpak-go-mod drops go.mod.yml in CWD; keep it beside the manifest.
mv -f go.mod.yml "$HERE/go.mod.yml"
echo "    wrote $HERE/go.mod.yml ($(grep -c 'type: archive' "$HERE/go.mod.yml" || echo '?') modules)"

echo "==> [3/5] validate MetaInfo"
appstreamcli validate --explain "$HERE/$APP.metainfo.xml" || {
  echo "!! metainfo validation failed — fix before submitting"; exit 1; }

echo "==> [4/5] flatpak-builder (offline: --disable-download proves no build-net)"
flatpak-builder --user --install --force-clean --disable-download \
  "$HERE/build" "$HERE/$APP.yaml"

echo "==> [5/5] done. Test it:"
echo "    flatpak run $APP"
echo
echo "If the build reached the network (error under --disable-download), the go.mod.yml"
echo "is incomplete — re-run step 2 after 'go mod download all', or vendor manually."
