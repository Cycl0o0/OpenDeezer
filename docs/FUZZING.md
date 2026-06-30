# Fuzzing

OpenDeezer decodes untrusted bytes (encrypted streams, MP3/FLAC, LAN packets), so
those paths are fuzzed. The harnesses are Go's native `testing.F` fuzzers, kept
next to the code they exercise:

| Fuzzer | Package | What it hits |
|---|---|---|
| `FuzzDecryptTrack` | `internal/deezer` | BF_CBC_STRIPE whole-buffer decrypt; asserts length is preserved |
| `FuzzStripeChunking` | `internal/deezer` | the stripe decryptor is independent of how input is split across `Feed()` calls |
| `FuzzFLACDecode` | `internal/audio` | the FLAC decode path on a malformed stream (must not panic) |

## Run locally

```sh
go test -run=^$ -fuzz=FuzzDecryptTrack   -fuzztime=60s ./internal/deezer
go test -run=^$ -fuzz=FuzzStripeChunking -fuzztime=60s ./internal/deezer
go test -run=^$ -fuzz=FuzzFLACDecode     -fuzztime=60s ./internal/audio
```

Without `-fuzz`, the `Fuzz*` functions still run their seed corpus as ordinary
tests under `go test ./...`. A crash is written to `testdata/fuzz/<Fuzzer>/` and
replays deterministically — commit it as a regression seed once fixed.

## Continuous fuzzing (CI)

[ClusterFuzzLite](https://google.github.io/clusterfuzzlite/) runs the same
harnesses in GitHub Actions — it's OSS-Fuzz's engine, self-hosted (no acceptance
into the OSS-Fuzz program required):

- **`.github/workflows/cflite-pr.yml`** — a short (3 min) run on each PR, focused
  on the changed code.
- **`.github/workflows/cflite-cron.yml`** — a nightly batch run that builds a
  corpus over time.

The build is defined by `.clusterfuzzlite/` (`Dockerfile`, `build.sh`,
`project.yaml`). `build.sh` compiles each `Fuzz*` with `compile_native_go_fuzzer`.

## Graduating to OSS-Fuzz proper

OSS-Fuzz (Google-hosted) gives free continuous fuzzing + a dedicated
infrastructure, but it only accepts projects with "a significant user base and/or
[that] are critical to global IT infrastructure." If OpenDeezer reaches that bar,
submission is small: the `.clusterfuzzlite/` files (`project.yaml`, `Dockerfile`,
`build.sh`) are already in the format OSS-Fuzz expects — copy them to
`projects/opendeezer/` in [google/oss-fuzz](https://github.com/google/oss-fuzz)
and open a PR. `project.yaml` already lists `security@cyclooo.fr` as the contact.
