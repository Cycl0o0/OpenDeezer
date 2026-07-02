#!/usr/bin/env bash
# Build OpenDeezer and install it on a USB-connected iPhone, signed with YOUR
# Apple ID. (CI only produces an UNSIGNED .ipa — iOS refuses to install that;
# an app must be signed by you before a device will run it.)
#
# Usage:
#   gui/ios/install-device.sh <TEAM_ID> [--no-multicast]
#
#   TEAM_ID       Your 10-character Apple Team ID. Find it in Xcode ▸ Settings ▸
#                 Accounts (select your Apple ID → the team row shows the id in
#                 parentheses), or at developer.apple.com ▸ Membership. A FREE
#                 Apple ID has one too.
#
#   --no-multicast  Strip the OpenDeezer Connect multicast entitlement before
#                 signing. REQUIRED for a free Apple ID: Apple grants the
#                 "Multicast Networking" capability only to PAID Developer
#                 accounts, so signing fails with it present. Cost: Connect can't
#                 auto-discover devices on the LAN (you can still add them by IP);
#                 everything else — playback, EQ, podcasts — works.
#
# Prerequisites: Xcode + command-line tools, xcodegen (brew install xcodegen),
# Go (for the gomobile bind), and the iPhone plugged in and trusted. First run:
# open Xcode ▸ Settings ▸ Accounts and add your Apple ID once so automatic
# signing can mint a provisioning profile.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
IOS_DIR="$ROOT/gui/ios"
export PATH="$PATH:$(go env GOPATH)/bin"

TEAM="${1:-}"
if [[ -z "$TEAM" || "$TEAM" == --* ]]; then
  echo "error: pass your Apple Team ID as the first argument." >&2
  echo "usage: gui/ios/install-device.sh <TEAM_ID> [--no-multicast]" >&2
  exit 1
fi
NO_MULTICAST=0
[[ "${2:-}" == "--no-multicast" ]] && NO_MULTICAST=1

echo "==> binding Go engine -> Odmobile.xcframework"
cd "$ROOT"
command -v gomobile >/dev/null || go install golang.org/x/mobile/cmd/gomobile@latest
command -v gobind >/dev/null || go install golang.org/x/mobile/cmd/gobind@latest
gomobile init
rm -rf "$IOS_DIR/Odmobile.xcframework"
gomobile bind -target=ios -o "$IOS_DIR/Odmobile.xcframework" ./mobile

echo "==> xcodegen generate"
cd "$IOS_DIR"
xcodegen generate

# For a free Apple ID, sign against an entitlements file without the paid-only
# multicast key. The real Resources/OpenDeezer.entitlements is left untouched.
ENTITLEMENTS_OVERRIDE=()
if [[ "$NO_MULTICAST" -eq 1 ]]; then
  TMP_ENT="$IOS_DIR/build/OpenDeezer-free.entitlements"
  mkdir -p "$IOS_DIR/build"
  cat >"$TMP_ENT" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict/></plist>
PLIST
  ENTITLEMENTS_OVERRIDE=("CODE_SIGN_ENTITLEMENTS=$TMP_ENT")
  echo "==> free-account mode: signing without the multicast entitlement"
fi

echo "==> building + signing for device (Team $TEAM)"
xcodebuild -project OpenDeezer.xcodeproj -scheme OpenDeezer \
  -destination 'generic/platform=iOS' -configuration Release -derivedDataPath build \
  -allowProvisioningUpdates \
  DEVELOPMENT_TEAM="$TEAM" CODE_SIGN_STYLE=Automatic \
  "${ENTITLEMENTS_OVERRIDE[@]}" build

APP="$IOS_DIR/build/Build/Products/Release-iphoneos/OpenDeezer.app"
[[ -d "$APP" ]] || { echo "error: build produced no app at $APP" >&2; exit 1; }

# Install to the first connected device. devicectl ships with Xcode 15+.
echo "==> locating connected iPhone"
UDID="$(xcrun devicectl list devices 2>/dev/null | awk '/connected/ && /iPhone/ {print $(NF-1); exit}')"
if [[ -z "$UDID" ]]; then
  echo "No connected iPhone found via devicectl. Plug in + trust the phone, or" >&2
  echo "open OpenDeezer.xcodeproj in Xcode, pick your iPhone, and press Run." >&2
  echo "The signed app is ready at: $APP" >&2
  exit 1
fi
echo "==> installing to $UDID"
xcrun devicectl device install app --device "$UDID" "$APP"
echo "Done. If the app won't open, trust the developer profile on the phone:"
echo "  Settings ▸ General ▸ VPN & Device Management ▸ (your Apple ID) ▸ Trust."
