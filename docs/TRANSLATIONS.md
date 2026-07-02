# Translations

OpenDeezer is localized natively on every client — there is no shared runtime
translation layer. The TUI and the phone web remote live in the Go engine; each
GUI uses its platform's own string mechanism. So adding or fixing a language means
touching one set of files per client, all in the same well-defined places.

The product currently ships in seven languages:

| Language | TUI / web | macOS `.lproj` | Windows `.resw` | GNOME `.po` | KDE `.ts` | Android `res/` | iOS `.xcstrings` |
|---|---|---|---|---|---|---|---|
| English | `en` | `en` | `en-US` | *(source)* | *(source)* | `values` | *(source, `en`)* |
| 简体中文 | `zh` | `zh-Hans` | `zh-CN` | `zh_CN` | `zh_CN` | `values-zh-rCN` | `zh-Hans` |
| हिन्दी | `hi` | `hi` | `hi-IN` | `hi` | `hi` | `values-hi` | `hi` |
| Español | `es` | `es` | `es-ES` | `es` | `es` | `values-es` | `es` |
| Français | `fr` | `fr` | `fr-FR` | `fr` | `fr` | `values-fr` | `fr` |
| العربية | `ar` | `ar` | `ar-SA` | `ar` | `ar` | `values-ar` | `ar` |
| Русский | `ru` | `ru` | `ru-RU` | `ru` | `ru` | `values-ru` | `ru` |

Each client falls back to English for any string a language is missing, and picks
the language from the OS/system setting (with a per-app override in the GUIs and a
🌐 Language menu / `LANG` in the TUI). Use each platform's own locale identifier —
they differ (`zh` vs `zh-CN` vs `zh_CN` vs `values-zh-rCN` vs `zh-Hans`); the
table above is the authoritative mapping.

## Two rules that apply everywhere

**1. Keep the placeholders and non-translatable tokens intact.** Every catalog
uses format placeholders (`%s` / `%d` / `%@` / `%lld` / `{0}` / `%1$s` / `%n`,
etc.). Translate the words around them, never the placeholders themselves. If a
language needs a different word order, use the *positional* form
(`%[1]s`/`%[2]s`, `%1$@`, `%1$s`, `{0}`/`{1}`, `%1`/`%2`) rather than dropping or
reordering the plain form. The per-client tests below fail if a placeholder is
added or dropped.

**2. Shared terms must match across clients.** These are one product; a term
should read identically in the TUI, the web remote and every GUI. Two parts:

- **Brand and technical tokens stay verbatim, untranslated, in every language:**
  OpenDeezer, Deezer, Flow, ARL, FLAC, MP3, HiFi, HQ, kbps, dB, Hz, kHz,
  ReplayGain, Blowfish, WASAPI, Wi-Fi, GitHub, AGPL-3.0, Cycl0o0, WinUI, Fluent,
  C#, Go, `libdeezercore.dll`.
- **Shared UI nouns get one translation per language, reused everywhere.** Pick a
  wording for terms like Liked Songs, Playlists, Charts, Podcasts, Search, Queue,
  Sleep timer, Crossfade, Gapless, Equalizer, Mono, Home, Settings, Language — and
  use the *same* translation for that term in all seven files. Before translating
  a term in a new client, check what you already used in the others (grep the
  existing `.json` / `.po` / `.resw` / `.ts` / `.strings` / `.xml` / `.xcstrings`).

## Plural categories

Counted nouns ("1 track" vs "5 tracks") need the CLDR plural categories for the
language. Provide exactly the categories the language uses — extra ones are
harmless on the TUI catalogs but are rejected/linted on some GUIs, so match the
list:

| Language | CLDR plural categories |
|---|---|
| English, हिन्दी | `one`, `other` |
| Español, Français | `one`, `other` (some engines also want `many`) |
| Русский | `one`, `few`, `many`, `other` |
| العربية | `zero`, `one`, `two`, `few`, `many`, `other` |
| 简体中文 | `other` only |

Each client below says exactly which counted nouns exist and how its plural forms
are named.

## Right-to-left

Arabic (and any future `he`/`fa`/`ur`) is right-to-left. The GUIs flip layout for
RTL locales automatically; the only place you touch this when adding an RTL
language is Windows (`Loc.SetLanguage`, see below). For an LTR language there is
nothing to do.

---

