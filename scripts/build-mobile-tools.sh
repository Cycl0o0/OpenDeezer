#!/usr/bin/env bash
# Build gomobile and gobind from the golang.org/x/mobile version pinned by
# go.mod. Keeping both binaries together is important: gomobile locates gobind
# through PATH when generating Android/iOS bindings.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${1:-}"
if [[ -z "$OUT" ]]; then
  echo "usage: $0 <output-directory>" >&2
  exit 2
fi

mkdir -p "$OUT"
cd "$ROOT"
go build -mod=readonly -trimpath -o "$OUT/gomobile" golang.org/x/mobile/cmd/gomobile
go build -mod=readonly -trimpath -o "$OUT/gobind" golang.org/x/mobile/cmd/gobind
