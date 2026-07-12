package audio

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/deezer"
)

// flakyServer serves body once with the connection torn mid-transfer (the client
// sees io.ErrUnexpectedEOF), then honors a Range request for the remainder. It
// exercises source.download()'s HTTP Range resume.
func flakyServer(t *testing.T, body []byte, dropAt int) *httptest.Server {
	t.Helper()
	var served int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rng := r.Header.Get("Range"); rng != "" {
			// Resume request: 206 with the remaining bytes.
			start := parseRangeStart(t, rng)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(body)-1, len(body)))
			w.Header().Set("Content-Length", strconv.Itoa(len(body)-start))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(body[start:])
			return
		}
		// First (full) request: advertise the full length but write only part of
		// it, then hijack and slam the connection so the client gets a torn body.
		if atomic.AddInt32(&served, 1) == 1 {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("server does not support hijacking")
				return
			}
			conn, bufrw, err := hj.Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			fmt.Fprintf(bufrw, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n", len(body))
			_, _ = bufrw.Write(body[:dropAt])
			_ = bufrw.Flush()
			_ = conn.Close() // abort: fewer bytes than Content-Length -> unexpected EOF
			return
		}
		// Shouldn't happen (resume uses Range), but serve full for safety.
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}))
}

func parseRangeStart(t *testing.T, rng string) int {
	t.Helper()
	rng = strings.TrimPrefix(rng, "bytes=")
	dash := strings.IndexByte(rng, '-')
	if dash < 0 {
		t.Fatalf("bad Range header %q", rng)
	}
	n, err := strconv.Atoi(rng[:dash])
	if err != nil {
		t.Fatalf("bad Range start in %q: %v", rng, err)
	}
	return n
}

// TestDownloadResumeEncrypted verifies that a torn encrypted download resumes via
// a Range request and yields bytes byte-identical to an uninterrupted download's
// decrypted output.
func TestDownloadResumeEncrypted(t *testing.T) {
	const trackID = "3135556"
	// Non-chunk-aligned length and drop offset exercise partial-chunk resume.
	cipher := make([]byte, 300000)
	for i := range cipher {
		cipher[i] = byte((i*7 + 3) % 251)
	}
	want, err := deezer.DecryptTrack(trackID, cipher)
	if err != nil {
		t.Fatal(err)
	}

	srv := flakyServer(t, cipher, 150000)
	defer srv.Close()

	s := newSource(&deezer.StreamPlan{CDNURL: srv.URL, TrackID: trackID, Format: "MP3_320", Encrypted: true}, 10000)
	s.download() // blocking; appends to s.sb and finishes
	got := drainStreamBuffer(s.sb)

	if !bytes.Equal(got, want) {
		t.Fatalf("resumed decrypt mismatch: got %d bytes, want %d bytes", len(got), len(want))
	}
	if e := s.lastErr(); e != "" {
		t.Fatalf("unexpected error after successful resume: %s", e)
	}
}

// TestDownloadResumePassthrough verifies the same for the plain (podcast) path.
func TestDownloadResumePassthrough(t *testing.T) {
	body := make([]byte, 250000)
	for i := range body {
		body[i] = byte((i*13 + 1) % 255)
	}
	srv := flakyServer(t, body, 90000)
	defer srv.Close()

	s := newSource(&deezer.StreamPlan{CDNURL: srv.URL, TrackID: "ep1", Format: "MP3", Encrypted: false}, 10000)
	s.download()
	got := drainStreamBuffer(s.sb)

	if !bytes.Equal(got, body) {
		t.Fatalf("resumed passthrough mismatch: got %d bytes, want %d bytes", len(got), len(body))
	}
}

