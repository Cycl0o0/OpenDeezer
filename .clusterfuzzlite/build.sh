#!/bin/bash -eu
# Compile OpenDeezer's native Go fuzzers (testing.F) into libFuzzer targets.
# Invoked by ClusterFuzzLite and OSS-Fuzz inside the base-builder-go image.
cd "$SRC/opendeezer"

MOD=github.com/Cycl0o0/OpenDeezer

# --- pure-Go, security-critical custom code: the BF_CBC_STRIPE decryptor -------
compile_native_go_fuzzer "$MOD/internal/deezer" FuzzDecryptTrack   fuzz_decrypt_track
compile_native_go_fuzzer "$MOD/internal/deezer" FuzzStripeChunking fuzz_stripe_chunking

# --- FLAC decode of untrusted media bytes (cgo audio pkg; ALSA installed) ------
# Best-effort: if the cgo audio package can't link in the fuzzing image, keep the
# pure-Go targets above rather than failing the whole build.
compile_native_go_fuzzer "$MOD/internal/audio" FuzzFLACDecode fuzz_flac_decode || \
  echo "warning: FuzzFLACDecode (cgo) failed to compile in this image; skipping it"
