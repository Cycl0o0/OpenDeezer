#!/bin/bash -eu
# Compile OpenDeezer's native Go fuzzers (testing.F) into libFuzzer targets.
# Invoked by ClusterFuzzLite and OSS-Fuzz inside the base-builder-go image.
cd "$SRC/opendeezer"

MOD=github.com/Cycl0o0/OpenDeezer/v2

# compile_native_go_fuzzer rewrites each testing.F harness to use go-118-fuzz-
# build's shim, so that package must resolve in the module. Add it here (the
# build container is ephemeral, so this doesn't touch the committed go.mod).
# NB: do NOT `go mod tidy` after — tidy prunes this dep (nothing in the committed
# tree imports it; the import is injected by compile_native_go_fuzzer at build).
go get github.com/AdamKorcz/go-118-fuzz-build/testing

# --- pure-Go, security-critical custom code: the BF_CBC_STRIPE decryptor -------
compile_native_go_fuzzer "$MOD/internal/deezer" FuzzDecryptTrack   fuzz_decrypt_track
compile_native_go_fuzzer "$MOD/internal/deezer" FuzzStripeChunking fuzz_stripe_chunking

# --- FLAC decode (internal/audio) ---------------------------------------------
# NOT compiled into the continuous run yet: FuzzFLACDecode already turned up a
# real out-of-memory (a malformed FLAC drives an unbounded allocation in the
# mewkiz/flac decoder — a DoS). The harness lives in internal/audio/fuzz_test.go
# for local runs (`go test -fuzz=FuzzFLACDecode ./internal/audio`); it goes back
# into CI once that allocation is bounded (a LimitReader wrapper / upstream fix).
# See docs/FUZZING.md.
