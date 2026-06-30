# Build image for OpenDeezer's fuzzers (ClusterFuzzLite + OSS-Fuzz).
FROM gcr.io/oss-fuzz-base/base-builder-go

# The FLAC fuzzer lives in internal/audio, which pulls in the cgo audio backend
# (oto/malgo) — that needs the ALSA dev headers to link on Linux.
RUN apt-get update && apt-get install -y --no-install-recommends libasound2-dev && \
    rm -rf /var/lib/apt/lists/*

COPY . $SRC/opendeezer
COPY .clusterfuzzlite/build.sh $SRC/build.sh
WORKDIR $SRC/opendeezer
