# Packaging

Distribution manifests for OpenDeezer. After tagging a release, fill in the real
SHA256 checksums (the release `checksums` job publishes `SHA256SUMS.txt`).

The `publish-manifests` job in release.yml automatically:
- downloads the sums,
- updates versions + checksum placeholders in homebrew/, winget/, aur/,
- opens a PR on this repo (branch: `release/manifests-<tag>`) via peter-evans/create-pull-request.

**Follow-ups (no secrets configured here):**
- Merge the PR, then manually (or via separate CI) push the updated homebrew formula to `Cycl0o0/homebrew-tap`.
- `updpkgsums` + push the PKGBUILD/.SRCINFO to the AUR `opendeezer` repo.
- Submit the winget manifests to microsoft/winget-pkgs (e.g. via wingetcreate or PR).

## Homebrew (`homebrew/opendeezer.rb`)
Tap-installable TUI formula (downloads the per-OS release binary). Update
`version` and the four `sha256` values, then publish to a tap
(`Cycl0o0/homebrew-tap`):
```sh
brew install Cycl0o0/tap/opendeezer
```

## AUR (`aur/PKGBUILD`, `aur/.SRCINFO`)
Builds the TUI from the tagged source (needs `go` + `alsa-lib`). Update `pkgver`,
run `updpkgsums` to fill `sha256sums`, regenerate `.SRCINFO`
(`makepkg --printsrcinfo > .SRCINFO`), then push to the AUR repo `opendeezer`.
```sh
yay -S opendeezer
```

## Flatpak (`flatpak/*.yaml`)
Three variants:
- `org.opendeezer.OpenDeezer.yaml` — GNOME (GTK4) client, GNOME runtime (WebKitGTK).
- `org.opendeezer.OpenDeezer.kde.yaml` — KDE (Qt6) client, KDE runtime (QtWebEngine).
- `org.opendeezer.OpenDeezer.unified.yaml` — unified dlopen launcher on the KDE
  runtime (Qt backend in-sandbox; GTK desktops use the GNOME variant).

Local build (pick a manifest):
```sh
flatpak-builder --user --install --force-clean build packaging/flatpak/org.opendeezer.OpenDeezer.yaml
```
For **Flathub**, the build sandbox has no network: vendor the Go modules and
remove the `--share=network` build-arg — generate a sources manifest with
[flatpak-builder-tools](https://github.com/flatpak/flatpak-builder-tools)
(`flatpak-go-mod`).

## winget (`winget/Cycl0o0.OpenDeezer.*.yaml`)
Portable TUI exe. After release, set `InstallerSha256`, then submit the three
manifests to [microsoft/winget-pkgs](https://github.com/microsoft/winget-pkgs)
under `manifests/c/Cycl0o0/OpenDeezer/<version>/` (e.g. via `wingetcreate submit`).
```sh
winget install Cycl0o0.OpenDeezer
```

## F-Droid / IzzyOnDroid (`packaging/fdroid/`)

Fastlane-style metadata for the Android client (phone + Android TV flavors):

```
packaging/fdroid/metadata/en-US/
  title.txt
  short_description.txt
  full_description.txt
```

**IzzyOnDroid route (recommended for F-Droid users):**
- IzzyOnDroid (https://apt.izzysoft.de/fdroid/) pulls signed release APKs directly from the GitHub Releases page (see the `android` job in `.github/workflows/release.yml`).
- After a tag, the signed `OpenDeezer-android.apk` (and TV) are attached; IzzyOnDroid indexes them.
- No manual submission step beyond tagging a release that includes the signed APKs.

**Why not mainline F-Droid:**
- OpenDeezer is a client for a proprietary streaming service (requires a Deezer account and authenticated requests to Deezer's closed APIs).
- Full source rebuilds in F-Droid's build server environment would not be able to exercise the streaming/download paths without real credentials, defeating the purpose of reproducible builds for the media functionality.
- The Android app is therefore distributed via GitHub releases (and IzzyOnDroid mirror) rather than the main F-Droid repository.

Local fdroid build metadata lives here only for the IzzyOnDroid / self-hosting use-case. The actual APKs are produced in CI.