// TestDownloadRefreshOnExpiry verifies that a 403 (expired signed URL) triggers
// plan.Refresh, and the download completes against the refreshed URL.
func TestDownloadRefreshOnExpiry(t *testing.T) {
	body := make([]byte, 120000)
	for i := range body {
		body[i] = byte(i % 255)
	}
	// good server serves the body in full.
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}))
	defer good.Close()
	// expired server always answers 403.
	expired := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "expired", http.StatusForbidden)
	}))
	defer expired.Close()

	var refreshed int32
	plan := &deezer.StreamPlan{CDNURL: expired.URL, TrackID: "t", Format: "MP3", Encrypted: false}
	plan.Refresh = func() (*deezer.StreamPlan, error) {
		atomic.AddInt32(&refreshed, 1)
		return &deezer.StreamPlan{CDNURL: good.URL, TrackID: "t", Format: "MP3", Encrypted: false}, nil
	}

	s := newSource(plan, 10000)
	s.download()
	got := drainStreamBuffer(s.sb)

	if atomic.LoadInt32(&refreshed) == 0 {
		t.Fatal("Refresh was never called on 403")
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("post-refresh body mismatch: got %d bytes, want %d bytes", len(got), len(body))
	}
}

// TestDownloadRefreshRejectsChangedPlan verifies that a 403-triggered Refresh
// whose plan came back with a different stream identity (here: the format
// changed at runtime) is NOT resumed into. The consumed offset and decryptor
// belong to the old stream, so splicing the refreshed one would corrupt audio;
// download must instead surface a clean error with nothing spliced.
func TestDownloadRefreshRejectsChangedPlan(t *testing.T) {
	body := make([]byte, 120000)
	for i := range body {
		body[i] = byte(i % 255)
	}
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}))
	defer good.Close()
	expired := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "expired", http.StatusForbidden)
	}))
	defer expired.Close()

	var refreshed int32
	plan := &deezer.StreamPlan{CDNURL: expired.URL, TrackID: "t", Format: "MP3_128", Encrypted: false}
	plan.Refresh = func() (*deezer.StreamPlan, error) {
		atomic.AddInt32(&refreshed, 1)
		// Quality switched between resolution and refresh: different Format.
		return &deezer.StreamPlan{CDNURL: good.URL, TrackID: "t", Format: "FLAC", Encrypted: false}, nil
	}

	s := newSource(plan, 10000)
	s.download()
	got := drainStreamBuffer(s.sb)

	if atomic.LoadInt32(&refreshed) == 0 {
		t.Fatal("Refresh was never called on 403")
	}
	if len(got) != 0 {
		t.Fatalf("changed-identity refresh must not be resumed into, but %d bytes were spliced", len(got))
	}
	if s.lastErr() == "" {
		t.Fatal("download should surface an error when the refreshed plan identity differs")
	}
}

// tornRawResponse hijacks the connection and writes a raw response whose body
// is shorter than the advertised Content-Length, then slams the connection so
// the client sees a torn mid-body read (io.ErrUnexpectedEOF).
func tornRawResponse(t *testing.T, w http.ResponseWriter, status, headers string, advertised int, body []byte) {
	t.Helper()
	hj, ok := w.(http.Hijacker)
	if !ok {
		t.Error("server does not support hijacking")
		return
	}
	conn, bufrw, err := hj.Hijack()
	if err != nil {
		t.Errorf("hijack: %v", err)
		return
	}
	fmt.Fprintf(bufrw, "HTTP/1.1 %s\r\n%sContent-Length: %d\r\n\r\n", status, headers, advertised)
	_, _ = bufrw.Write(body)
	_ = bufrw.Flush()
	_ = conn.Close()
}

