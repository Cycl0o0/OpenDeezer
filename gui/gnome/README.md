# OpenDeezer (GNOME)

Native GNOME front-end for OpenDeezer — a GTK4 + libadwaita desktop client with a
Deezer-purple accent. The whole engine (login, browse, Blowfish decrypt, MP3
decode, ALSA playback) is the Go core compiled to a C static archive
(`lib/libdeezercore.a`) and linked in-process; this directory is UI only.

## Look & layout

- **AdwNavigationSplitView:** a `navigation-sidebar` listing **Liked Songs** and
  your **playlists**, beside a `GtkColumnView` track table (Title · Artist ·
  Album · Time).
- **Now-playing bar** pinned to the bottom: cover art, title/artist,
  prev/play-pause/next, a seek scrubber, position/duration, and a volume slider.
- **Adaptive:** as the window narrows, the now-playing bar stacks into two, then
  three rows and the sidebar folds into its own page (back button in the
  header), so the window shrinks to 360 × 294 — see `setup_adaptive_layout`.
- **Search** lives in the content header bar — press Enter to search Deezer.
- **Theme:** Deezer "Electric Violet" `#A238FF` accent via a `GtkCssProvider`
  (libadwaita 1.6 accent variables with a pre-1.6 named-color fallback), forced
  dark color scheme.
- **About** (menu button, top-left) credits **Cycl0o0**, AGPL-3.0.

All blocking engine calls (login, browse, play, cover fetch) run on `GTask`
worker threads; cheap player state is polled from a 300 ms `g_timeout` that also
drives the scrubber and auto-advances when a track ends.

## Build & run

```sh
cd gui/gnome
chmod +x build.sh        # first time only
./build.sh               # builds the Go archive, then meson + ninja
./opendeezer-gnome
```

`build.sh` always rebuilds `lib/libdeezercore.a` from `../../corelib` first, so
the app and the engine never drift.

### Dependencies (Debian/Ubuntu)

```sh
sudo apt install \
  golang gcc pkg-config meson ninja-build \
  libgtk-4-dev libadwaita-1-dev libjson-glib-dev libasound2-dev \
  libwebkitgtk-6.0-dev
```

`libasound2-dev` is required at build/link time (the engine's `oto/v3` audio
backend hard-links ALSA via `#cgo pkg-config: alsa`); at runtime the host needs
`libasound.so.2`. The Go core uses pure-Go crypto/HTTP, so **no** libssl/libcurl
is needed.

`libwebkitgtk-6.0-dev` provides the GTK4 WebKit port (`webkitgtk-6.0`) used by the
embedded **Log in with Deezer** webview; it also pulls in `libsoup-3.0-dev`,
whose `SoupCookie` API reads the `arl` cookie back out of the WebView.

### ARL / login

On first launch (no stored ARL) the app opens a **Log in to Deezer** window with
a **Log in with Deezer** button that embeds the real Deezer web login
(WebKitGTK). After you sign in, the `arl` cookie is captured automatically, used
to start the session, and written to `~/.config/opendeezer/arl.txt` so the next
launch auto-logs-in — no manual ARL needed. A manual-ARL paste box stays on the
same screen as a fallback. The ARL is still read from `$DEEZER_ARL` first, then
`~/.config/opendeezer/arl.txt`, at startup.

The login page runs inside WebKitGTK's bubblewrap sandbox when the system allows
it. Where unprivileged user namespaces are restricted (Ubuntu 24.04+ through
AppArmor, some containers), bwrap can't start and WebKit would abort the app, so
the client checks for this at startup and then shows the login page without the
sandbox, logging a one-line `WebKit sandbox unavailable` message. Setting
`WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS` yourself skips that check.

## Files

```
src/main.c   the whole app (DzTrack model, sidebar, column view, player bar,
             async browse/play/cover, polling, About, theme)
meson.build  links lib/libdeezercore.a + -lasound -lpthread -ldl -lm
build.sh     builds the Go archive, then configures + compiles with meson
data/        .desktop entry
lib/         generated libdeezercore.{a,h} (git-ignored, produced by build.sh)
```

The C API is defined in `../../corelib/deezercore.go`
(`go build -buildmode=c-archive`).
