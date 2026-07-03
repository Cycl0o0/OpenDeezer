# macOS code signing & notarization

Gatekeeper blocks every downloaded macOS build that is not **notarized** with an
Apple **Developer ID** identity — the "OpenDeezer can't be opened because Apple
cannot check it for malicious software" / "is damaged" dialogs. This is the
one-time setup that makes the `.app` open with no prompt and no quarantine.

The release workflow (`.github/workflows/release.yml`, `macos-app` job) already
does the signing, notarization and stapling. It only activates when the secrets
below are present, so nothing here changes the build for forks or PRs — a
secret-less run still produces the same unsigned zip as before.

## One-time: Apple Developer account + Developer ID certificate

1. Enrol in the **Apple Developer Program** (https://developer.apple.com,
   $99/yr). Note your **Team ID** (Membership page, e.g. `AB12CD34EF`).
2. Create a **Developer ID Application** certificate: Xcode → Settings →
   Accounts → your team → *Manage Certificates* → **+** → *Developer ID
   Application*. (Or via the CSR flow on the developer portal.)
3. Export it as a `.p12`: **Keychain Access → My Certificates**, right-click the
   *Developer ID Application: …* entry (the one with a private key) → *Export* →
   `.p12`, set an export password.
4. Get the identity's full name — you'll need the exact string:
   ```
   security find-identity -v -p codesigning
   # -> "Developer ID Application: Your Name (AB12CD34EF)"
   ```

## One-time: App Store Connect API key (for notarytool)

An API key is cleaner than an Apple-ID + app-specific-password in CI.

1. https://appstoreconnect.apple.com → **Users and Access → Integrations →
   App Store Connect API** → **+** to generate a key. Role **Developer** is
   enough for notarization.
2. Download the `AuthKey_XXXXXXXXXX.p8` (**one-time download**). Record the
   **Key ID** (`XXXXXXXXXX`) and the **Issuer ID** (UUID at the top of the page).

## GitHub repository secrets

Settings → Secrets and variables → Actions → **New repository secret**. Add all
six (the `macos-app` job checks for `MACOS_CERTIFICATE_P12` + `APPLE_API_KEY_P8`
to decide whether to sign):

| Secret | Value |
| --- | --- |
| `MACOS_CERTIFICATE_P12` | base64 of the `.p12`: `base64 -i cert.p12 \| pbcopy` |
| `MACOS_CERTIFICATE_PASSWORD` | the `.p12` export password from step 3 |
| `MACOS_SIGN_IDENTITY` | the full identity string, e.g. `Developer ID Application: Your Name (AB12CD34EF)` |
| `APPLE_API_KEY_ID` | the API **Key ID** |
| `APPLE_API_ISSUER_ID` | the API **Issuer ID** |
| `APPLE_API_KEY_P8` | base64 of the key: `base64 -i AuthKey_XXXXXXXXXX.p8 \| pbcopy` |

Both files are base64-encoded so the multi-line PEM/binary survives the secret
store intact. Nothing else is required — cut the next tag and the release `.app`
is signed, notarized and stapled automatically.

## Verifying a release

After downloading and unzipping a signed release:

```sh
spctl -a -vvv --type exec OpenDeezer.app     # -> "accepted, source=Notarized Developer ID"
codesign --verify --deep --strict --verbose=2 OpenDeezer.app
xcrun stapler validate OpenDeezer.app        # -> "The validate action worked!"
```

## Notes & troubleshooting

- **Hardened runtime is required to notarize** — the workflow signs with
  `--options runtime`. The app is *not* App-Sandboxed (Developer ID distribution
  outside the Mac App Store does not require it), so outbound/LAN networking and
  CoreAudio need **no entitlements**. If a future dependency trips a hardened-
  runtime check at launch, add a `gui/macos/OpenDeezer.entitlements` file (e.g.
  `com.apple.security.cs.allow-unsigned-executable-memory`) and pass it to the
  bundle `codesign` with `--entitlements`.
- **`stapler` can only staple `.app`/`.dmg`/`.pkg`**, not a bare CLI binary. The
  TUI (`opendeezer-tui-darwin-*`) is best installed via Homebrew, which strips
  quarantine on install; notarizing the CLI would mean shipping it inside a
  notarized zip/pkg instead of a raw binary (a follow-up if needed).
- **Local dry run** of the whole chain:
  ```sh
  cd gui/macos && make app
  codesign --force --timestamp --options runtime --sign "$IDENTITY" OpenDeezer.app/Contents/MacOS/OpenDeezer
  codesign --force --timestamp --options runtime --sign "$IDENTITY" OpenDeezer.app
  ditto -c -k --keepParent OpenDeezer.app OpenDeezer.zip
  xcrun notarytool submit OpenDeezer.zip --key AuthKey.p8 --key-id "$KID" --issuer "$ISS" --wait
  xcrun stapler staple OpenDeezer.app
  ```
- A notarization rejection prints a submission id; read the log with
  `xcrun notarytool log <id> --key … --key-id … --issuer …`.
