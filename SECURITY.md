# Security Policy

## Reporting a vulnerability

Please report security issues privately to **security@cyclooo.fr**. Do not open a
public GitHub issue for a vulnerability.

Include what you found, how to reproduce it, and the affected version if you know
it. You'll get an acknowledgement, and a fix + disclosure timeline once it's
triaged. Coordinated disclosure is appreciated.

## Scope

OpenDeezer logs in, decrypts and decodes everything **on your own machine** — it
processes untrusted bytes from several sources, which is where the security
attention goes:

- **Encrypted audio streams** — Deezer's CDN delivers `BF_CBC_STRIPE`-encrypted
  audio (`internal/deezer/decrypt.go`).
- **Media decoders** — MP3 and FLAC are decoded from whatever the CDN (or, for
  podcasts, an off-Deezer host) returns (`internal/audio`).
- **LAN packets** — OpenDeezer Connect discovery parses UDP packets from the
  local network (`internal/discovery`).
- **The control / web-remote API** — an opt-in HTTP/JSON server
  (`internal/control`); see its auth model in the README.

Your **ARL** (Deezer session cookie) is stored locally and only ever sent to
deezer.com in the requests OpenDeezer makes for you.

## Continuous fuzzing

The decrypt and decode paths are continuously fuzzed. Native Go fuzz harnesses
live next to the code (`FuzzDecryptTrack`, `FuzzStripeChunking`, `FuzzFLACDecode`)
and run both locally (`go test -fuzz=...`) and in CI via
[ClusterFuzzLite](https://google.github.io/clusterfuzzlite/) (OSS-Fuzz's engine,
self-hosted in this repo's Actions). See [docs/FUZZING.md](docs/FUZZING.md).
