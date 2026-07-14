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
  TOOLS_DIR="$(mktemp -d "${TMPDIR:-/tmp}/opendeezer-mobile-tools.XXXXXX")"
  trap 'rm -rf "$TOOLS_DIR"' EXIT
  "$ROOT/scripts/build-mobile-tools.sh" "$TOOLS_DIR"
  export PATH="$TOOLS_DIR:$PATH"
  # Some engine doc comments contain "/*" (e.g. control-API route globs like
  # "/play/mix/*"). gomobile embeds Go doc comments verbatim into the generated
  # ObjC header's block comments, so a nested "/*" trips clang's -Wcomment; and
  # gomobile's objc cgo template compiles the gobind package with -Werror (it
  # also *overwrites* CGO_CFLAGS, so an env override can't reach that build).
  # Demote just that one warning in the template so the c-archive build passes.
  # The module-cache dir is read-only, so patch the file in place with cp
  # (needs only the *file* writable, not the dir) rather than sed -i (renames).
  for f in "$(go env GOMODCACHE)"/golang.org/x/mobile@*/bind/objc/seq_darwin.go.support; do
    [[ -f "$f" ]] || continue
    if grep -q -- '-Werror' "$f" && ! grep -q -- '-Wno-error=comment' "$f"; then
      chmod u+w "$f"
      sed 's/-fblocks -Werror/-fblocks -Werror -Wno-error=comment/' "$f" > "$f.patched"
      cp "$f.patched" "$f" && rm -f "$f.patched"
    fi
  done
  rm -rf "$IOS_DIR/Odmobile.xcframework"
  gomobile bind -trimpath -target=ios -o "$IOS_DIR/Odmobile.xcframework" ./mobile
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
