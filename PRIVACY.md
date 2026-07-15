# OpenDeezer Privacy Policy

Effective date: July 15, 2026

This policy applies to OpenDeezer applications and command-line tools published
from the official OpenDeezer repository. OpenDeezer is an independent,
open-source project and is not affiliated with or endorsed by Deezer.

## Summary

OpenDeezer does not operate an account service or analytics backend. The project
maintainers do not receive your Deezer credential, library, listening history,
searches, downloads, or playback activity through OpenDeezer. The application
processes this information on your device and communicates directly with the
services and devices described below.

OpenDeezer contains no advertising SDK, behavioral analytics, or automatic
crash-reporting SDK, and the project maintainers do not sell personal data.

## Information handled by the application

### Deezer session and account data

OpenDeezer uses your Deezer `arl` session cookie as a credential. Depending on
the client, you provide it directly or OpenDeezer captures it after you sign in
through an embedded Deezer web page. The credential is stored in the
application's local configuration directory so that you remain signed in. It is
sent to Deezer when needed to authenticate requests; it is not sent to the
OpenDeezer maintainers.

To provide its features, OpenDeezer requests and processes Deezer account,
library, search, playlist, playback, entitlement, and media information. Actions
such as editing a playlist, liking a track, or reporting playback are sent
directly to Deezer. Deezer's own terms and privacy policy apply to information
processed by Deezer and to the embedded sign-in page.

### Information stored locally

OpenDeezer may store the following on your device:

- your Deezer session credential;
- application preferences and optional remote-control credentials;
- listening history, logs, artwork, and an encrypted media cache;
- tracks or episodes that you explicitly download; and
- local device-discovery and connection settings.

These files remain on your device unless you place them in a synchronized
folder, back them up, or share them yourself. Desktop and terminal clients store
configuration under the platform's application-data or configuration directory,
commonly `~/.config/opendeezer` on Unix-like systems and
`%APPDATA%\opendeezer` on Windows.

### Update checks

OpenDeezer checks the public GitHub Releases API for new versions. This request
does not include your Deezer credential or library data. GitHub receives the
ordinary connection information associated with an HTTPS request, such as your
IP address and user agent, under GitHub's privacy policy.

### Optional integrations and local-network features

- If you configure Discord Rich Presence, OpenDeezer sends now-playing details
  such as track title, artist, playback state, and timestamps to the Discord
  client running on your device. Discord then processes that information under
  its own privacy policy.
- If you enable OpenDeezer Connect, the web remote, or LAN control, playback
  state and commands can be exchanged with devices on your local network. The
  feature may use an account proof, pairing code, or locally configured access
  token. It is disabled or restricted to the local device unless you enable
  broader access.
- Your operating system, app store, package manager, or distribution platform
  may independently collect download, installation, or crash information under
  its own settings and privacy policy. OpenDeezer does not automatically send
  that information to the project maintainers.

## Retention and deletion

The OpenDeezer maintainers do not retain the application data described above
because they do not receive it. Local data remains until you clear it in the
application, sign out where that option is available, delete the OpenDeezer
configuration/application-data directory, and separately remove any media you
downloaded outside that directory. You can revoke the stored session by signing
out of Deezer sessions or changing the relevant Deezer account credentials.

Uninstalling a package may not remove configuration, caches, or downloaded
media; remove those files manually if you want them deleted.

## Security

Treat the Deezer `arl` value and OpenDeezer remote-control tokens like
passwords. OpenDeezer restricts sensitive configuration files with local file
permissions where the platform supports it, but anyone who can access your user
account or its files may be able to read them. Do not post credentials in bug
reports, screenshots, or logs.

## Changes and contact

Material changes to this policy will be published in this file with a revised
effective date. For privacy or security reports involving credentials or other
sensitive information, email `security@cyclooo.fr`. GitHub issues are public, so
never include a session cookie, password, access token, or other private
information in an issue.

For non-security questions, email `contact@cyclooo.fr` or open a GitHub issue.
