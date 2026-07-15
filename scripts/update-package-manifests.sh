#!/usr/bin/env bash
# Update package-manager manifests from a tagged release's checksums.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${1:-}"
SUMS_FILE="${2:-}"
SOURCE_SHA="${3:-}"
RELEASE_DATE="${4:-}"

if [[ -z "$VERSION" || -z "$SUMS_FILE" || -z "$SOURCE_SHA" || -z "$RELEASE_DATE" ]]; then
  echo "usage: $0 <version> <SHA256SUMS.txt> <source-tarball-sha256> <release-date>" >&2
  exit 2
fi
if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "invalid version: $VERSION" >&2
  exit 2
fi
if [[ ! -f "$SUMS_FILE" ]]; then
  echo "checksums file not found: $SUMS_FILE" >&2
  exit 2
fi
if [[ ! "$SOURCE_SHA" =~ ^[0-9a-f]{64}$ ]]; then
  echo "invalid source tarball checksum" >&2
  exit 2
fi
if [[ ! "$RELEASE_DATE" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
  echo "invalid release date: $RELEASE_DATE" >&2
  exit 2
fi

# Validate cross-provider release invariants before modifying any file.
FDROID="$ROOT/packaging/fdroid/fdroiddata/fr.cyclooo.opendeezer.yml"
VERSION_CODE=""
if [[ -f "$FDROID" ]]; then
  BUILD_COUNT="$(grep -c '^  - versionName:' "$FDROID" || true)"
  if [[ "$BUILD_COUNT" != 1 ]]; then
    echo "F-Droid candidate must contain exactly one build template; found $BUILD_COUNT" >&2
    exit 1
  fi
  ANDROID_VERSION="$(sed -n 's/^[[:space:]]*versionName = "\([^"]*\)".*/\1/p' "$ROOT/gui/android/app/build.gradle.kts" | head -1)"
  if [[ "$ANDROID_VERSION" != "$VERSION" ]]; then
    echo "Android versionName $ANDROID_VERSION does not match release version $VERSION" >&2
    exit 1
  fi
  VERSION_CODE="$(sed -n 's/^[[:space:]]*versionCode = \([0-9][0-9]*\).*/\1/p' "$ROOT/gui/android/app/build.gradle.kts" | head -1)"
  if [[ ! "$VERSION_CODE" =~ ^[0-9]+$ ]]; then
    echo "could not read Android versionCode" >&2
    exit 1
  fi
  FDROID_CURRENT_VERSION="$(sed -n 's/^CurrentVersion: //p' "$FDROID" | head -1)"
  FDROID_CURRENT_CODE="$(sed -n 's/^CurrentVersionCode: \([0-9][0-9]*\)$/\1/p' "$FDROID" | head -1)"
  if [[ ! "$FDROID_CURRENT_CODE" =~ ^[0-9]+$ ]]; then
    echo "could not read current F-Droid version code" >&2
    exit 1
  fi
  if (( VERSION_CODE < FDROID_CURRENT_CODE )) || \
     [[ "$VERSION" != "$FDROID_CURRENT_VERSION" && "$VERSION_CODE" -le "$FDROID_CURRENT_CODE" ]]; then
    echo "Android versionCode must increase for a new F-Droid release ($FDROID_CURRENT_CODE -> $VERSION_CODE)" >&2
    exit 1
  fi
fi

checksum() {
  local artifact="$1" value
  value="$(awk -v name="$artifact" '$2 == name { print $1 }' "$SUMS_FILE")"
  if [[ ! "$value" =~ ^[0-9a-f]{64}$ ]]; then
    echo "missing or invalid checksum for $artifact" >&2
    exit 1
  fi
  printf '%s' "$value"
}

DARWIN_ARM64="$(checksum opendeezer-tui-darwin-arm64)"
DARWIN_AMD64="$(checksum opendeezer-tui-darwin-amd64)"
LINUX_ARM64="$(checksum opendeezer-tui-linux-arm64)"
LINUX_AMD64="$(checksum opendeezer-tui-linux-amd64)"
WINDOWS_AMD64="$(checksum opendeezer-tui-windows-amd64.exe)"

FORMULA="$ROOT/packaging/homebrew/opendeezer.rb"
sed -i.bak "s/^  version \"[^\"]*\"/  version \"$VERSION\"/" "$FORMULA"
sed -i.bak "/opendeezer-tui-darwin-arm64\"/{n;s/sha256 \"[^\"]*\"/sha256 \"$DARWIN_ARM64\"/;}" "$FORMULA"
sed -i.bak "/opendeezer-tui-darwin-amd64\"/{n;s/sha256 \"[^\"]*\"/sha256 \"$DARWIN_AMD64\"/;}" "$FORMULA"
sed -i.bak "/opendeezer-tui-linux-arm64\"/{n;s/sha256 \"[^\"]*\"/sha256 \"$LINUX_ARM64\"/;}" "$FORMULA"
sed -i.bak "/opendeezer-tui-linux-amd64\"/{n;s/sha256 \"[^\"]*\"/sha256 \"$LINUX_AMD64\"/;}" "$FORMULA"
rm -f "$FORMULA.bak"

for manifest in "$ROOT"/packaging/winget/Cycl0o0.OpenDeezer*.yaml; do
  sed -i.bak "s/^PackageVersion: .*/PackageVersion: $VERSION/" "$manifest"
  rm -f "$manifest.bak"
done
INSTALLER="$ROOT/packaging/winget/Cycl0o0.OpenDeezer.installer.yaml"
LOCALE="$ROOT/packaging/winget/Cycl0o0.OpenDeezer.locale.en-US.yaml"
sed -i.bak "s#^    InstallerUrl: .*#    InstallerUrl: https://github.com/Cycl0o0/OpenDeezer/releases/download/v$VERSION/opendeezer-tui-windows-amd64.exe#" "$INSTALLER"
sed -i.bak "s/^    InstallerSha256: .*/    InstallerSha256: $WINDOWS_AMD64/" "$INSTALLER"
sed -i.bak "s/^ReleaseDate: .*/ReleaseDate: \"$RELEASE_DATE\"/" "$INSTALLER"
rm -f "$INSTALLER.bak"
sed -i.bak "s#^LicenseUrl: .*#LicenseUrl: https://github.com/Cycl0o0/OpenDeezer/blob/v$VERSION/LICENSE#" "$LOCALE"
sed -i.bak "s#^ReleaseNotesUrl: .*#ReleaseNotesUrl: https://github.com/Cycl0o0/OpenDeezer/releases/tag/v$VERSION#" "$LOCALE"
rm -f "$LOCALE.bak"

PKGBUILD="$ROOT/packaging/aur/PKGBUILD"
SRCINFO="$ROOT/packaging/aur/.SRCINFO"
sed -i.bak "s/^pkgver=.*/pkgver=$VERSION/" "$PKGBUILD"
sed -i.bak "s/^sha256sums=.*/sha256sums=('$SOURCE_SHA')/" "$PKGBUILD"
rm -f "$PKGBUILD.bak"
sed -i.bak "s/^\([[:space:]]*pkgver = \).*/\1$VERSION/" "$SRCINFO"
sed -i.bak "s#^\([[:space:]]*source = \).*#\1opendeezer-$VERSION.tar.gz::https://github.com/Cycl0o0/OpenDeezer/archive/refs/tags/v$VERSION.tar.gz#" "$SRCINFO"
sed -i.bak "s/^\([[:space:]]*sha256sums = \).*/\1$SOURCE_SHA/" "$SRCINFO"
rm -f "$SRCINFO.bak"

# Fail before opening a release PR if a provider file was not updated as expected.
grep -Fq "version \"$VERSION\"" "$FORMULA"
for sha in "$DARWIN_ARM64" "$DARWIN_AMD64" "$LINUX_ARM64" "$LINUX_AMD64"; do
  grep -Fq "sha256 \"$sha\"" "$FORMULA"
done
for manifest in "$ROOT"/packaging/winget/Cycl0o0.OpenDeezer*.yaml; do
  grep -Fq "PackageVersion: $VERSION" "$manifest"
done
grep -Fq "releases/download/v$VERSION/opendeezer-tui-windows-amd64.exe" "$INSTALLER"
grep -Fq "InstallerSha256: $WINDOWS_AMD64" "$INSTALLER"
grep -Fq "ReleaseDate: \"$RELEASE_DATE\"" "$INSTALLER"
grep -Fq "LicenseUrl: https://github.com/Cycl0o0/OpenDeezer/blob/v$VERSION/LICENSE" "$LOCALE"
grep -Fq "ReleaseNotesUrl: https://github.com/Cycl0o0/OpenDeezer/releases/tag/v$VERSION" "$LOCALE"
grep -Fq "pkgver=$VERSION" "$PKGBUILD"
grep -Fq "sha256sums=('$SOURCE_SHA')" "$PKGBUILD"
grep -Fq "pkgver = $VERSION" "$SRCINFO"
grep -Fq "source = opendeezer-$VERSION.tar.gz::https://github.com/Cycl0o0/OpenDeezer/archive/refs/tags/v$VERSION.tar.gz" "$SRCINFO"
grep -Fq "sha256sums = $SOURCE_SHA" "$SRCINFO"

SCOOP="$ROOT/packaging/scoop/opendeezer.json"
if [[ -f "$SCOOP" ]]; then
  sed -i.bak "s/^    \"version\": \"[^\"]*\"/    \"version\": \"$VERSION\"/" "$SCOOP"
  sed -i.bak "s#releases/download/v[0-9][0-9.]*/opendeezer-tui-windows-amd64\.exe#releases/download/v$VERSION/opendeezer-tui-windows-amd64.exe#" "$SCOOP"
  sed -i.bak "s/^            \"hash\": \"[0-9a-f]*\"/            \"hash\": \"$WINDOWS_AMD64\"/" "$SCOOP"
  rm -f "$SCOOP.bak"
  grep -Fq "\"version\": \"$VERSION\"" "$SCOOP"
  grep -Fq "releases/download/v$VERSION/opendeezer-tui-windows-amd64.exe" "$SCOOP"
  grep -Fq "\"hash\": \"$WINDOWS_AMD64\"" "$SCOOP"
fi

SNAP="$ROOT/packaging/snap/snapcraft.yaml"
if [[ -f "$SNAP" ]]; then
  sed -i.bak "s/^version: .*/version: \"$VERSION\"/" "$SNAP"
  rm -f "$SNAP.bak"
  grep -Fq "version: \"$VERSION\"" "$SNAP"
fi

if [[ -f "$FDROID" ]]; then
  sed -i.bak "s/^  - versionName: .*/  - versionName: $VERSION/" "$FDROID"
  sed -i.bak "s/^    versionCode: .*/    versionCode: $VERSION_CODE/" "$FDROID"
  sed -i.bak "s/^    commit: .*/    commit: v$VERSION/" "$FDROID"
  sed -i.bak "s/^CurrentVersion: .*/CurrentVersion: $VERSION/" "$FDROID"
  sed -i.bak "s/^CurrentVersionCode: .*/CurrentVersionCode: $VERSION_CODE/" "$FDROID"
  rm -f "$FDROID.bak"
  grep -Fq "  - versionName: $VERSION" "$FDROID"
  grep -Fq "    versionCode: $VERSION_CODE" "$FDROID"
  grep -Fq "    commit: v$VERSION" "$FDROID"
  grep -Fq "CurrentVersion: $VERSION" "$FDROID"
  grep -Fq "CurrentVersionCode: $VERSION_CODE" "$FDROID"
fi
