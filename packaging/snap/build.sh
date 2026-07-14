#!/usr/bin/env bash
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
OUT="$HERE/dist"
IMAGE="ghcr.io/canonical/snapcraft@sha256:0443273552768a3230c2ede3aa47e567da0242bfbb0a7bb1283093208c404a0c"
SNAPCRAFT_COMMIT="4a76630698678f9617ccc137a4e6ff93e4eb3e89"
VERSION="$(sed -n 's/^version: "\([^"]*\)"/\1/p' "$HERE/snapcraft.yaml")"
SOURCE_REF="v$VERSION"

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required to run the isolated Snapcraft build" >&2
  exit 1
fi
if ! docker info >/dev/null 2>&1; then
  echo "the Docker daemon must be running to build the snap" >&2
  exit 1
fi
if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required to fetch the pinned Snapcraft extension files" >&2
  exit 1
fi
if [[ -z "$VERSION" ]] || ! git -C "$ROOT" rev-parse --verify --quiet "$SOURCE_REF^{commit}" >/dev/null; then
  echo "snap version must identify an existing source tag (missing $SOURCE_REF)" >&2
  exit 1
fi

WORK="$(mktemp -d "${TMPDIR:-/tmp}/opendeezer-snap.XXXXXX")"
VOLUME="opendeezer-snap-build-$$-$RANDOM"
PUBLISH_TMP=""
cleanup() {
  docker volume rm --force "$VOLUME" >/dev/null 2>&1 || true
  [[ -z "$PUBLISH_TMP" ]] || rm -f -- "$PUBLISH_TMP"
  rm -rf "$WORK"
}
trap cleanup EXIT

mkdir -p "$WORK/project/snap" "$WORK/output" "$OUT"
git -C "$ROOT" archive --format=tar "$SOURCE_REF" | tar -xf - -C "$WORK/project"

# Include the working recipe while keeping application source pinned to the tag
# named by its version. A dirty checkout can never silently alter shipped code.
cp "$HERE/snapcraft.yaml" "$WORK/project/snap/snapcraft.yaml"

# Canonical's 8/core24 OCI image omits these extension resources even though
# Snapcraft expands the GNOME extension to reference them. Supply the exact
# files from the matching Snapcraft 8.11.1 commit.
mkdir -p "$WORK/command-chain"
for file in Makefile desktop-launch gpu-2404-wrapper hooks-configure-fonts run; do
  curl --fail --silent --show-error --location \
    "https://raw.githubusercontent.com/canonical/snapcraft/$SNAPCRAFT_COMMIT/extensions/desktop/command-chain/$file" \
    --output "$WORK/command-chain/$file"
done

# Keep Snapcraft's parts tree on a native Linux filesystem. Building directly
# on a macOS Docker bind mount makes Craft follow Debian documentation symlinks
# while packages are being merged and can fail with a spurious missing-file
# error. Only the source import and final snap cross the bind-mount boundary.
docker volume create "$VOLUME" >/dev/null
docker run --rm \
  --platform linux/amd64 \
  --entrypoint /bin/sh \
  -v "$WORK/project:/source:ro" \
  -v "$VOLUME:/project" \
  "$IMAGE" -c 'cp -a /source/. /project/'

docker run --rm \
  --platform linux/amd64 \
  -v "$VOLUME:/project" \
  -v "$WORK/command-chain:/usr/share/snapcraft/extensions/desktop/command-chain:ro" \
  "$IMAGE" pack

docker run --rm \
  --platform linux/amd64 \
  --entrypoint /bin/sh \
  -v "$VOLUME:/project:ro" \
  -v "$WORK/output:/out" \
  "$IMAGE" -c 'set -eu
    found=0
    for snap in /project/*.snap; do
      [ -f "$snap" ] || continue
      cp "$snap" /out/
      found=1
    done
    [ "$found" -eq 1 ]'

# Stage the container-owned result before touching the last successful output.
# Exactly one artifact is expected because this helper builds one host platform.
shopt -s nullglob
staged_snaps=("$WORK/output"/*.snap)
shopt -u nullglob
if [[ "${#staged_snaps[@]}" -ne 1 ]] || [[ ! -f "${staged_snaps[0]:-}" ]] || [[ -L "${staged_snaps[0]:-}" ]]; then
  echo "Snapcraft must produce exactly one regular .snap artifact" >&2
  exit 1
fi

published_name="$(basename "${staged_snaps[0]}")"
PUBLISH_TMP="$OUT/.${published_name}.tmp.$$"
install -m 0644 "${staged_snaps[0]}" "$PUBLISH_TMP"

if [[ -e "$OUT/$published_name" ]] && [[ ! -f "$OUT/$published_name" ]] && [[ ! -L "$OUT/$published_name" ]]; then
  echo "refusing to replace non-file output: $OUT/$published_name" >&2
  exit 1
fi

# Replace stale regular files and symlinks only after the fresh artifact has
# been staged successfully. The final rename is atomic within dist/.
find "$OUT" -mindepth 1 -maxdepth 1 -name '*.snap' ! -name "$published_name" \( -type f -o -type l \) -delete
mv -f "$PUBLISH_TMP" "$OUT/$published_name"
PUBLISH_TMP=""

if [[ ! -f "$OUT/$published_name" ]] || [[ -L "$OUT/$published_name" ]]; then
  echo "the freshly built snap was not published to $OUT" >&2
  exit 1
fi

echo "Snap written to $OUT"
