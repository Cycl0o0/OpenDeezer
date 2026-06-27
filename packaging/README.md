# Packaging

Distribution manifests for OpenDeezer. After tagging a release, fill in the real
SHA256 checksums (the release `checksums` job publishes `SHA256SUMS.txt`).

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

## Flatpak (`flatpak/org.opendeezer.OpenDeezer.yaml`)
GNOME (GTK4) client; the GNOME runtime provides WebKitGTK for the login view.
Local build:
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
under `manifests/c/Cycl0o0/OpenDeezer/0.4.0/` (e.g. via `wingetcreate submit`).
```sh
winget install Cycl0o0.OpenDeezer
```