## TUI + web remote (Go engine)

The Bubble Tea TUI reads JSON catalogs; the phone web remote has an inline JS
dictionary. Both live in the Go tree.

Adding a locale (example: Portuguese `pt`):

1. **`internal/i18n/i18n.go`** — append `{Code: "pt", Name: "Português"}` to the
   `supported` slice, add `"pt"` to the switch in `parseLocale` (so detection and
   `SetLocale` accept it), and add a case in `pluralCategory` for the language's
   CLDR cardinal rule. Languages that are just `one`/`other` can share the default
   branch; add an explicit case only for `few`/`many`/`two`/`zero` languages.
2. **`internal/ui/language.go`** — add `"pt"` to `languageOrder` so the 🌐 Language
   menu row cycles to it.
3. **`internal/i18n/locales/pt.json`** — copy `en.json` and translate every value.
   Keep every key and every format verb (`%s` / `%d` / `%%`) intact; if word order
   changes with two or more arguments, use Go indexed verbs (`%[1]s` / `%[2]s`).
   For the counted-noun message, provide `"Queue (%d track)|<category>"` keys for
   each category the language can return (filling all six is safe — extra
   categories are ignored).
4. **`internal/control/webui/remote.html`** — add a `pt: { … }` block to the inline
   `I18N` object, mirroring the `en` keys exactly. The web remote auto-selects it
   from `navigator.language` (2-letter prefix, `en` fallback).

Verify:

```sh
go test ./internal/i18n/                  # completeness + placeholder-parity tests
go build ./... && go vet ./...
gofmt -w internal/i18n/i18n.go internal/ui/language.go
```

The completeness test fails if any `en` key is missing from `pt.json`; the
placeholder-parity test fails if any format verb was added or dropped. Hand-editing
the JSON is fully supported — the tests enforce parity.

## macOS (`gui/macos`)

Strings live in per-language `.lproj` bundles; every user-facing literal already
routes through `L()` / `Lf()` / `Lp()` (`Bundle.module`) in
`Sources/OpenDeezer/L.swift`, so no Swift changes are needed for existing strings.

Adding a language (example: `pt-BR`; use the exact BCP-47 folder name, e.g.
`pt-BR.lproj`, `zh-Hans.lproj`):

1. Copy `Sources/OpenDeezer/Resources/en.lproj/Localizable.strings` and
   `en.lproj/Localizable.stringsdict` into a new
   `Sources/OpenDeezer/Resources/<code>.lproj/`.
2. In `Localizable.strings`, translate only the right-hand value of each
   `"key" = "value";` line. Keep the keys and every `%@`/`%d` placeholder
   unchanged (use `%1$@`/`%2$@` only if you must reorder).
3. In `Localizable.stringsdict`, translate each plural form and provide the CLDR
   categories the language requires. Keep `NSStringLocalizedFormatKey = "%#@v@"`,
   `NSStringFormatValueTypeKey = "d"` and the `%d` specifier.
4. Add the code to the `CFBundleLocalizations` array in `Info.plist` so macOS
   offers it under System Settings → per-app language.

No `Package.swift` edit is needed — `resources: [.process("Resources")]`
auto-includes any new `.lproj`, and the Makefile copies the resource bundle into
the `.app`. When you introduce a *new* literal, wrap it with `L`/`Lf`/`Lp` and add
its key to every `<lang>.lproj/Localizable.strings` (and to the `.stringsdict` if
it is a counted noun).

Verify (from `gui/macos`):

```sh
make app        # or: swift build
```

## Windows (`gui/windows`)

The source of truth is `Strings/en-US/Resources.resw`; a missing key makes
`Loc.S` return the raw key at runtime.

Adding a language (example: Italian `it-IT`):

1. Create `Strings/it-IT/Resources.resw` — copy `Strings/en-US/Resources.resw`
   verbatim, then translate every `<value>`. Keep every `<data name="…">` key
   unchanged, all `{0}`/`{1}` placeholders intact, and the brand/tech tokens
   verbatim.
2. **Plurals.** For the five counted nouns (Tracks, Episodes, Fans, Minutes,
   Seconds), include only the CLDR forms the language uses, named
   `<data name="Tracks_One">`, `Tracks_Other`, plus `Few`/`Many`/`Two`/`Zero` as
   needed. If the language needs categories beyond `one`/`other`, add a case to
   `Loc.PluralCategory(lang, n)` in `Loc.cs` returning the matching suffix names;
   unlisted languages fall back to `one`/`other`.
