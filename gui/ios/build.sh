#!/usr/bin/env bash
# Builds the OpenDeezer iOS app for the Simulator:
#   1. binds the Go engine (mobile/) into Odmobile.xcframework via gomobile
#   2. generates OpenDeezer.xcodeproj from project.yml via xcodegen
#   3. builds the app with xcodebuild (no code signing, simulator only)
#
# Usage: gui/ios/build.sh [--skip-bind] [--skip-gen]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
IOS_DIR="$ROOT/gui/ios"
export PATH="$PATH:$(go env GOPATH)/bin"

SKIP_BIND=0
SKIP_GEN=0
for arg in "$@"; do
  case "$arg" in
    --skip-bind) SKIP_BIND=1 ;;
    --skip-gen) SKIP_GEN=1 ;;
  esac
done

if [[ "$SKIP_BIND" -eq 0 ]]; then
  echo "==> binding Go engine -> Odmobile.xcframework"
  cd "$ROOT"
  command -v gomobile >/dev/null || go install golang.org/x/mobile/cmd/gomobile@latest
  command -v gobind >/dev/null || go install golang.org/x/mobile/cmd/gobind@latest
  gomobile init
  rm -rf "$IOS_DIR/Odmobile.xcframework"
  gomobile bind -target=ios -o "$IOS_DIR/Odmobile.xcframework" ./mobile
fi

if [[ "$SKIP_GEN" -eq 0 ]]; then
  echo "==> xcodegen generate"
  cd "$IOS_DIR"
  xcodegen generate
fi

echo "==> xcodebuild (iphonesimulator)"
cd "$IOS_DIR"
xcodebuild -project OpenDeezer.xcodeproj -scheme OpenDeezer -sdk iphonesimulator \
  -destination 'generic/platform=iOS Simulator' -derivedDataPath build \
  CODE_SIGNING_ALLOWED=NO build
