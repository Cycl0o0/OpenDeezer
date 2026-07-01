#!/usr/bin/env bash
#
# Builds the OpenDeezer Android app:
#   1. binds the Go engine (./mobile) into an Android AAR with gomobile, and
#   2. assembles the debug APK with Gradle.
#
# Requirements: Go 1.24+, JDK 17, the Android SDK + NDK (ANDROID_NDK_HOME / a
# local.properties pointing at the SDK), and an internet connection (the Gradle
# wrapper downloads Gradle 8.7 on first run).
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

echo "==> Installing gomobile/gobind (if missing)"
go install golang.org/x/mobile/cmd/gomobile@latest
go install golang.org/x/mobile/cmd/gobind@latest
export PATH="$PATH:$(go env GOPATH)/bin"

echo "==> gomobile init"
gomobile init

echo "==> Binding ./mobile -> app/libs/odmobile.aar"
mkdir -p "$SCRIPT_DIR/app/libs"
( cd "$REPO_ROOT" && gomobile bind -target=android -androidapi 24 \
    -o gui/android/app/libs/odmobile.aar ./mobile )

echo "==> Assembling debug APKs (phone + Android TV flavors)"
( cd "$SCRIPT_DIR" && ./gradlew --no-daemon assembleMobileDebug assembleTvDebug )

echo "==> Done. APKs at:"
echo "    $SCRIPT_DIR/app/build/outputs/apk/mobile/debug/app-mobile-debug.apk  (phone/tablet)"
echo "    $SCRIPT_DIR/app/build/outputs/apk/tv/debug/app-tv-debug.apk          (Android TV)"
