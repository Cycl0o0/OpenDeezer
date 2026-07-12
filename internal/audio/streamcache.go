package audio

import "io"

// StreamCache is an optional on-disk cache of raw CDN stream bodies, keyed by
// "trackID.format" (e.g. "12345.MP3_320"). The stored bytes are exactly what
// the CDN served — still stripe-encrypted ciphertext for tracks — so nothing
// decrypted ever lands on disk; decryption still happens in the normal
// playback pipeline. *mediacache.Cache satisfies this interface; the audio
// package depends only on the interface so the cache stays a pluggable opt-in.
type StreamCache interface {
	// Get returns a reader over the cached body and its size when key is
	// present. The caller must close the reader.
	Get(key string) (io.ReadCloser, int64, bool)
	// TeeReader returns a reader that consumes src while mirroring the bytes
	// into the cache under key, committing the entry on clean EOF and
	// discarding the partial write on any read/write error. Implementations
	// return src unchanged when they cannot start the write (e.g. one is
	// already in progress for key).
	TeeReader(key string, src io.Reader) io.Reader
}

// SetStreamCache attaches an optional raw-stream cache used by every source
// created afterwards (Play/Preload). nil — the default — disables caching.
// Only full encrypted track streams are cached; previews and plain passthrough
// streams (podcasts) never touch the cache.
//
// Call it once at startup, before any playback: the field is read without
// synchronization when sources are created, so SetStreamCache is NOT safe to
// call concurrently with Play/Preload or while a track is playing.
func (p *Player) SetStreamCache(c StreamCache) { p.streamCache = c }
