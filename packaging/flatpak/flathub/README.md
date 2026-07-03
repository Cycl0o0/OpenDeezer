# Flathub submission — OpenDeezer (unified Qt/KDE build)

This directory holds the files needed to publish OpenDeezer on Flathub. The
submitted app is the **unified** Linux client (`org.opendeezer.OpenDeezer.unified`,
Qt6/KDE runtime, GTK desktops run it fine too).

## Files

| File | Purpose |
| --- | --- |
| `org.opendeezer.OpenDeezer.unified.yaml` | the Flathub manifest (offline build, pinned commit) |
| `org.opendeezer.OpenDeezer.unified.metainfo.xml` | required AppStream metadata |
| `gen-and-build.sh` | Linux helper: generate `go.mod.yml`, validate, build + install |
| `go.mod.yml` | **generated** offline Go module sources (run the helper) |

The desktop file (`packaging/flatpak/org.opendeezer.OpenDeezer.unified.desktop`)
and icon (`assets/icon.png`) already exist in the repo and are installed by the
manifest.

## Before you submit — three TODOs

1. **Screenshots.** Flathub requires ≥1 hosted screenshot. Capture the app, host
   the PNGs (e.g. `https://opendeezer.org/screenshots/desktop-home.png`) and make
   sure the URLs in the metainfo load over HTTPS. Aspect ratio 1:1 to 2:1.
2. **Generate + test the build on Linux** (flatpak-builder does not run on macOS —
   use your OrbStack Ubuntu box):
   ```sh
   packaging/flatpak/flathub/gen-and-build.sh
   flatpak run org.opendeezer.OpenDeezer.unified
   ```
   This writes `go.mod.yml`, validates the metainfo, and builds **offline**
   (`--disable-download`) to prove no build-time network — a hard Flathub rule.
   Commit the generated `go.mod.yml`.
3. **Bump the pinned `commit:`** in the manifest to whatever tag you submit.

## Submitting

Flathub app-IDs must be domain-verifiable. `org.opendeezer.OpenDeezer.unified`
maps to `opendeezer.org` (which you own) and the metainfo `<url type="homepage">`
points there — so verification passes.

1. Fork <https://github.com/flathub/flathub> and create a branch off the
   **`new-pr`** branch.
2. Add the manifest, metainfo and `go.mod.yml` (and any patches) to the branch.
3. Open a PR **against `new-pr`**. The Flathub build bot compiles it; a reviewer
   checks it. Address feedback.
4. On approval Flathub creates a dedicated repo `flathub/org.opendeezer.OpenDeezer.unified`
   you get push access to — pushes there trigger the official builds.

## ⚠️ Content-policy caveat

Flathub reviewers may reject apps that facilitate ToS/copyright violations. A
Deezer stream/download client is a plausible rejection on those grounds — the
packaging here is technically complete, but approval is not guaranteed.
