# Contributing to OpenDeezer

Thanks for your interest in OpenDeezer.

## Development

The engine and TUI are pure Go (cgo only for ALSA on Linux and the c-archive
used by the native GUIs).

```sh
go build ./...                       # build everything
go test ./...                        # run tests
go test -race ./...                  # race detector (what CI runs)
go vet ./...                         # static checks
golangci-lint run                    # lint (see .golangci.yml)
```

Build the shared engine for the native GUIs:

```sh
CGO_ENABLED=1 go build -buildmode=c-archive -o libdeezercore.a ./corelib
```

Run the TUI (needs a Deezer Premium ARL — see the README):

```sh
go run ./cmd/opendeezer
OPENDEEZER_LOG=debug go run ./cmd/opendeezer   # with debug logging to opendeezer.log
```

## Architecture

- `internal/deezer` — API client (ARL login, gw-light + REST browse, lyrics,
  charts, artist profiles) and Blowfish stream decryption.
- `internal/audio` — oto-backed player (MP3 + FLAC, seek, ReplayGain).
- `internal/queue` — shared playback queue (shuffle/repeat/history).
- `internal/ui` — Bubble Tea TUI.
- `internal/log` — leveled file logging.
- `corelib` — the `DZ*` C API consumed by the native GUIs.
- `gui/*` — native frontends (macOS SwiftUI, GNOME GTK, KDE Qt, Windows WinUI,
  unified Linux launcher).

## Translations

OpenDeezer ships in English, 简体中文, हिन्दी, Español, Français, العربية and
Русский, across every client. New languages and fixes to the existing ones are
welcome — each client localizes natively (Go JSON catalogs for the TUI, an inline
dictionary for the web remote, `.lproj` on macOS/iOS, `.resw` on Windows, gettext
`.po` on GNOME, Qt `.ts` on KDE, `values-*` resources on Android). See
[`docs/TRANSLATIONS.md`](docs/TRANSLATIONS.md) for the per-client steps and how to
verify each one.

## Pull requests

- Keep the build, `go vet`, and `go test -race ./...` green.
- Match the surrounding style; add tests for new logic where practical.
- New `DZ*` exports must also be added to `gui/windows/libdeezercore.def`.
- Commits are authored by the project owner only — do not add co-author trailers.

## Scope & legality

OpenDeezer is an authenticated Deezer client. Playback quality and downloads are
limited to the signed-in account's Deezer entitlements. It is not affiliated with
Deezer; contributions must not circumvent account access controls or enable
access to media that is unavailable to that account.