3. **Register it in the picker.** Add a tuple to `Loc.Languages` in `Loc.cs`, e.g.
   `("it-IT", "Italiano")` — the first element MUST equal the folder name, the
   second is the native autonym shown in Settings → Language.
4. **RTL.** `Loc.SetLanguage` already flags `ar`/`he`/`fa`/`ur` as right-to-left;
   extend that check only if the new language is RTL.

No `.csproj` edit is needed — it globs `Strings\**\*.resw` as `PRIResource`, and
MakePri compiles the new locale into `resources.pri` automatically.

Verify:

```powershell
xmllint --noout Strings\it-IT\Resources.resw   # well-formed XML
.\build.ps1                                     # dotnet publish
```

Confirm every `<data name>` present in `en-US` is present in the new file (key
parity).

## GNOME (`gui/gnome`)

Standard gettext. Requires the `gettext` tools (`msgfmt`/`xgettext`/`msginit`/
`msgmerge`). All commands run from `gui/gnome`.

Adding a language (example: Portuguese `pt`):

1. Add the locale code on its own line to `po/LINGUAS` (keep it sorted). Use the
   gettext form — `language`, or `language_REGION` for a variant (this repo uses
   `zh_CN`).
2. Create `po/pt.po`. Easiest:
   `msginit --input=po/opendeezer-gnome.pot --locale=pt --output=po/pt.po`
   (fills a correct header including the right `Plural-Forms`). Or copy an existing
   same-plural-rules `.po` (e.g. `es.po`) and edit the `Language:` /
   `Language-Team:` / `Plural-Forms:` header lines.
3. Translate every `msgstr`. Keep printf specifiers exactly (`%s`, `%d`, `%u`,
   `%lld`) and the brand words verbatim. Leave the `OpenDeezer` msgid untranslated
   (it is the desktop `Name=` brand). For the four plural entries (`msgid_plural`
   present: `%u track` / `%lld track` / `%lld fan` / `%lld episode`) provide
   exactly `nplurals` `msgstr[N]` forms, each keeping the number placeholder.

No meson edits are needed — `po/meson.build` (the `i18n.gettext` glib preset)
auto-discovers `LINGUAS`, builds `pt/LC_MESSAGES/opendeezer-gnome.mo`, and the
`.desktop` merge picks up `pt` too.

Verify:

```sh
/usr/bin/msgfmt -cv -o /dev/null po/pt.po   # must report zero errors
./build.sh
```

If you first add *new* source strings, wrap them in `_("…")` (or `N_()`+`_()` for
compile-time arrays, `ngettext(sing, plur, n)` for counted nouns) in `src/main.c`,
regenerate the template with `meson compile -C build opendeezer-gnome-pot`, then
`msgmerge -U po/*.po po/opendeezer-gnome.pot` before translating the new msgids.

## KDE (`gui/kde`)

Qt Linguist catalogs. All commands run from `gui/kde`. Do **not** commit `.qm`
files (git-ignored; the build regenerates them).

Adding a locale (example: `pt_BR`):

1. Create the catalog with `lupdate` (the filename suffix sets the language and
   the correct plural-form count):

   ```sh
   lupdate src/mainwindow.cpp src/settingsdialog.cpp src/logindialog.cpp \
           src/loginhelper.cpp src/main.cpp -ts i18n/opendeezer_pt_BR.ts
   ```

   Or copy an existing `i18n/opendeezer_fr.ts` and change the
   `<TS language="…">` attribute.
2. Open it in Qt Linguist (or edit the XML) and fill every `<translation>`,
   including all `<numerusform>` children for the seven `%n` plural messages
   (`%n track(s)`, `Liked Songs — %n track(s)`, `Charts — %n track(s)`,
   `%n playlist(s)`, `%n episode(s)`, `%n podcast(s)`, `%n fan(s)`). Remove all
   `type="unfinished"`. Keep `%1`/`%2`/`%n` placeholders and brand tokens verbatim;
   keep the `&` mnemonic in menu strings.
3. Register it in `CMakeLists.txt`: add
   `${CMAKE_CURRENT_SOURCE_DIR}/i18n/opendeezer_pt_BR.ts` to the
   `OPENDEEZER_TS_FILES` list. The `foreach` compiles it to `.qm` and all three
   targets embed it — no other CMake change.

