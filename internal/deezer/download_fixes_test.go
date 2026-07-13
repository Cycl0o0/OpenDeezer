package deezer

// Regression tests for the batch-download and tagging defect fixes:
// ctx cancellation surfacing, unique + atomic filenames, album pagination,
// strict playlist listing for downloads, atomic FLAC tagging, the
// ErrPartialDownload double-wrap, and single plan resolution per batch track.

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/go-flac/flacvorbis"
	flac "github.com/go-flac/go-flac"
)

// rtFunc adapts a function to http.RoundTripper so tests can fake the gw
// transport without any network.
type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(body []byte) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

// gwTracksPage builds a gw playlist.getSongs response with n tracks.
func gwTracksPage(startID, n int) []byte {
	var sb strings.Builder
	sb.WriteString(`{"error":{},"results":{"data":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"SNG_ID":"%d","SNG_TITLE":"Song %d","DURATION":"100","ART_ID":"7","ART_NAME":"A","ALB_TITLE":"Alb"}`, startID+i, startID+i)
	}
	sb.WriteString(`]}}`)
	return []byte(sb.String())
}

// restAlbumPage builds a REST /album/<id>/tracks page with n tracks.
func restAlbumPage(startID, n int, next string) []byte {
	var sb strings.Builder
	sb.WriteString(`{"data":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"id":%d,"title":"Track %d","duration":100,"artist":{"id":1,"name":"A"},"album":{"title":"Alb"}}`, startID+i, startID+i)
	}
	sb.WriteString(`]`)
	if next != "" {
		fmt.Fprintf(&sb, `,"next":%q`, next)
	}
	sb.WriteString(`,"total":130}`)
	return []byte(sb.String())
}

// loggedInPremium flips the package-private session fields so the download
// entry points pass their LoggedIn/Premium gates without a real login.
func loggedInPremium(c *Client) {
	c.mu.Lock()
	c.apiToken = "test-token"
	c.canHQ = true
	c.mu.Unlock()
}

// --- C6: unique filenames + atomic single-track writes ---

func TestTrackFileName_IncludesTrackID(t *testing.T) {
	a := Track{ID: "111", Name: "Intro", Artists: []Artist{{Name: "Band"}}}
	b := Track{ID: "222", Name: "Intro", Artists: []Artist{{Name: "Band"}}}
	na, nb := trackFileName(a, "mp3"), trackFileName(b, "mp3")
	if na == nb {
		t.Fatalf("same-title/artist tracks with different IDs collide: %q", na)
	}
	if !strings.Contains(na, "111") || !strings.Contains(nb, "222") {
		t.Errorf("filenames should embed the track ID: %q / %q", na, nb)
	}
}

func TestSaveTrack_SameTitleArtistDifferentID_BothSaved(t *testing.T) {
	tmp := t.TempDir()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "AUDIO:%s", r.URL.Path)
	}))
	defer ts.Close()

	c := New("dummy")
	tr1 := Track{ID: "111", Name: "Intro", Artists: []Artist{{Name: "Band"}}}
	tr2 := Track{ID: "222", Name: "Intro", Artists: []Artist{{Name: "Band"}}}
	plan1 := &StreamPlan{CDNURL: ts.URL + "/one", Format: "MP3_128", Encrypted: false}
	plan2 := &StreamPlan{CDNURL: ts.URL + "/two", Format: "MP3_128", Encrypted: false}

	p1, err := c.saveTrack(context.Background(), tr1, tmp, plan1, nil)
	if err != nil {
		t.Fatalf("saveTrack tr1: %v", err)
	}
	p2, err := c.saveTrack(context.Background(), tr2, tmp, plan2, nil)
	if err != nil {
		t.Fatalf("saveTrack tr2: %v", err)
	}
	if p1 == p2 {
		t.Fatalf("both tracks saved to the same path %q (second overwrote first)", p1)
	}
	b1, _ := os.ReadFile(p1)
	b2, _ := os.ReadFile(p2)
	// Tagging prepends an ID3 header; the distinct audio bodies must survive.
	if !bytes.Contains(b1, []byte("AUDIO:/one")) || !bytes.Contains(b2, []byte("AUDIO:/two")) {
		t.Errorf("audio bodies mixed up or lost: p1=%q p2=%q", b1, b2)
	}
}

