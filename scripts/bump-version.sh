#!/usr/bin/env bash
# bump-version.sh — bump the OpenDeezer release number EVERYWHERE in one shot.
#
#   scripts/bump-version.sh            # minor bump: 1.7.0 -> 1.8.0
#   scripts/bump-version.sh patch      # 1.7.0 -> 1.7.1
#   scripts/bump-version.sh major      # 1.7.0 -> 2.0.0
#   scripts/bump-version.sh 2.1.3      # explicit target
#
# Single source of truth is internal/version/version.go (all Go surfaces —
# TUI, corelib, gomobile, MCP — read it). This script updates that plus every
# per-client build file and packaging manifest, and increments the platform
# build numbers (CFBundleVersion, versionCode, CURRENT_PROJECT_VERSION).
#
# It is idempotent: re-running with the same target only fixes stragglers.
# NOTE: winget's `ManifestVersion:` is the manifest SCHEMA version — never
# touched here (only PackageVersion / URLs are).
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION_GO=internal/version/version.go
CUR=$(sed -n 's/^const Number = "\(.*\)"$/\1/p' "$VERSION_GO")
[[ -n "$CUR" ]] || { echo "cannot read current version from $VERSION_GO" >&2; exit 1; }

IFS=. read -r MAJ MIN PAT <<<"$CUR"
case "${1:-minor}" in
  major) NEW="$((MAJ + 1)).0.0" ;;
  minor) NEW="$MAJ.$((MIN + 1)).0" ;;
  patch) NEW="$MAJ.$MIN.$((PAT + 1))" ;;
  *)     NEW="$1"
         [[ "$NEW" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "bad version: $NEW" >&2; exit 1; } ;;
esac
echo "bump: $CUR -> $NEW"

# sub FILE SED_EXPR — apply in place, warn if the file doesn't exist.
sub() {
  local f=$1; shift
  [[ -f "$f" ]] || { echo "  ! missing: $f (skipped)" >&2; return 0; }
  sed -i '' "$@" "$f"
  echo "  · $f"
}

# bumpnum FILE SED_CAPTURE — increment the first captured integer.
bumpnum() {
  local f=$1 pat=$2
  [[ -f "$f" ]] || { echo "  ! missing: $f (skipped)" >&2; return 0; }
  perl -0pi -e "s/$pat/\$1 . (\$2 + 1)/e" "$f"
  echo "  · $f (build number +1)"
}

# ---- single source of truth (all Go binaries) ----
sub "$VERSION_GO" "s/^const Number = \".*\"/const Number = \"$NEW\"/"

# ---- macOS ----
# CFBundleShortVersionString = NEW; CFBundleVersion += 1.
perl -0pi -e "s/(<key>CFBundleShortVersionString<\/key>\s*<string>)[^<]+/\${1}$NEW/" gui/macos/Info.plist
bumpnum gui/macos/Info.plist '(<key>CFBundleVersion<\/key>\s*<string>)(\d+)'
echo "  · gui/macos/Info.plist"

# ---- GNOME ----
sub gui/gnome/meson.build "s/version: '[0-9.]*'/version: '$NEW'/"
sub gui/gnome/src/main.c "s/\"$CUR\"/\"$NEW\"/g"

# ---- KDE ----
sub gui/kde/src/mainwindow.cpp "s/OpenDeezer $CUR/OpenDeezer $NEW/g"

# ---- Windows ----
sub gui/windows/MainWindow.xaml.cs "s/OpenDeezer $CUR/OpenDeezer $NEW/g"
# Anchor on the assemblyIdentity name, not the current version: if the manifest
# ever falls out of sync (it froze at 1.6.0.0 through several releases because the
# old "$CUR.0" pattern silently matched nothing), a value-agnostic sed still fixes it.
sub gui/windows/app.manifest "s/\(name=\"OpenDeezer.app\" version=\"\)[0-9.]*\"/\1$NEW.0\"/"

# ---- Android (phone + TV share the module) ----
sub gui/android/app/build.gradle.kts "s/versionName = \"[0-9.]*\"/versionName = \"$NEW\"/"
bumpnum gui/android/app/build.gradle.kts '(versionCode = )(\d+)'

# ---- iOS (project.yml + the checked-in pbxproj) ----
sub gui/ios/project.yml "s/MARKETING_VERSION: \"[0-9.]*\"/MARKETING_VERSION: \"$NEW\"/"
bumpnum gui/ios/project.yml '(CURRENT_PROJECT_VERSION: ")(\d+)'
sub gui/ios/OpenDeezer.xcodeproj/project.pbxproj "s/MARKETING_VERSION = [0-9.]*;/MARKETING_VERSION = $NEW;/g"
perl -0pi -e 's/(CURRENT_PROJECT_VERSION = )(\d+)(;)/$1 . ($2 + 1) . $3/ge' gui/ios/OpenDeezer.xcodeproj/project.pbxproj
echo "  · gui/ios/OpenDeezer.xcodeproj/project.pbxproj"

# ---- packaging manifests (checksums are filled at release time) ----
sub packaging/aur/PKGBUILD "s/^pkgver=.*/pkgver=$NEW/"
sub packaging/aur/.SRCINFO -e "s/pkgver = .*/pkgver = $NEW/" -e "s|/v$CUR/|/v$NEW/|g" -e "s/opendeezer-$CUR/opendeezer-$NEW/g"
sub packaging/homebrew/opendeezer.rb "s/$CUR/$NEW/g"
for f in packaging/winget/*.yaml; do
  # PackageVersion + tag URLs only — ManifestVersion is the SCHEMA version.
  sub "$f" -e "s/^PackageVersion: .*/PackageVersion: $NEW/" -e "s|/v$CUR/|/v$NEW/|g" -e "s/ReleaseNotesUrl: \(.*\)v$CUR/ReleaseNotesUrl: \1v$NEW/"
done
for f in packaging/flatpak/*.yaml; do
  sub "$f" "s/tag: v[0-9.]*/tag: v$NEW/"
done

# ---- verify: nothing still carries the old version where it matters ----
echo
echo "leftover occurrences of $CUR (excluding CHANGELOG/history — review manually):"
grep -rn --exclude-dir=.git --exclude=CHANGELOG.md --exclude-dir=scripts -F "$CUR" \
  internal/version gui/macos/Info.plist gui/gnome/meson.build gui/kde/src/mainwindow.cpp \
  gui/windows/app.manifest gui/android/app/build.gradle.kts gui/ios/project.yml \
  packaging 2>/dev/null || echo "  (none)"
echo
echo "done: $NEW — remember to add a CHANGELOG.md entry and run: go build ./..."
