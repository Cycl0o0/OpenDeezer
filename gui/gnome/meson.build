# OpenDeezer — native GNOME (GTK4 + libadwaita) front-end.
#
# The Go engine archive (lib/libdeezercore.a + lib/libdeezercore.h) must already
# exist before configuring: build.sh builds it first, then runs meson. The
# archive is a Linux cgo c-archive that statically links the Go runtime and
# dynamically links ALSA (via ebitengine/oto), so the final link needs
# -lasound -lpthread -ldl -lm AFTER the archive (left-to-right resolution).

project('opendeezer-gnome', 'c',
  version: '1.0.0',
  meson_version: '>=0.59.0',
  default_options: ['c_std=c11', 'warning_level=1'])

deps = [
  # gtk_picture_set_content_fit is 4.8+; CSS string loader guarded for 4.12.
  dependency('gtk4', version: '>=4.10'),
  # AdwNavigationSplitView/AdwToolbarView are 1.4; AdwAboutDialog/AdwSwitchRow
  # are 1.5; AdwPreferencesDialog is 1.5 (older falls back to the window).
  dependency('libadwaita-1', version: '>=1.5'),
  dependency('json-glib-1.0'),
  # GDBus (MPRIS) ships in gio; pulled in transitively by gtk4 but make explicit.
  dependency('gio-2.0'),
  # Embedded Deezer login: the GTK4 WebKit port (header webkit/webkit.h).
  dependency('webkitgtk-6.0'),
  # The arl cookie is read back as a SoupCookie from the WebKit cookie manager
  # (libsoup-3.0 is pulled in by webkitgtk-6.0; declared here for its symbols).
  dependency('libsoup-3.0'),
]

# Embedded resources (app icon) compiled into the binary.
gnome = import('gnome')
resources = gnome.compile_resources('opendeezer-resources',
  'data/opendeezer.gresource.xml', source_dir: 'data')

# Prebuilt Go engine archive + its generated header.
core_archive = meson.current_source_dir() / 'lib' / 'libdeezercore.a'
dzcore = declare_dependency(
  link_args: [core_archive, '-lasound', '-lpthread', '-ldl', '-lm'],
  include_directories: include_directories('lib'),
)

executable('opendeezer-gnome',
  'src/main.c', 'src/standalone.c', resources,
  dependencies: deps + [dzcore],
  install: true)

# GTK backend for the unified Linux client (gui/linux launcher dlopen's this).
shared_library('opendeezer-gtk',
  'src/main.c', resources,
  dependencies: deps + [dzcore],
  install: true)

install_data('data/org.opendeezer.OpenDeezer.desktop',
  install_dir: get_option('datadir') / 'applications')

# App icon (matches the .desktop Icon= name so the shell + window pick it up).
install_data('data/opendeezer.png',
  install_dir: get_option('datadir') / 'icons/hicolor/512x512/apps',
  rename: 'org.opendeezer.OpenDeezer.png')