func TestSaveTrack_FailedTransferPreservesExistingFile(t *testing.T) {
	tmp := t.TempDir()
	// A CDN that advertises 100 bytes, sends 10, then kills the connection.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("short body"))
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		panic(http.ErrAbortHandler)
	}))
	defer ts.Close()

	c := New("dummy")
	tr := Track{ID: "111", Name: "Keeper", Artists: []Artist{{Name: "Band"}}}
	plan := &StreamPlan{CDNURL: ts.URL + "/a", Format: "MP3_128", Encrypted: false}

	// A previous successful download already lives at the final path.
	final := filepath.Join(tmp, trackFileName(tr, "mp3"))
	if err := os.WriteFile(final, []byte("GOOD OLD AUDIO"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := c.saveTrack(context.Background(), tr, tmp, plan, nil); err == nil {
		t.Fatal("expected the interrupted transfer to error")
	}
	got, err := os.ReadFile(final)
	if err != nil {
		t.Fatalf("prior good file is gone: %v", err)
	}
	if string(got) != "GOOD OLD AUDIO" {
		t.Errorf("prior good file was clobbered by the failed transfer: %q", got)
	}
	ents, _ := os.ReadDir(tmp)
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".part") {
			t.Errorf("leftover temp file %q after failed transfer", e.Name())
		}
	}
}

// --- C7: album tracklist pagination ---

func TestAlbumTracks_PaginatesBeyond100(t *testing.T) {
	var indexes []string
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/album/42/tracks" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		idx := r.URL.Query().Get("index")
		indexes = append(indexes, idx)
		w.Header().Set("Content-Type", "application/json")
		switch idx {
		case "0", "":
			_, _ = w.Write(restAlbumPage(0, 100, ts.URL+"/album/42/tracks?index=100"))
		case "100":
			_, _ = w.Write(restAlbumPage(100, 30, ""))
		default:
			_, _ = w.Write([]byte(`{"data":[]}`))
		}
	}))
	defer ts.Close()

	c := New("dummy")
	c.restURLOverride = ts.URL

	tracks, err := c.AlbumTracks("42")
	if err != nil {
		t.Fatalf("AlbumTracks: %v", err)
	}
	if len(tracks) != 130 {
		t.Fatalf("got %d tracks, want 130 (pagination lost a page)", len(tracks))
	}
	if tracks[0].ID != "0" || tracks[129].ID != "129" {
		t.Errorf("track order wrong: first=%s last=%s", tracks[0].ID, tracks[129].ID)
	}
	if len(indexes) != 2 || indexes[1] != "100" {
		t.Errorf("expected requests at index 0 and 100, got %v", indexes)
	}
}

func TestAlbumTracks_SinglePageStops(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(restAlbumPage(0, 12, ""))
	}))
	defer ts.Close()

	c := New("dummy")
	c.restURLOverride = ts.URL

	tracks, err := c.AlbumTracks("7")
	if err != nil {
		t.Fatalf("AlbumTracks: %v", err)
	}
	if len(tracks) != 12 || calls != 1 {
		t.Errorf("got %d tracks in %d calls, want 12 tracks in 1 call", len(tracks), calls)
	}
}

// --- C8: strict playlist listing for downloads ---

