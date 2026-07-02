# Compiles every po/<lang>.po into <lang>/LC_MESSAGES/opendeezer-gnome.mo and
# installs it under <localedir>. The "glib" preset wires up the standard GLib
# gettext keywords (_, N_, C_, NC_, g_dngettext, …) for the POT-update target.
i18n.gettext('opendeezer-gnome',
  preset: 'glib',
  args: [
    '--from-code=UTF-8',
    '--add-comments=TRANSLATORS:',
    '--package-name=opendeezer-gnome',
  ])
