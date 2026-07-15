# Android release signing

Android refuses to install an unsigned APK, and it refuses to *upgrade* an app
whose new APK is signed by a different key than the installed one. So a release
build must be signed with a **stable release keystore** you keep forever. Unlike
macOS/Windows there is no CA or notarization — you self-sign; the keystore *is*
the identity.

The `android` job in `.github/workflows/release.yml` builds **release-signed**
universal and per-ABI APKs. It fails closed when any signing secret is absent,
so a tagged release can never accidentally publish a debug-signed APK.

## The keystore

A stable PKCS12 keystore (alias `opendeezer`) is configured in the repository's
GitHub Actions secrets. GitHub secrets are write-only deployment copies and do
**not** count as a recoverable backup.

> ⚠️ **Never lose this keystore.** If it's gone you can never ship a compatible
> update — every existing install would have to be uninstalled and reinstalled
> (losing its data). Keep at least two encrypted, tested copies in separate
> locations, with one offline, and store the password separately. Never commit
> the keystore or its password. Periodically confirm that a backup can be opened
> and that its certificate SHA-256 is
> `e5c114ba5ef3482b5a441485923835c510c724013f4116c9a44808b347dfa230`.

To regenerate an equivalent keystore (only if starting fresh — a new key breaks
upgrades for existing users):

```sh
# JDK keytool:
keytool -genkeypair -v -keystore opendeezer-release.p12 -storetype PKCS12 \
  -alias opendeezer -keyalg RSA -keysize 4096 -validity 12000 \
  -dname "CN=Youssef Chabb, O=OpenDeezer, C=FR"
# or, without a working JVM, openssl:
openssl req -x509 -newkey rsa:4096 -keyout k.pem -out c.pem -days 12000 -nodes \
  -subj "/CN=Youssef Chabb/O=OpenDeezer/C=FR"
openssl pkcs12 -export -inkey k.pem -in c.pem -name opendeezer -out opendeezer-release.p12
```

## F-Droid APK

The regular Android release is built explicitly with
`-PupstreamUpdates=true`, so installs from GitHub keep the in-app update check.
The F-Droid binaries are built with `-PupstreamUpdates=false`, because F-Droid
owns updates for that installation. There is one APK per ABI:

- `OpenDeezer-android-fdroid-<version>-<versionCode>.apk`
- `armeabi-v7a`: base Android version code × 10 + 1
- `arm64-v8a`: base Android version code × 10 + 2
- `x86`: base Android version code × 10 + 3
- `x86_64`: base Android version code × 10 + 4

For version `3.1.4` (base code `26`), the four APKs therefore use codes
`261` through `264`. Normal phone and TV GitHub builds use the same mapping;
their universal APKs use the `× 10` code. A later GitHub release can therefore
still upgrade an installation that originated from F-Droid.

`.github/workflows/fdroid-reproducible.yml` builds all four unsigned F-Droid
variants at F-Droid's canonical source path, signs them with APK Signature
Schemes v2/v3, and reconstructs each result with `apksigcopier` before
publication. The workflow is manual by design. Its `source_ref` must be the full
commit hash reviewed in fdroiddata, and the main GitHub release must already
contain `SHA256SUMS.txt`.

## GitHub repository secrets

The job requires and checks all four values before it starts the release build.

| Secret | Value |
| --- | --- |
| `ANDROID_KEYSTORE_BASE64` | base64 of the `.p12`: `base64 -i opendeezer-release.p12` |
| `ANDROID_KEYSTORE_PASSWORD` | the keystore password |
| `ANDROID_KEY_ALIAS` | `opendeezer` |
| `ANDROID_KEY_PASSWORD` | same password (PKCS12 uses one password for store + key) |

`gui/android/app/build.gradle.kts` reads these via environment variables set by
the workflow. CI produces a universal APK plus `armeabi-v7a`, `arm64-v8a`, `x86`
and `x86_64` APKs for both the mobile and TV flavors, all signed with the same
release key. The stable release names are `OpenDeezer-android*.apk` and
`OpenDeezer-androidtv*.apk`.

## Notes

- **Local builds**: `./gradlew assembleMobileRelease` on a dev machine with no
  keystore env produces an *unsigned* release APK (won't install) — use the debug
  variant locally, or export the four env vars to sign.
- **Play Protect**: sideloaded APKs from outside the Play Store still show an
  "unknown source" warning on install. Release-signing fixes installability,
  upgrades and integrity — it does **not** remove that Play Protect prompt (only
  Play Store distribution does, which a Deezer downloader can't use).
- **Verify** an APK's signer: `apksigner verify --print-certs app-mobile-release.apk`.