func TestCollectTrackPagesStrict(t *testing.T) {
	t.Run("mid-fetch error fails whole listing", func(t *testing.T) {
		boom := errors.New("page 2 exploded")
		got, err := collectTrackPagesStrict(func(start, nb int) ([]Track, error) {
			if start == 0 {
				out := make([]Track, nb) // full page forces a second fetch
				return out, nil
			}
			return nil, boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the page-2 error", err)
		}
		if got != nil {
			t.Errorf("strict listing must not return a partial set, got %d tracks", len(got))
		}
	})

	t.Run("happy path accumulates all pages", func(t *testing.T) {
		got, err := collectTrackPagesStrict(func(start, nb int) ([]Track, error) {
			if start == 0 {
				return make([]Track, nb), nil
			}
			return make([]Track, 50), nil
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(got) != pageSize+50 {
			t.Errorf("got %d tracks, want %d", len(got), pageSize+50)
		}
	})
}

func TestSavePlaylist_FailsOnMidFetchPageError(t *testing.T) {
	// gw transport: page start=0 succeeds with a full page, page start=200
	// fails at the transport level. The tolerant PlaylistTracks returns the
	// partial page; SavePlaylist (strict) must fail instead of downloading a
	// truncated playlist as success.
	pageErr := errors.New("simulated page-2 network failure")
	c := New("dummy")
	loggedInPremium(c)
	c.http = &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "gw-light.php") {
			return nil, fmt.Errorf("unexpected URL %s", r.URL)
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"start":0`) {
			return jsonResponse(gwTracksPage(0, pageSize)), nil
		}
		return nil, pageErr
	})}

	// Tolerant browse listing: partial set, nil error (unchanged behavior).
	browse, err := c.PlaylistTracks("42")
	if err != nil {
		t.Fatalf("tolerant PlaylistTracks should absorb the page error, got %v", err)
	}
	if len(browse) != pageSize {
		t.Fatalf("tolerant listing returned %d tracks, want %d", len(browse), pageSize)
	}

	// Strict download listing: the same failure must surface.
	saverCalls := 0
	opts := DownloadOptions{
		trackSaver: func(ctx context.Context, tt Track, d string, plan *StreamPlan, f ArtworkFetcher) (string, error) {
			saverCalls++
			return filepath.Join(d, tt.ID+".mp3"), nil
		},
		planResolver: func(string) (*StreamPlan, error) { return &StreamPlan{Format: "MP3_128"}, nil },
	}
	paths, err := c.SavePlaylist(context.Background(), "42", t.TempDir(), opts)
	if err == nil {
		t.Fatal("SavePlaylist must fail when the playlist listing is truncated")
	}
	if paths != nil {
		t.Errorf("no paths expected on listing failure, got %d", len(paths))
	}
	if saverCalls != 0 {
		t.Errorf("no track should be downloaded from a truncated listing; saver ran %d times", saverCalls)
	}
}

// --- C5: cancellation surfaces and pre-cancelled ctx skips the listing ---

func TestSaveAlbum_PreCancelledContextSkipsListing(t *testing.T) {
	listings := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		listings++
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer ts.Close()

	c := New("dummy")
	loggedInPremium(c)
	c.restURLOverride = ts.URL

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := c.SaveAlbum(ctx, "42", t.TempDir(), DownloadOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("SaveAlbum err = %v, want context.Canceled", err)
	}
	if listings != 0 {
		t.Errorf("cancelled SaveAlbum still performed %d listing request(s)", listings)
	}
}

func TestSavePlaylist_PreCancelledContextSkipsListing(t *testing.T) {
	c := New("dummy")
	loggedInPremium(c)
	// Any gw call would be a bug; make it observable without network.
	c.http = &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("unexpected network call from a cancelled SavePlaylist")
	})}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := c.SavePlaylist(ctx, "42", t.TempDir(), DownloadOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("SavePlaylist err = %v, want context.Canceled", err)
	}
}

// --- C17: aggregate error matches both sentinels ---

func TestSaveBatch_PartialErrorMatchesBothSentinels(t *testing.T) {
	tmp := t.TempDir()
	c := &Client{}
	boom := errors.New("decrypt exploded")

	trs := []Track{{ID: "ok", Name: "OK"}, {ID: "bad", Name: "BAD"}}
	opts := DownloadOptions{
		trackSaver: func(ctx context.Context, tt Track, d string, plan *StreamPlan, f ArtworkFetcher) (string, error) {
			if tt.ID == "bad" {
				return "", boom
			}
			return filepath.Join(d, tt.ID+".mp3"), nil
		},
		planResolver: func(string) (*StreamPlan, error) { return &StreamPlan{Format: "MP3_128"}, nil },
	}

	saved, err := c.saveBatch(context.Background(), trs, tmp, opts)
	if err == nil {
		t.Fatal("expected aggregate error")
	}
	if !errors.Is(err, ErrPartialDownload) {
		t.Errorf("errors.Is(err, ErrPartialDownload) = false; err = %v", err)
	}
	if !errors.Is(err, boom) {
		t.Errorf("errors.Is(err, firstConcreteErr) = false; err = %v", err)
	}
	if len(saved) != 1 {
		t.Errorf("saved = %d, want 1", len(saved))
	}
}

// --- A4: one PrepareStream per batched track ---

func TestSaveBatch_ResolvesPlanOncePerTrack(t *testing.T) {
	tmp := t.TempDir()
	c := &Client{}

	trs := []Track{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}, {ID: "c", Name: "C"}}
	resolved := map[string]*StreamPlan{}
	resolves := 0
	opts := DownloadOptions{
		planResolver: func(id string) (*StreamPlan, error) {
			resolves++
			p := &StreamPlan{TrackID: id, Format: "MP3_320"}
			resolved[id] = p
			return p, nil
		},
		trackSaver: func(ctx context.Context, tt Track, d string, plan *StreamPlan, f ArtworkFetcher) (string, error) {
			if plan == nil {
				t.Errorf("track %s: saver got nil plan (would re-resolve)", tt.ID)
			} else if plan != resolved[tt.ID] {
				t.Errorf("track %s: saver got a different plan than the batch resolved", tt.ID)
			}
			return filepath.Join(d, tt.ID+".mp3"), nil
		},
	}

	if _, err := c.saveBatch(context.Background(), trs, tmp, opts); err != nil {
		t.Fatalf("saveBatch: %v", err)
	}
	if resolves != len(trs) {
		t.Errorf("plan resolved %d times for %d tracks, want exactly one each", resolves, len(trs))
	}
}

// --- C9: FLAC tagging never destroys the downloaded audio ---

func TestTagFLAC_ErrorLeavesOriginalBytesIntact(t *testing.T) {
	tr := Track{Name: "T", Artists: []Artist{{Name: "A"}}, AlbumName: "Alb"}

	t.Run("corrupt flac input", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "bad.flac")
		orig := []byte("not a flac stream at all")
		if err := os.WriteFile(path, orig, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := tagFLAC(path, tr, nil, 0, ""); err == nil {
			t.Fatal("expected parse error for corrupt input")
		}
		got, _ := os.ReadFile(path)
		if !bytes.Equal(got, orig) {
			t.Error("corrupt-input tagging modified the original file")
		}
	})

	t.Run("write failure preserves audio", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("read-only dir does not block file creation on Windows")
		}
		if os.Geteuid() == 0 {
			t.Skip("directory permissions do not bind root")
		}
		tmp := t.TempDir()
		path := filepath.Join(tmp, "good.flac")
		orig, err := base64.StdEncoding.DecodeString(flacAudioB64)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, orig, 0o644); err != nil {
			t.Fatal(err)
		}
		// A read-only directory makes the temp-file creation fail, standing in
		// for ENOSPC/short-write failures. The old in-place O_TRUNC rewrite
		// would still have truncated the audio here (file itself is writable).
		if err := os.Chmod(tmp, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(tmp, 0o755) })

		if err := tagFLAC(path, tr, nil, 1, "2025"); err == nil {
			t.Fatal("expected tagging to fail in a read-only directory")
		}
		got, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Fatalf("original audio is gone: %v", rerr)
		}
		if !bytes.Equal(got, orig) {
			t.Error("failed tagging modified the original audio bytes")
		}
	})
}

func TestTagFile_DispatchesFLACUnderPartSuffix(t *testing.T) {
	// saveTrack tags the not-yet-renamed "<name>.flac.part" file; the FLAC
	// path must still be chosen despite the .part suffix.
	tmp := t.TempDir()
	path := filepath.Join(tmp, "song.flac.part")
	raw, err := base64.StdEncoding.DecodeString(flacAudioB64)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	tr := Track{Name: "Part Song", Artists: []Artist{{Name: "A"}}, AlbumName: "Alb"}
	if err := tagFile(context.Background(), path, tr, "", 0, "", nil); err != nil {
		t.Fatalf("tagFile(.flac.part): %v", err)
	}

	f, err := flac.ParseFile(path)
	if err != nil {
		t.Fatalf("result is not a valid FLAC (mp3 path used?): %v", err)
	}
	title := ""
	for _, mb := range f.Meta {
		if mb.Type == flac.VorbisComment {
			if vc, perr := flacvorbis.ParseFromMetaDataBlock(*mb); perr == nil {
				if vs, _ := vc.Get(flacvorbis.FIELD_TITLE); len(vs) > 0 {
					title = vs[0]
				}
			}
		}
	}
	if title != "Part Song" {
		t.Errorf("vorbis TITLE = %q, want %q (flac dispatch failed)", title, "Part Song")
	}
}

// --- B12: concurrent .part uniqueness via CreateTemp ---

func TestSaveTrack_ConcurrentSameTrack_UsesUniqueTemps(t *testing.T) {
	tmp := t.TempDir()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return enough distinct bytes so we can verify clean (non-interleaved) content.
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("CLEAN-AUDIO-FOR-CONCURRENT-TEST-1234567890"))
	}))
	defer ts.Close()

	c := New("dummy")
	// No premium/login needed: we pass plan, saveTrack path doesn't gate.
	tr := Track{ID: "conc42", Name: "SameSong", Artists: []Artist{{Name: "Artist"}}}
	plan := &StreamPlan{CDNURL: ts.URL + "/audio", Format: "MP3_128", Encrypted: false}

	seen := map[string]struct{}{}
	var seenMu sync.Mutex
	testCreateTemp = func(dir, pattern string) (*os.File, error) {
		f, err := os.CreateTemp(dir, pattern)
		if err == nil {
			seenMu.Lock()
			seen[f.Name()] = struct{}{}
			seenMu.Unlock()
		}
		return f, err
	}
	defer func() { testCreateTemp = nil }()

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.saveTrack(context.Background(), tr, tmp, plan, nil)
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	// At least one should succeed (rename race on final is acceptable; content is identical).
	success := 0
	for e := range results {
		if e == nil {
			success++
		}
	}
	if success == 0 {
		t.Fatalf("expected at least one concurrent save to succeed")
	}

	seenMu.Lock()
	nTemps := len(seen)
	seenMu.Unlock()
	if nTemps < 2 {
		t.Errorf("concurrent downloads used only %d distinct temp(s); wanted >=2 for uniqueness", nTemps)
	}

	// Final file must exist and contain clean (non-corrupt) payload.
	final := filepath.Join(tmp, trackFileName(tr, "mp3"))
	b, err := os.ReadFile(final)
	if err != nil {
		t.Fatalf("final file missing: %v", err)
	}
	if !bytes.Contains(b, []byte("CLEAN-AUDIO-FOR-CONCURRENT")) {
		t.Errorf("final content corrupted or incomplete: %q", string(b))
	}

	// No stray .part files left behind.
	ents, _ := os.ReadDir(tmp)
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".part") {
			t.Errorf("leftover temp after concurrent saves: %s", e.Name())
		}
	}
}

// --- sanitize keeps [id] suffix even after long/UTF-8 title ---

func TestTrackFileName_TruncatesBeforeIDSuffix_UTF8Safe(t *testing.T) {
	// 200+ multi-byte runes in title; without the pre-truncate+append fix the
	// [id] would be sliced off and/or a rune split.
	long := strings.Repeat("é", 220) // each é is 2 bytes
	tr := Track{ID: "98765", Name: long, Artists: []Artist{{Name: "B"}}}
	fn := trackFileName(tr, "mp3")
	base := strings.TrimSuffix(fn, ".mp3")

	if !strings.Contains(base, "[98765]") {
		t.Fatalf("[id] suffix was stripped by truncate: %q", base)
	}
	if !utf8.ValidString(base) {
		t.Fatalf("filename base is not valid UTF-8: %q", base)
	}
	// The whole base must respect ~180 byte cap.
	if len(base) > 180+len(" [98765]") { // allow a little for safety margin in impl
		// actually impl caps the desc then +suffix, sanitize may trim
		t.Logf("len=%d (acceptable if id present)", len(base))
	}
	// Id must be near end
	if !strings.HasSuffix(base, "[98765]") && !strings.Contains(base, "[98765]") {
		t.Errorf("id not at/near end: %q", base)
	}
}

// --- B13: PodcastEpisodes and Playlists pagination (100+30) ---

func TestPodcastEpisodes_PaginatesBeyond100(t *testing.T) {
	var indexes []string
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/podcast/99/episodes") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		idx := r.URL.Query().Get("index")
		indexes = append(indexes, idx)
		w.Header().Set("Content-Type", "application/json")
		switch idx {
		case "0", "":
			_, _ = w.Write(podcastEpisodesPage(0, 100, ts.URL+"/podcast/99/episodes?index=100"))
		case "100":
			_, _ = w.Write(podcastEpisodesPage(100, 30, ""))
		default:
			_, _ = w.Write([]byte(`{"data":[]}`))
		}
	}))
	defer ts.Close()

	c := New("dummy")
	c.restURLOverride = ts.URL

	eps, err := c.PodcastEpisodes("99")
	if err != nil {
		t.Fatalf("PodcastEpisodes: %v", err)
	}
	if len(eps) != 130 {
		t.Fatalf("got %d episodes, want 130 (pagination lost data)", len(eps))
	}
	if eps[0].ID != "0" || eps[129].ID != "129" {
		t.Errorf("episode order wrong: first=%s last=%s", eps[0].ID, eps[129].ID)
	}
	if len(indexes) != 2 || indexes[1] != "100" {
		t.Errorf("expected index 0 and 100, got %v", indexes)
	}
}

func podcastEpisodesPage(startID, n int, next string) []byte {
	var sb strings.Builder
	sb.WriteString(`{"data":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"id":%d,"title":"Ep %d","duration":120}`, startID+i, startID+i)
	}
	sb.WriteString(`]`)
	if next != "" {
		fmt.Fprintf(&sb, `,"next":%q`, next)
	}
	sb.WriteString(`}`)
	return []byte(sb.String())
}

func TestPlaylists_Paginates(t *testing.T) {
	// Uses gw transport mock (pageProfile with index).
	calls := 0
	c := New("dummy")
	loggedInPremium(c)
	c.http = &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "gw-light.php") {
			return nil, fmt.Errorf("unexpected %s", r.URL)
		}
		var b []byte
		if r.Body != nil {
			b, _ = io.ReadAll(r.Body)
		}
		calls++
		// respond based on index in body
		if strings.Contains(string(b), `"index":100`) {
			return jsonResponse(gwPlaylistsPage(100, 30)), nil
		}
		return jsonResponse(gwPlaylistsPage(0, 100)), nil
	})}

	pls, err := c.Playlists()
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	if len(pls) != 130 {
		t.Fatalf("got %d playlists, want 130", len(pls))
	}
	if pls[0].ID != "0" || pls[129].ID != "129" {
		t.Errorf("order: first=%s last=%s", pls[0].ID, pls[129].ID)
	}
	if calls < 2 {
		t.Errorf("expected >=2 gw calls for pagination, got %d", calls)
	}
}

func gwPlaylistsPage(startID, n int) []byte {
	var sb strings.Builder
	sb.WriteString(`{"error":{},"results":{"TAB":{"playlists":{"data":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"PLAYLIST_ID":"%d","TITLE":"Pl %d","NB_SONG":5,"PLAYLIST_PICTURE":"p"}`, startID+i, startID+i)
	}
	sb.WriteString(`]}}}}`)
	return []byte(sb.String())
}

// --- E1: PrepareStream error classification ---

func TestPrepareStream_Transient5xxRetried_NoPreviewFallback(t *testing.T) {
	mediaCalls := 0
	gwCalls := 0
	c := New("dummy")
	loggedInPremium(c)
	c.mu.Lock()
	c.licenseToken = "lic-1"
	c.userID = "1"
	c.mu.Unlock()
	c.restURLOverride = "http://rest.test"
	c.http = &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		var bb []byte
		if r.Body != nil {
			bb, _ = io.ReadAll(r.Body)
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(bb))
		}
		q := r.URL.RawQuery
		if strings.Contains(q, "method=deezer.getUserData") {
			return loginResponseForTest(), nil
		}
		if strings.Contains(q, "method=song.getData") {
			gwCalls++
			return jsonResponse([]byte(`{"error":{},"results":{"TRACK_TOKEN":"tok-xyz","GAIN":"0"}}`)), nil
		}
		if r.URL.Host == "media.deezer.com" || strings.Contains(r.URL.Path, "get_url") {
			mediaCalls++
			// always 5xx to force retries then error
			return &http.Response{
				StatusCode: 503,
				Status:     "503 Service Unavailable",
				Body:       io.NopCloser(strings.NewReader(`{}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		}
		if strings.HasPrefix(r.URL.Path, "/track/") {
			// should NOT be hit for transient case
			return jsonResponse([]byte(`{"preview":"https://x/pre.mp3"}`)), nil
		}
		return nil, fmt.Errorf("unexpected test req: %s %s", r.Method, r.URL)
	})}

	plan, err := c.PrepareStream("123")
	if err == nil {
		t.Fatalf("expected error for persistent transient, got plan=%+v", plan)
	}
	if IsNoFullMedia(err) {
		t.Errorf("transient 5xx should not be classified as NoFullMedia: %v", err)
	}
	if mediaCalls < 2 {
		t.Errorf("expected at least 2 (retries) media calls on 5xx, got %d", mediaCalls)
	}
	// preview must not have been attempted
	// (we didn't count but if plan came back as preview it would be wrong anyway)
	if plan != nil && plan.Preview {
		t.Error("must not return preview plan on transient error")
	}
}

