# Obtainium

OpenDeezer can be installed and updated directly from its GitHub releases with
[Obtainium](https://github.com/ImranR98/Obtainium). Choose the configuration for
the device you are using:

- `opendeezer-mobile.json` installs the phone/tablet app
  (`fr.cyclooo.opendeezer`).
- `opendeezer-tv.json` installs the Android TV app
  (`fr.cyclooo.opendeezer.tv`).

In Obtainium, open **Add App**, choose **Obtainium Import**, and select the
downloaded JSON file. The configurations accept only the corresponding
OpenDeezer APK names; phone and TV builds cannot be mixed up.

Each configuration enables Obtainium's architecture filter. Releases containing
split APKs therefore select the first ABI supported by the device from:

- `arm64-v8a`
- `armeabi-v7a`
- `x86_64`
- `x86`

The universal `OpenDeezer-android.apk` or `OpenDeezer-androidtv.apk` remains a
fallback for releases that do not contain split APKs.

## Publishing in the Obtainium app catalog

These files work without catalog submission. For one-click discovery on
[apps.obtainium.imranr.dev](https://apps.obtainium.imranr.dev), convert the
desired import entry to the catalog format and open a pull request against
[`ImranR98/apps.obtainium.imranr.dev`](https://github.com/ImranR98/apps.obtainium.imranr.dev).
That repository's contribution script accepts an Obtainium export as input.
