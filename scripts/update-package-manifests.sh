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
  FDROID_COMMIT="$(git -C "$ROOT" rev-parse HEAD^{commit})"
  if [[ ! "$FDROID_COMMIT" =~ ^[0-9a-f]{40}$ ]]; then
    echo "could not determine the full F-Droid source commit" >&2
    exit 1
  fi
  BUILD_COUNT="$(grep -c '^  - versionName:' "$FDROID" || true)"
  if [[ "$BUILD_COUNT" != 4 ]]; then
    echo "F-Droid candidate must contain exactly four ABI build templates; found $BUILD_COUNT" >&2
    exit 1
  fi
  for field in versionCode commit; do
    FIELD_COUNT="$(grep -c "^    $field:" "$FDROID" || true)"
    if [[ "$FIELD_COUNT" != 4 ]]; then
      echo "F-Droid candidate must contain exactly four $field fields; found $FIELD_COUNT" >&2
      exit 1
    fi
  done
  if [[ "$(grep -Ec '^    commit: [0-9a-f]{40}$' "$FDROID" || true)" != 4 ]]; then
    echo "F-Droid source commits must all be full 40-character hashes" >&2
    exit 1
  fi
  EXPECTED_ABIS=$'armeabi-v7a\narm64-v8a\nx86\nx86_64'
  FDROID_ABIS="$(sed -n 's/^      - fdroidAbi=//p' "$FDROID")"
  if [[ "$FDROID_ABIS" != "$EXPECTED_ABIS" ]]; then
    echo "F-Droid ABI build templates are missing, duplicated, or out of order" >&2
    exit 1
  fi
  EXPECTED_VERCODE_OPERATIONS=$'  - 10 * %c + 1\n  - 10 * %c + 2\n  - 10 * %c + 3\n  - 10 * %c + 4'
  FDROID_VERCODE_OPERATIONS="$(awk '
    /^VercodeOperation:$/ { capture=1; next }
    capture && /^  - / { print; next }
    capture { exit }
  ' "$FDROID")"
  if [[ "$FDROID_VERCODE_OPERATIONS" != "$EXPECTED_VERCODE_OPERATIONS" ]]; then
    echo "F-Droid VercodeOperation does not match the ABI version-code scheme" >&2
    exit 1
  fi
  EXPECTED_BINARIES='Binaries: https://github.com/Cycl0o0/OpenDeezer/releases/download/v%v/OpenDeezer-android-fdroid-%v-%c.apk'
  if [[ "$(grep -Fxc "$EXPECTED_BINARIES" "$FDROID" || true)" != 1 ]]; then
    echo "F-Droid Binaries URL does not include the ABI version code" >&2
    exit 1
  fi
  if grep -Eq '^[[:space:]]+output:' "$FDROID"; then
    echo "F-Droid ABI build templates must not define output" >&2
    exit 1
  fi
  for field in CurrentVersion CurrentVersionCode; do
    FIELD_COUNT="$(grep -c "^$field:" "$FDROID" || true)"
    if [[ "$FIELD_COUNT" != 1 ]]; then
      echo "F-Droid candidate must contain exactly one $field field; found $FIELD_COUNT" >&2
      exit 1
    fi
  done
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
  if (( VERSION_CODE < 1 || VERSION_CODE > 209999999 )); then
    echo "Android base versionCode is outside the ABI-safe range" >&2
    exit 1
  fi
  FDROID_VERSION_CODE_MAX=$((VERSION_CODE * 10 + 4))
  FDROID_CURRENT_VERSION="$(sed -n 's/^CurrentVersion: //p' "$FDROID" | head -1)"
  FDROID_CURRENT_CODE="$(sed -n 's/^CurrentVersionCode: \([0-9][0-9]*\)$/\1/p' "$FDROID" | head -1)"
  if [[ ! "$FDROID_CURRENT_CODE" =~ ^[0-9]+$ ]]; then
    echo "could not read current F-Droid version code" >&2
    exit 1
  fi
  if (( FDROID_VERSION_CODE_MAX < FDROID_CURRENT_CODE )) || \
     [[ "$VERSION" != "$FDROID_CURRENT_VERSION" && "$FDROID_VERSION_CODE_MAX" -le "$FDROID_CURRENT_CODE" ]]; then
    echo "Android ABI versionCodes must increase for a new F-Droid release ($FDROID_CURRENT_CODE -> $FDROID_VERSION_CODE_MAX)" >&2
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
  FDROID_TMP="$(mktemp "${TMPDIR:-/tmp}/opendeezer-fdroid.XXXXXX")"
  trap 'if [[ -n "${FDROID_TMP:-}" ]]; then rm -f "$FDROID_TMP"; fi' EXIT
  cp -p "$FDROID" "$FDROID_TMP"
  awk -v version="$VERSION" -v base="$VERSION_CODE" -v commit="$FDROID_COMMIT" '
    /^  - versionName: / {
      names++
      print "  - versionName: " version
      next
    }
    /^    versionCode: / {
      codes++
      if (codes > 4) exit 2
      print "    versionCode: " (base * 10 + codes)
      next
    }
    /^    commit: / {
      commits++
      print "    commit: " commit
      next
    }
    /^CurrentVersion: / {
      current_versions++
      print "CurrentVersion: " version
      next
    }
    /^CurrentVersionCode: / {
      current_codes++
      print "CurrentVersionCode: " (base * 10 + 4)
      next
    }
    { print }
    END {
      if (names != 4 || codes != 4 || commits != 4 ||
          current_versions != 1 || current_codes != 1) exit 3
    }
  ' "$FDROID" > "$FDROID_TMP"
  [[ "$(grep -Fc "  - versionName: $VERSION" "$FDROID_TMP")" -eq 4 ]]
  [[ "$(grep -Fc "    commit: $FDROID_COMMIT" "$FDROID_TMP")" -eq 4 ]]
  for offset in 1 2 3 4; do
    grep -Fq "    versionCode: $((VERSION_CODE * 10 + offset))" "$FDROID_TMP"
  done
  grep -Fq "CurrentVersion: $VERSION" "$FDROID_TMP"
  grep -Fq "CurrentVersionCode: $FDROID_VERSION_CODE_MAX" "$FDROID_TMP"
  mv "$FDROID_TMP" "$FDROID"
  FDROID_TMP=""
fi