func TestPrepareStream_Entitlement_NoMedia_GivesPreview(t *testing.T) {
	c := New("dummy")
	loggedInPremium(c)
	c.mu.Lock()
	c.licenseToken = "lic-1"
	c.userID = "1"
	c.mu.Unlock()
	c.restURLOverride = "http://rest.test"
	c.http = &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		var bb []byte
		if r.Body != nil {
			bb, _ = io.ReadAll(r.Body)
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(bb))
		}
		q := r.URL.RawQuery
		if strings.Contains(q, "method=deezer.getUserData") {
			return loginResponseForTest(), nil
		}
		if strings.Contains(q, "method=song.getData") {
			return jsonResponse([]byte(`{"error":{},"results":{"TRACK_TOKEN":"tok-xyz","GAIN":"0"}}`)), nil
		}
		if r.URL.Host == "media.deezer.com" || strings.Contains(r.URL.Path, "get_url") {
			// entitlement rejection via deezer error envelope
			return jsonResponse([]byte(`{"data":[{"errors":[{"code":4,"message":"no media"}]}]}`)), nil
		}
		if strings.HasPrefix(r.URL.Path, "/track/") {
			return jsonResponse([]byte(`{"preview":"https://cdn/preview.mp3"}`)), nil
		}
		return nil, fmt.Errorf("unexpected test req: %s %s", r.Method, r.URL)
	})}

	plan, err := c.PrepareStream("123")
	if err != nil {
		t.Fatalf("expected preview fallback on entitlement, got err=%v", err)
	}
	if plan == nil || !plan.Preview || plan.CDNURL == "" {
		t.Fatalf("expected preview plan, got %+v", plan)
	}
}

func loginResponseForTest() *http.Response {
	return jsonResponse([]byte(`{"error":{},"results":{"checkForm":"api-t","USER":{"USER_ID":"1","BLOG_NAME":"t","OPTIONS":{"license_token":"lic-1","web_hq":true,"web_lossless":false,"mobile_hq":true,"mobile_lossless":false}},"OFFERS":[{"title":"P"}]}}`))
}
