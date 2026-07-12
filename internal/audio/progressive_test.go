package audio

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/deezer"
)

// throttledServer streams body in fixed-size chunks with a delay between each,
// simulating a slow network so a progressive decoder can be observed producing
// PCM before the whole file has arrived.
func throttledServer(body []byte, chunk int, delay time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		for off := 0; off < len(body); off += chunk {
			end := off + chunk
			if end > len(body) {
				end = len(body)
			}
			_, _ = w.Write(body[off:end])
			if fl != nil {
				fl.Flush()
			}
			time.Sleep(delay)
		}
	}))
}

// TestProgressiveStartMP3 confirms MP3 decoding begins (PCM lands in the ring)
// before the full download completes, against a throttled server. Previously the
// decoder waited for the whole file (waitDone) before emitting any audio.
func TestProgressiveStartMP3(t *testing.T) {
	mp3 := makeMP3(1400) // ~584KB, comfortably above the 256KB watermark floor
	srv := throttledServer(mp3, 32*1024, 10*time.Millisecond)
	defer srv.Close()

	s := newSource(&deezer.StreamPlan{CDNURL: srv.URL, Format: "MP3", Encrypted: false}, 15000)
	defer s.kill()
	go s.download()
	go s.decode()

	deadline := time.Now().Add(3 * time.Second)
	progressive := false
	for time.Now().Before(deadline) {
		if s.ring.buffered() > 0 {
			// PCM is decoding; the download must not have finished yet for this to
			// count as a progressive (pre-completion) start.
			if !sbDone(s.sb) {
				progressive = true
			}
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !progressive {
		t.Fatal("decoder did not produce PCM before the download completed (not progressive)")
	}
}

// TestProgressiveWatermarkReturnsEarly checks the streamBuffer watermark returns
// once the margin is buffered without waiting for the whole stream.
func TestProgressiveWatermarkReturnsEarly(t *testing.T) {
	sb := newStreamBuffer()
	done := make(chan struct{})
	go func() {
		sb.waitWatermark(256 * 1024)
		close(done)
	}()
	sb.append(make([]byte, 256*1024)) // reach the watermark; stream NOT finished
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("waitWatermark did not return after the margin was buffered")
	}
	if sbDone(sb) {
		t.Fatal("watermark returned only because the stream finished; expected early return")
	}
}