// TestDownloadResumeRejectsWrongRangeStart verifies that a resume 206 whose
// Content-Range does not start exactly at the consumed offset is treated as a
// failed attempt (never spliced): the download retries and, with the server
// persistently mispositioned, surfaces an error while the buffer holds only
// the clean prefix.
func TestDownloadResumeRejectsWrongRangeStart(t *testing.T) {
	body := make([]byte, 200000)
	for i := range body {
		body[i] = byte((i*7 + 3) % 251)
	}
	const dropAt = 80000
	var served int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rng := r.Header.Get("Range"); rng != "" {
			// Broken/malicious resume: 206 whose body starts 1KB BEFORE the
			// requested offset.
			start := parseRangeStart(t, rng)
			wrong := start - 1024
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", wrong, len(body)-1, len(body)))
			w.Header().Set("Content-Length", strconv.Itoa(len(body)-wrong))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(body[wrong:])
			return
		}
		if atomic.AddInt32(&served, 1) == 1 {
			tornRawResponse(t, w, "200 OK", "", len(body), body[:dropAt])
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	s := newSource(&deezer.StreamPlan{CDNURL: srv.URL, TrackID: "ep-misrange", Format: "MP3", Encrypted: false}, 10000)
	s.download()
	got := drainStreamBuffer(s.sb)

	if !bytes.Equal(got, body[:dropAt]) {
		t.Fatalf("mismatched 206 was spliced: got %d bytes, want the clean %d-byte prefix", len(got), dropAt)
	}
	if s.lastErr() == "" {
		t.Fatal("expected an error after exhausting resume attempts against a mispositioned server")
	}
}

// TestDownloadResumeEntityChanged verifies the If-Range/validator protection on
// plain passthrough hosts: the first response carries an ETag; the host then
// ignores Range on resume and answers a full 200 for a DIFFERENT entity (new
// ETag). The old skip-splicing would silently mix two entities; now the resume
// must send If-Range and the download must fail cleanly with nothing spliced.
func TestDownloadResumeEntityChanged(t *testing.T) {
	bodyA := make([]byte, 150000)
	bodyB := make([]byte, 150000)
	for i := range bodyA {
		bodyA[i] = byte((i*13 + 1) % 255)
		bodyB[i] = byte((i*17 + 9) % 255)
	}
	const dropAt = 70000
	var served int32
	var sawIfRange atomic.Value // string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&served, 1) == 1 {
			tornRawResponse(t, w, "200 OK", "ETag: \"v1\"\r\n", len(bodyA), bodyA[:dropAt])
			return
		}
		// The entity was replaced between requests: ignore Range, answer a full
		// 200 with a new validator.
		sawIfRange.Store(r.Header.Get("If-Range"))
		w.Header().Set("ETag", `"v2"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(bodyB)))
		_, _ = w.Write(bodyB)
	}))
	defer srv.Close()

	s := newSource(&deezer.StreamPlan{CDNURL: srv.URL, TrackID: "ep-mutated", Format: "MP3", Encrypted: false}, 10000)
	s.download()
	got := drainStreamBuffer(s.sb)

	if v, _ := sawIfRange.Load().(string); v != `"v1"` {
		t.Fatalf("resume GET should send If-Range with the stored validator, got %q", v)
	}
	if !bytes.Equal(got, bodyA[:dropAt]) {
		t.Fatalf("changed entity was spliced: got %d bytes, want the clean %d-byte prefix of the first entity", len(got), dropAt)
	}
	if s.lastErr() == "" {
		t.Fatal("expected a validator-mismatch error, got success")
	}
}

// TestDownloadTrickleServerTerminates verifies the retry budget is only
// refreshed by meaningful progress: a host that drips one byte per connection
// and then drops must exhaust maxResumeAttempts and surface an error within a
// bounded number of requests instead of looping forever (streamHTTPClient
// deliberately has no overall timeout).
func TestDownloadTrickleServerTerminates(t *testing.T) {
	const total = 1 << 20
	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reqs, 1)
		start := 0
		if rng := r.Header.Get("Range"); rng != "" {
			start = parseRangeStart(t, rng)
		}
		one := []byte{byte(start % 251)}
		if start > 0 {
			hdr := fmt.Sprintf("Content-Range: bytes %d-%d/%d\r\n", start, total-1, total)
			tornRawResponse(t, w, "206 Partial Content", hdr, total-start, one)
			return
		}
		tornRawResponse(t, w, "200 OK", "", total, one)
	}))
	defer srv.Close()

	s := newSource(&deezer.StreamPlan{CDNURL: srv.URL, TrackID: "drip", Format: "MP3", Encrypted: false}, 10000)
	done := make(chan struct{})
	go func() {
		s.download()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("trickle download did not terminate: 1-byte progress kept resetting the retry budget")
	}
	if s.lastErr() == "" {
		t.Fatal("trickle download should surface an error")
	}
	if n := atomic.LoadInt32(&reqs); n > maxResumeAttempts+1 {
		t.Fatalf("trickle server received %d requests, want at most %d", n, maxResumeAttempts+1)
	}
}
