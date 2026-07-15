# F-Droid packaging

This directory contains a **candidate** build recipe for the phone/tablet
Android application (`fr.cyclooo.opendeezer`) and points to the repository's
canonical store text. It has not yet been accepted into the main F-Droid
repository.

## Layout

- `../../fastlane/metadata/android/en-US/` is the canonical upstream store
  metadata in the layout F-Droid imports.
- `fdroiddata/fr.cyclooo.opendeezer.yml` is a proposal to copy to
  `metadata/fr.cyclooo.opendeezer.yml` in an `fdroiddata` checkout.

The recipe builds `odmobile.aar` from the reviewed Go source; it never downloads
or bundles the generated AAR. It pins Go through the `go` srclib, derives the
exact `golang.org/x/mobile` version from `go.mod`, pins NDK r26d, and assembles
four unsigned `mobileRelease` APKs for F-Droid to verify and sign. Each build
selects exactly one ABI and has its own ordered version code. The Android TV
flavor has a different application ID (`fr.cyclooo.opendeezer.tv`) and needs a
separate submission after the phone recipe is proven on the official build
infrastructure.

## Before submitting

1. Keep all four build blocks on the same full source commit hash. That commit
   must contain the `fdroidAbi` Gradle support and must match the source used by
   the reproducible-binary workflow.
2. Add an icon and real phone screenshots under
   `fastlane/metadata/android/en-US/images/`.
3. Confirm that the OpenDeezer name and artwork do not infringe Deezer's
   trademark or other third-party rights. F-Droid reviews this explicitly.
4. Run the candidate in an `fdroiddata` checkout:

   ```sh
   cp packaging/fdroid/fdroiddata/fr.cyclooo.opendeezer.yml \
     /path/to/fdroiddata/metadata/
   fdroid lint fr.cyclooo.opendeezer
   for code in 261 262 263 264; do
     fdroid build --test "fr.cyclooo.opendeezer:$code"
   done
   ```

5. Test login, streaming, foreground playback, downloads and logout on a clean
   device. The build server does not need Deezer credentials; credentials are
   only needed for this functional test.
6. Submit the metadata to the
   [F-Droid Data repository](https://gitlab.com/fdroid/fdroiddata) and disclose
   `NonFreeNet`, `TetheredNet`, and `Tracking` anti-features. Playback events
   are reported to Deezer for listening and free-tier accounting.

## Known blockers and review notes

- Gomobile records its absolute local-module replacement in Go's build-info
  section. The upstream reproducible workflow therefore builds at F-Droid's
  canonical `/home/vagrant/build/fr.cyclooo.opendeezer` path. Keep that path and
  the source commit identical when comparing the four binaries.
- The tagged release currently requires Go 1.25.12. The recipe builds it from
  F-Droid's `go` srclib, using Debian's Go only as a bootstrap compiler. This is
  slower than a conventional Android build and has not yet been exercised on an
  official F-Droid worker.
- The app contains no Google Play Services, Firebase, proprietary analytics or
  proprietary advertising SDK dependency. AndroidX/Compose and Coil are fetched
  from trusted Maven repositories by Gradle.
- OpenDeezer necessarily contacts Deezer's proprietary service and requires a
  Deezer account, and reports playback events to Deezer. Those behaviors require
  anti-feature disclosure; they do not by themselves prevent a source build.
- The F-Droid recipe disables both the automatic GitHub update check and the
  user-triggered Settings action through a compile-time Gradle property. GitHub
  builds retain both paths.
- The candidate uses upstream reproducible binaries and pins the upstream
  signing certificate through `AllowedAPKSigningKeys`. Do not publish or change
  those binaries after F-Droid has reviewed them.
- The v3.1.3 tag stores its ARL session credential in plain SharedPreferences.
  The current source encrypts it with Android Keystore and excludes the
  preference file from backup/device transfer; publish those fixes in a new tag
  before submitting the recipe.

IzzyOnDroid is a separate third-party repository that indexes upstream APKs. It
requires an explicit onboarding request and policy review; publishing a GitHub
release does not guarantee automatic indexing.