No source change is needed: `main.cpp`/`loginhelper.cpp` already load
`:/i18n/opendeezer_<xx>` for `QLocale::system()` and flip to RTL when the locale
is right-to-left.

Verify:

```sh
lrelease i18n/opendeezer_pt_BR.ts   # must report 0 unfinished, 0 errors
./build.sh
```

## Android (`gui/android`)

Both flavors (phone/tablet + Android TV) share `app/src/main/res`, so one file
localizes both.

Adding a language (example: German `de`):

1. Copy `app/src/main/res/values/strings.xml` to a new locale-qualified dir
   `app/src/main/res/values-<code>/strings.xml`, where `<code>` is the Android
   resource qualifier — `language`, or `language-rREGION` (e.g. `values-de`,
   `values-pt-rBR`, `values-zh-rCN`).
2. Translate every `<string>` value and every `<plurals><item>` value. Keep the
   `name=` attributes unchanged and preserve positional placeholders exactly
   (`%1$s`, `%2$s`, `%1$d`).
3. **Delete the four `translatable="false"` entries** from your new file
   (`settings_connect`, `device_self_phone`, `device_self_tv`,
   `update_notes_title`) — do not translate them; leaving them triggers an
   `ExtraTranslation` lint error.
4. For the four `<plurals>` (`n_tracks`, `n_episodes`, `n_fans`,
   `pauses_in_minutes`) provide exactly the CLDR categories the language needs.
5. Escape apostrophes as `\'` and ampersands as `&amp;` in XML values.
6. Add the BCP-47 tag to `app/src/main/res/xml/locales_config.xml` (e.g.
   `<locale android:name="de"/>`) so the Android 13+ per-app language picker lists
   it.

No engine change is needed — the `odmobile.aar` is git-ignored and rebuilt each CI
run via gomobile. Non-composable code uses `context.getString(R.string.x)`; Compose
uses `stringResource()` / `pluralStringResource()`.

Verify:

```sh
export ANDROID_HOME=$HOME/Library/Android/sdk
./gradlew :app:assembleDebug
./gradlew :app:lintMobileDebug :app:lintTvDebug
```

`MissingTranslation`, `ExtraTranslation` and `MissingQuantity` lint must all be 0.

## iOS (`gui/ios`)

Two hand-editable Apple String Catalogs (JSON, `sourceLanguage=en`):
`Resources/Localizable.xcstrings` (UI, 132 keys) and `Resources/InfoPlist.xcstrings`
(`NSLocalNetworkUsageDescription`).

Adding a locale (example: `de`; use `zh-Hans`/`zh-Hant` for Chinese):

1. In **both** `.xcstrings`, for every entry under
   `strings.<key>.localizations`, add a `"<code>"` object.
   - Normal string: `{"stringUnit":{"state":"translated","value":"…"}}`.
   - The four plural keys (`%lld songs`, `%lld episodes`, `%lld min`,
     `%lld fans`):
     `{"variations":{"plural":{ "<cat>":{"stringUnit":{"state":"translated","value":"…"}} }}}`
     providing the language's CLDR categories. Keep `%@`, `%lld` and word order
     intact.
2. Leave brand/verbatim keys as-is (the OS names — Mac/Windows/Linux/Android/iOS —
   are not in the catalog and correctly fall back to English).

No `project.yml` / xcodegen change is needed — the catalog declares its own
languages, so a new `<code>.lproj` is produced at build time.

Verify:

```sh
./build.sh   # binds the Go engine, xcodegen generate, simulator xcodebuild
```

Confirm a `<code>.lproj` appears inside the built `OpenDeezer.app`.

---

## Checklist before opening a PR

- [ ] The language is registered in **every** client you touched (see the mapping
      table for the correct per-platform code), and in each client's language
      picker / list.
- [ ] Every key from the English source exists in the new file (no missing keys →
      no raw-key fallbacks at runtime).
- [ ] Placeholders and brand/tech tokens are byte-for-byte intact.
- [ ] Plural entries cover exactly the language's CLDR categories.
- [ ] Shared UI terms match the wording already used in the other clients.
- [ ] Each client's verify command above passes (tests / lint / `.po`/`.resw`/
      `.ts` validation / build).
