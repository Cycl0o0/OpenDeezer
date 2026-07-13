package audio

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/Cycl0o0/OpenDeezer/v3/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/v3/internal/mediacache"
)

// Compile-time check: the real on-disk cache satisfies the audio-side
// interface (the product code only ever depends on StreamCache).
var _ StreamCache = (*mediacache.Cache)(nil)

// makeCipher builds deterministic pseudo-ciphertext of n bytes.
func makeCipher(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*11 + 5) % 253)
	}
	return b
}

// countingCDN serves body with HTTP Range support, counting every request and
// recording the start offset of the most recent Range request (-1 = none).
type countingCDN struct {
	srv       *httptest.Server
	hits      atomic.Int32
	lastRange atomic.Int64
}

func newCountingCDN(t *testing.T, body []byte) *countingCDN {
	t.Helper()
	c := &countingCDN{}
	c.lastRange.Store(-1)
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.hits.Add(1)
		if rng := r.Header.Get("Range"); rng != "" {
			start := parseRangeStart(t, rng)
			c.lastRange.Store(int64(start))
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(body)-1, len(body)))
			w.Header().Set("Content-Length", strconv.Itoa(len(body)-start))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(body[start:])
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func newTestCache(t *testing.T) *mediacache.Cache {
	t.Helper()
	mc, err := mediacache.New(t.TempDir(), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	return mc
}

// TestStreamCacheHitSkipsHTTP verifies that after one full play the second play
// of the same track+format is served entirely from the cache — zero HTTP
// requests — and yields byte-identical decrypted output.
func TestStreamCacheHitSkipsHTTP(t *testing.T) {
	const trackID = "3135556"
	cipher := makeCipher(300000)
	want, err := deezer.DecryptTrack(trackID, cipher)
	if err != nil {
		t.Fatal(err)
	}
	mc := newTestCache(t)
	cdn := newCountingCDN(t, cipher)
	plan := &deezer.StreamPlan{CDNURL: cdn.srv.URL, TrackID: trackID, Format: "MP3_320", Encrypted: true}

	s1 := newSource(plan, 10000)
	s1.cache = mc
	s1.download()
	if got := drainStreamBuffer(s1.sb); !bytes.Equal(got, want) {
		t.Fatalf("first play mismatch: got %d bytes, want %d", len(got), len(want))
	}
	if h := cdn.hits.Load(); h != 1 {
		t.Fatalf("first play made %d HTTP requests, want 1", h)
	}

	s2 := newSource(plan, 10000)
	s2.cache = mc
	s2.download()
	if got := drainStreamBuffer(s2.sb); !bytes.Equal(got, want) {
		t.Fatalf("cached play output differs from the network play")
	}
	if h := cdn.hits.Load(); h != 1 {
		t.Fatalf("cached play performed HTTP requests: total %d, want still 1", h)
	}
	if e := s2.lastErr(); e != "" {
		t.Fatalf("unexpected error on cached play: %s", e)
	}
	// The cached size fed setContentLength/preallocate (SeekEnd resolves early).
	s2.sb.mu.Lock()
	total := s2.sb.total
	s2.sb.mu.Unlock()
	if total != int64(len(cipher)) {
		t.Fatalf("cached play content length = %d, want %d", total, len(cipher))
	}
}

// TestStreamCacheStoresExactCiphertext verifies that after a full first play
// the cache holds exactly the raw bytes the server served (ciphertext at rest,
// never the decrypted output).
func TestStreamCacheStoresExactCiphertext(t *testing.T) {
	const trackID = "3135556"
	cipher := makeCipher(260000)
	mc := newTestCache(t)
	cdn := newCountingCDN(t, cipher)

	s := newSource(&deezer.StreamPlan{CDNURL: cdn.srv.URL, TrackID: trackID, Format: "FLAC", Encrypted: true}, 10000)
	s.cache = mc
	s.download()
	drainStreamBuffer(s.sb)

	rc, size, ok := mc.Get(trackID + ".FLAC")
	if !ok {
		t.Fatal("cache miss after a full play")
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(cipher)) || !bytes.Equal(got, cipher) {
		t.Fatalf("cached entry is not the served ciphertext: got %d bytes (size %d), want %d", len(got), size, len(cipher))
	}
	if n, b := mc.Stats(); n != 1 || b != int64(len(cipher)) {
		t.Fatalf("Stats = %d entries / %d bytes, want 1 / %d", n, b, len(cipher))
	}
}

// TestStreamCacheInterruptedDownloadNotCached verifies that a torn first
// download (mid-body drop + Range resume) leaves NO cache entry while playback
// still completes correctly via the resume path.
func TestStreamCacheInterruptedDownloadNotCached(t *testing.T) {
	const trackID = "3135556"
	cipher := makeCipher(300000)
	want, err := deezer.DecryptTrack(trackID, cipher)
	if err != nil {
		t.Fatal(err)
	}
	mc := newTestCache(t)
	srv := flakyServer(t, cipher, 150000)
	defer srv.Close()

	s := newSource(&deezer.StreamPlan{CDNURL: srv.URL, TrackID: trackID, Format: "MP3_320", Encrypted: true}, 10000)
	s.cache = mc
	s.download()
	if got := drainStreamBuffer(s.sb); !bytes.Equal(got, want) {
		t.Fatalf("interrupted play did not decode correctly: got %d bytes, want %d", len(got), len(want))
	}
	if e := s.lastErr(); e != "" {
		t.Fatalf("unexpected error after successful resume: %s", e)
	}
	if _, _, ok := mc.Get(trackID + ".MP3_320"); ok {
		t.Fatal("interrupted first download left a cache entry")
	}
	if n, b := mc.Stats(); n != 0 || b != 0 {
		t.Fatalf("Stats after interrupted download = %d entries / %d bytes, want 0 / 0", n, b)
	}
}

// TestStreamCacheSkipsPreviewAndPassthrough verifies that plain passthrough
// streams (podcasts) and preview plans never create cache entries.
func TestStreamCacheSkipsPreviewAndPassthrough(t *testing.T) {
	mc := newTestCache(t)

	// Plain passthrough (podcast-style, Encrypted=false).
	body := makeCipher(120000)
	cdn := newCountingCDN(t, body)
	s := newSource(&deezer.StreamPlan{CDNURL: cdn.srv.URL, TrackID: "ep1", Format: "MP3", Encrypted: false}, 10000)
	s.cache = mc
	s.download()
	if got := drainStreamBuffer(s.sb); !bytes.Equal(got, body) {
		t.Fatalf("passthrough play mismatch: got %d bytes, want %d", len(got), len(body))
	}

	// Preview (the 30-second free fallback) — excluded even when flagged
	// encrypted, so a preview can never shadow the full track under its key.
	cipher := makeCipher(80000)
	cdn2 := newCountingCDN(t, cipher)
	s2 := newSource(&deezer.StreamPlan{CDNURL: cdn2.srv.URL, TrackID: "3135556", Format: "MP3_128", Encrypted: true, Preview: true}, 10000)
	s2.cache = mc
	s2.download()
	drainStreamBuffer(s2.sb)

	if n, b := mc.Stats(); n != 0 || b != 0 {
		t.Fatalf("preview/passthrough created cache entries: %d entries / %d bytes", n, b)
	}
}

// tornCache is a StreamCache whose Get always hits but whose reader fails
// after `good` bytes, exercising the fall-back-to-HTTP-from-consumed-offset
// path of download().
type tornCache struct {
	data []byte
	good int
}

func (c *tornCache) Get(string) (io.ReadCloser, int64, bool) {
	return &tornReader{data: c.data[:c.good]}, int64(len(c.data)), true
}
func (c *tornCache) TeeReader(_ string, src io.Reader) io.Reader { return src }

type tornReader struct {
	data []byte
	off  int
}

func (r *tornReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, errors.New("cached file torn")
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}
func (r *tornReader) Close() error { return nil }

// TestStreamCacheTornHitFallsBackToHTTP verifies that a mid-read error on a
// cached entry falls back to the network from the exact ciphertext offset
// already fed (a Range request at consumed), and playback still decodes
// byte-identically.
func TestStreamCacheTornHitFallsBackToHTTP(t *testing.T) {
	const trackID = "3135556"
	const good = 100000 // deliberately not 2048-chunk-aligned
	cipher := makeCipher(260000)
	want, err := deezer.DecryptTrack(trackID, cipher)
	if err != nil {
		t.Fatal(err)
	}
	cdn := newCountingCDN(t, cipher)

	s := newSource(&deezer.StreamPlan{CDNURL: cdn.srv.URL, TrackID: trackID, Format: "MP3_320", Encrypted: true}, 10000)
	s.cache = &tornCache{data: cipher, good: good}
	s.download()
	if got := drainStreamBuffer(s.sb); !bytes.Equal(got, want) {
		t.Fatalf("torn-cache play mismatch: got %d bytes, want %d", len(got), len(want))
	}
	if h := cdn.hits.Load(); h == 0 {
		t.Fatal("no HTTP fallback happened after the torn cache read")
	}
	if start := cdn.lastRange.Load(); start != good {
		t.Fatalf("HTTP fallback resumed from offset %d, want %d", start, good)
	}
}

// TestSetStreamCacheNilSafe covers the setter contract: nil-safe, installable
// and clearable before playback starts.
func TestSetStreamCacheNilSafe(t *testing.T) {
	p := &Player{}
	p.SetStreamCache(nil) // must not panic
	mc := newTestCache(t)
	p.SetStreamCache(mc)
	if p.streamCache == nil {
		t.Fatal("SetStreamCache did not install the cache")
	}
	p.SetStreamCache(nil)
	if p.streamCache != nil {
		t.Fatal("SetStreamCache(nil) did not clear the cache")
	}
}

// TestStreamCacheOfflineHit verifies that an offline play (plan.CDNURL == "")
// with a cache hit successfully decodes byte-identical PCM data with zero HTTP requests.
func TestStreamCacheOfflineHit(t *testing.T) {
	const trackID = "123456"
	cipher := makeCipher(100000)
	want, err := deezer.DecryptTrack(trackID, cipher)
	if err != nil {
		t.Fatal(err)
	}

	mc := newTestCache(t)
	// Seed the cache directly
	cacheKey := trackID + ".MP3_320"
	teeReader := mc.TeeReader(cacheKey, bytes.NewReader(cipher))
	_, err = io.ReadAll(teeReader)
	if err != nil {
		t.Fatal(err)
	}

	// Create plan with empty CDNURL
	plan := &deezer.StreamPlan{
		CDNURL:    "",
		TrackID:   trackID,
		Format:    "MP3_320",
		Encrypted: true,
	}

	s := newSource(plan, 10000)
	s.cache = mc
	s.download()

	if got := drainStreamBuffer(s.sb); !bytes.Equal(got, want) {
		t.Fatalf("offline hit play mismatch: got %d bytes, want %d", len(got), len(want))
	}
	if e := s.lastErr(); e != "" {
		t.Fatalf("unexpected error on offline play: %s", e)
	}

	// Check that size handling worked correctly using the cached size
	s.sb.mu.Lock()
	total := s.sb.total
	s.sb.mu.Unlock()
	if total != int64(len(cipher)) {
		t.Fatalf("offline play content length = %d, want %d", total, len(cipher))
	}
}

// TestStreamCacheOfflineMiss verifies that an offline play (plan.CDNURL == "")
// with a cache miss fails immediately with ErrOffline, without panic or HTTP dialing.
func TestStreamCacheOfflineMiss(t *testing.T) {
	mc := newTestCache(t)
	// Create plan with empty CDNURL and no entry in cache
	plan := &deezer.StreamPlan{
		CDNURL:    "",
		TrackID:   "not_cached_track",
		Format:    "MP3_320",
		Encrypted: true,
	}

	s := newSource(plan, 10000)
	s.cache = mc
	s.download()

	// Ensure the source error was set to ErrOffline
	gotErr := s.lastErr()
	if gotErr != ErrOffline.Error() {
		t.Fatalf("expected error %q, got %q", ErrOffline.Error(), gotErr)
	}

	// Drain streamBuffer should return the same error
	_, err := s.sb.Read(make([]byte, 100))
	if err == nil {
		t.Fatal("expected read error from streamBuffer, got nil")
	}
	if !errors.Is(err, ErrOffline) {
		t.Fatalf("expected streamBuffer error errors.Is(ErrOffline), got %v", err)
	}
}
