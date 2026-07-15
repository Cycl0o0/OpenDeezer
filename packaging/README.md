# Distribution packaging

These files prepare OpenDeezer for several distribution providers. They do not
mean the application is already listed: public catalogs still require their
maintainers' accounts, reviews, and policy decisions.

| Provider | Repository support | Publication status |
| --- | --- | --- |
| F-Droid | Four ABI source-build recipes, reproducible binaries, and Fastlane text | Under fdroiddata review; publish all four immutable binaries before updating its metadata |
| Obtainium | Phone and TV import configurations | Ready for direct GitHub Releases use; catalog entry optional |
| Snap Store | Strictly confined GNOME recipe | `amd64` pack validated; register the name, test on Ubuntu, then upload |
| Homebrew | macOS/Linux TUI formula | Checksum-pinned; publish it to `Cycl0o0/homebrew-tap` |
| WinGet | Windows TUI manifests, schema 1.12 | Checksum-pinned; submit to `microsoft/winget-pkgs` |
| AUR | Source-building `PKGBUILD` and `.SRCINFO` | Checksum-pinned; publish through the AUR account |
| Scoop | Windows TUI manifest | Direct manifest install supported; a bucket makes it discoverable |
| Flathub | Existing local Flatpak drafts only | Blocked; do not submit the current files |
| IzzyOnDroid | Uses the signed GitHub APKs | Deferred pending its current inclusion-policy review |

## Release automation

The `publish-manifests` release job runs
`scripts/update-package-manifests.sh`. It updates versions and real checksums in
Homebrew, WinGet, AUR, Scoop, Snap, and all four F-Droid ABI build blocks,
preserves the result as a workflow artifact, and tries to open
`release/manifests-<tag>`. PR creation is best-effort because a repository may
disable PR creation by `GITHUB_TOKEN`; checksum or manifest validation failures
still fail the job.

After merging that PR, the remaining external actions are:

- copy the formula to `Cycl0o0/homebrew-tap`;
- push the AUR files to the `opendeezer` AUR repository;
- submit the WinGet directory with `wingetcreate` or a pull request;
- publish the Scoop manifest in a maintained bucket;
- build, test, register, and upload the Snap;
- update and submit the F-Droid recipe after the next Android tag.

## Android: F-Droid and Obtainium

[`fdroid/`](fdroid/) contains a mainline F-Droid proposal that builds the Go AAR
and one APK per supported ABI from source. It declares the `NonFreeNet` and
`TetheredNet` anti-features because a Deezer account and proprietary Deezer
service are required. Canonical listing text is under
`../fastlane/metadata/android/`.
The F-Droid guide records the remaining signing, screenshot, reproducibility,
and trademark review work.

[`obtainium/`](obtainium/) contains import files for the phone/tablet and TV
application IDs. Obtainium needs no central listing: it can follow the signed,
per-ABI APKs on GitHub Releases directly.

IzzyOnDroid is separate from F-Droid and does not index every GitHub release
automatically. Its current inclusion policy requires a separate request and
review, so it is intentionally not submitted here.

## Linux: Snap and local Flatpak

[`snap/`](snap/) packages the GNOME client with strict confinement. Its guide
covers local build/install tests and the human Snap Store publication step.

The manifests under [`flatpak/`](flatpak/) remain useful for local Flatpak
builds. They are not a Flathub submission. Current
[Flathub requirements](https://docs.flathub.org/docs/for-app-authors/requirements)
restrict AI-assisted applications and submission material and also impose
third-party trademark rules. A human maintainer must obtain policy and naming
clearance before replacing and submitting the stale `flatpak/flathub/` draft.

## Terminal package managers

- [`homebrew/opendeezer.rb`](homebrew/opendeezer.rb) installs the appropriate
  macOS or Linux TUI release binary. After tap publication:
  `brew install Cycl0o0/tap/opendeezer`.
- [`winget/`](winget/) installs the portable 64-bit Windows TUI. After community
  repository acceptance: `winget install Cycl0o0.OpenDeezer`.
- [`aur/`](aur/) builds the TUI from tagged source. After AUR publication:
  `yay -S opendeezer`.
- [`scoop/opendeezer.json`](scoop/opendeezer.json) installs the 64-bit Windows
  TUI and carries a checksum-aware autoupdate template. See its README for the
  direct-manifest command and bucket publication.

Commercial mobile and desktop stores are not prepared here. Apple, Google,
Microsoft Store, and Amazon submissions need developer accounts, platform
signing/provisioning, and—most importantly—clear permission to use Deezer's
service and branding.
