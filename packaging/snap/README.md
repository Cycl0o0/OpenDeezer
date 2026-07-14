# Snap package

This directory contains a strict-confinement Snapcraft recipe for the native
GTK4/libadwaita client. It deliberately packages one complete frontend rather
than the unified launcher: the latter requires both the GNOME and KDE runtime
stacks in a single snap.

## Build

From the repository root:

```sh
packaging/snap/build.sh
```

The build uses a pinned Canonical Snapcraft 8/core24 OCI image and writes the
result to `packaging/snap/dist/`. Docker (with its daemon running) and `curl`
must be available. The recipe downloads a checksum-pinned Go 1.25.12 toolchain
for `amd64` or `arm64` and removes it from the final snap. This local OCI build
produces an `amd64` snap; use native Snapcraft or Launchpad remote builds for
`arm64`. Application source is archived from the `v<version>` tag named in
`snapcraft.yaml`; the build fails if that tag does not exist, so the store
version cannot accidentally package a different checkout.

For a native Snapcraft installation, copy `snapcraft.yaml` to
`snap/snapcraft.yaml` at the repository root and run `snapcraft pack`.

## Test before publishing

On an Ubuntu host with snapd:

```sh
sudo snap install --dangerous packaging/snap/dist/opendeezer_*.snap
snap connections opendeezer
opendeezer
```

Verify login, audio playback, a download to a user-selected home directory,
Wayland and X11 startup, and media-key/MPRIS control. Configuration and the ARL
credential are kept in
`$SNAP_USER_COMMON/.config/opendeezer`, which is private to the snap and
survives revisions.

Publishing requires the project maintainer to register the `opendeezer` name,
upload the built snap, and complete the Snap Store review. The `mpris` slot is
not auto-connected by default, so request an auto-connection from the store if
media-key integration should work without a manual connection.
