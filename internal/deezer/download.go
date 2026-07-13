package deezer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	odlog "github.com/Cycl0o0/OpenDeezer/v2/internal/log"
)

// ErrPremiumRequired is returned by SaveTrack when the logged-in account is not
// entitled to full-track streaming (Deezer Free). Downloads save the full,
// decrypted track, which a free account cannot resolve — so the feature is
// gated to paid plans. Callers use errors.Is to show a "requires a paid plan"
// hint and hide the download action.
var ErrPremiumRequired = errors.New("downloads require a paid Deezer plan")

// ErrPreviewOnly is returned by SaveTrack when the only source Deezer offers for
// a track is the public 30-second preview (e.g. the track is geo-restricted or
// otherwise unavailable at full length for this account). We refuse to write a
// 30-second clip to disk under a full-track filename.
var ErrPreviewOnly = errors.New("full track not available for download")

// IsPremiumRequired reports whether err is the premium-required download gate.
func IsPremiumRequired(err error) bool { return errors.Is(err, ErrPremiumRequired) }

// IsPreviewOnly reports whether err is the preview-only download refusal.
func IsPreviewOnly(err error) bool { return errors.Is(err, ErrPreviewOnly) }

// DownloadProgress reports status for one track in a batch (SaveAlbum / SavePlaylist).
// It is delivered to the optional Progress callback even for skipped or errored tracks.
type DownloadProgress struct {
	Index   int // 1-based index in the batch
	Total   int
	TrackID string
	Title   string
	Err     error // per-track error (nil on success or skip)
}

// DownloadOptions tunes batch downloads. All fields are optional.
type DownloadOptions struct {
	SkipExisting bool
	// Progress is invoked after each track (success, skip, or error). Safe to be nil.
	Progress func(DownloadProgress)
	// ArtworkFetcher overrides cover art retrieval (for tests; nil = default Deezer CDN).
	ArtworkFetcher ArtworkFetcher

	// Concurrency is the maximum number of tracks to download in parallel
	// inside SaveAlbum/SavePlaylist (bounded worker pool). <=0 selects the
	// default (3). Adding the field keeps all prior call sites compatible.
	Concurrency int

	// trackSaver is internal hook for tests (same package) to inject a fake
	// downloader. Zero means use the real client-backed implementation.
	trackSaver trackDownloadFunc

	// planResolver is internal hook for tests (same package) to supply
	// *StreamPlan for filename/ext/preview decisions without calling
	// c.PrepareStream (no network in tests).
	planResolver planResolverFunc
}

// trackDownloadFunc is the signature for the injectable per-track saver used
// by batch code. Same package only (tests). plan is the already-resolved
// stream plan when the caller has one (batch code resolves it for the
// skip-existing filename check); nil means the saver resolves it itself.
type trackDownloadFunc func(ctx context.Context, t Track, dir string, plan *StreamPlan, fetch ArtworkFetcher) (string, error)

// planResolverFunc is the signature for the injectable plan getter used by
// batch pre-checks for filename and preview decisions. Same package only.
type planResolverFunc func(string) (*StreamPlan, error)

// testCreateTemp is a test-only hook (same package) so B12 concurrency tests
// can observe the distinct temp paths created by concurrent saveTrack calls.
// Production always uses os.CreateTemp when this is nil.
var testCreateTemp func(dir, pattern string) (*os.File, error)

// downloadUserAgent matches the streaming user agent so the CDN treats download
// and playback transfers identically.
const downloadUserAgent = userAgent

// downloadClient is a dedicated client for CDN transfers. Unlike the session
// client it bounds how long a stalled server may keep a transfer alive: dial and
// TLS-handshake timeouts guard connection setup (ResponseHeaderTimeout does NOT
// cover setup — it only starts once the request is written), ResponseHeaderTimeout
// guards the initial response, and IdleConnTimeout bounds reused connections.
// Bodies can be arbitrarily long, so total cancellation is left to the context.
var downloadClient = &http.Client{
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	},
}

// Resume tuning for resilient CDN downloads (mirrors audio/player.go values
// so behavior is consistent between streaming and Save* paths).
const (
	maxResumeAttempts  = 3
	maxRefreshAttempts = 2
	resumeBackoff      = 300 * time.Millisecond
	minResumeProgress  = 64 * 1024
)

// DownloadTrack fetches and decrypts a Deezer stream to w. plan must come from
// [Client.PrepareStream] (tracks) or [Client.PodcastEpisodeStream] (episodes,
// which are unencrypted). The bytes written form a valid MP3 or FLAC file.
func DownloadTrack(plan *StreamPlan, w io.Writer) error {
	return DownloadTrackContext(context.Background(), plan, w)
}

// DownloadTrackContext is [DownloadTrack] with cancellation. Cancelling ctx
// aborts the in-flight CDN read and unblocks the copy.
//
// The implementation is resilient: it performs HTTP Range resume (with
// If-Range validator) on mid-body connection drops, a few retries + backoff,
// calls plan.Refresh on 403/410 to obtain a fresh URL (validating that the
// refreshed plan describes the identical stream), and verifies that the final
// received byte count matches the declared Content-Length (rejecting short
// files so callers can redownload).
func DownloadTrackContext(ctx context.Context, plan *StreamPlan, w io.Writer) error {
	_, _, err := downloadToWriter(ctx, plan, w)
	return err
}

// downloadToWriter is the resilient fetcher used by Download* and by the
// Save* paths (via saveTrack). It returns bytes written to w, the last-seen
// content length (for the short-file check), and error.
func downloadToWriter(ctx context.Context, plan *StreamPlan, w io.Writer) (written, contentLen int64, err error) {
	if plan == nil || plan.CDNURL == "" {
		return 0, 0, fmt.Errorf("download: empty stream plan")
	}

	var dec *StripeDecryptor
	if plan.Encrypted {
		var derr error
		dec, derr = NewStripeDecryptor(plan.TrackID)
		if derr != nil {
			return 0, 0, derr
		}
	}

	url := plan.CDNURL
	buf := make([]byte, 64*1024)
	var plain []byte
	consumed := int64(0) // raw ciphertext/plain bytes read from CDN
	attempts := 0
	refreshes := 0
	validator := ""
	validatorSet := false
	var lastCL int64

	for {
		if cerr := ctx.Err(); cerr != nil {
			return consumed, lastCL, cerr
		}
		startOff := consumed
		resp, oerr := openDownloadStream(ctx, url, consumed, validator)
		if oerr != nil {
			attempts++
			if attempts > maxResumeAttempts {
				return consumed, lastCL, classifyNet(oerr)
			}
			backoffDownload(ctx, attempts)
			continue
		}

		switch {
		case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusGone:
			resp.Body.Close()
			if u2 := refreshPlanURL(plan, &refreshes); u2 != "" {
				url = u2
				continue
			}
			e := fmt.Errorf("CDN returned %s", resp.Status)
			resp.Body.Close()
			return consumed, lastCL, e
		case resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent:
			e := fmt.Errorf("CDN returned %s", resp.Status)
			resp.Body.Close()
			return consumed, lastCL, e
		}

		// Resume position sanity for 206.
		if resp.StatusCode == http.StatusPartialContent && consumed > 0 {
			if start := contentRangeStart(resp); start != consumed {
				resp.Body.Close()
				attempts++
				if attempts > maxResumeAttempts {
					e := fmt.Errorf("CDN resume at %d answered Content-Range start %d", consumed, start)
					return consumed, lastCL, e
				}
				backoffDownload(ctx, attempts)
				continue
			}
		}

		if cl := totalLength(resp); cl > 0 {
			lastCL = cl
		}
		if !validatorSet {
			validator = responseValidator(resp)
			validatorSet = true
		}

		// Server ignored our Range (200 instead of 206): skip prefix bytes
		// only if entity validator still matches.
		var skip int64
		if consumed > 0 && resp.StatusCode == http.StatusOK {
			if validator != "" && responseValidator(resp) != validator {
				resp.Body.Close()
				e := fmt.Errorf("stream entity changed during resume (validator mismatch)")
				return consumed, lastCL, e
			}
			skip = consumed
		}

		completed, rerr := pumpDownloadBody(resp.Body, buf, &plain, dec, w, &consumed, skip)
		resp.Body.Close()

		if lastCL > 0 && consumed < lastCL {
			if rerr == io.EOF || rerr == io.ErrUnexpectedEOF || completed {
				// Server ended the body (EOF or short-CL) under the declared length:
				// treat as final short file (reject before rename), not a retryable drop.
				return consumed, lastCL, fmt.Errorf("download: short file (got %d, declared %d)", consumed, lastCL)
			}
			// else: transport/read error while still under length -- retry resume
		}
		if completed || rerr == io.EOF {
			return consumed, lastCL, nil
		}
		if cerr := ctx.Err(); cerr != nil {
			return consumed, lastCL, cerr
		}

		// Torn mid-body: allow retry budget reset only on meaningful progress.
		if consumed-startOff >= minResumeProgress {
			attempts = 0
		}
		attempts++
		if attempts > maxResumeAttempts {
			if rerr != nil {
				return consumed, lastCL, rerr
			}
			return consumed, lastCL, io.ErrUnexpectedEOF
		}
		backoffDownload(ctx, attempts)
	}
}

// openDownloadStream issues the (possibly ranged) CDN GET. Mirrors the
// Range + If-Range logic from the audio player.
func openDownloadStream(ctx context.Context, url string, offset int64, validator string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", downloadUserAgent)
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		if validator != "" {
			req.Header.Set("If-Range", validator)
		}
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// pumpDownloadBody feeds one response body (after skip) into w, advancing the
// decryptor (if any) and consumed count. On clean EOF it flushes Finish for
// encrypted streams. Returns completed=true only for io.EOF.
func pumpDownloadBody(body io.Reader, buf []byte, plain *[]byte, dec *StripeDecryptor, w io.Writer, consumed *int64, skip int64) (completed bool, err error) {
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			b := buf[:n]
			if skip > 0 {
				d := skip
				if d > int64(len(b)) {
					d = int64(len(b))
				}
				b = b[d:]
				skip -= d
			}
			if len(b) > 0 {
				if dec != nil {
					*plain = dec.Feed(b, (*plain)[:0])
					if _, werr := w.Write(*plain); werr != nil {
						return false, werr
					}
					*consumed = dec.Consumed()
				} else {
					if _, werr := w.Write(b); werr != nil {
						return false, werr
					}
					*consumed += int64(len(b))
				}
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				if dec != nil {
					*plain = dec.Finish((*plain)[:0])
					if len(*plain) > 0 {
						if _, werr := w.Write(*plain); werr != nil {
							return false, werr
						}
					}
				}
				return true, nil
			}
			return false, rerr
		}
	}
}

// refreshPlanURL re-resolves via plan.Refresh (if present) on 403/410, but
// only accepts a plan describing the identical stream (same Format/Encrypted/Preview).
// A changed plan would corrupt the decryptor state or the output file.
func refreshPlanURL(plan *StreamPlan, count *int) string {
	if plan.Refresh == nil || *count >= maxRefreshAttempts {
		return ""
	}
	*count++
	np, err := plan.Refresh()
	if err != nil || np == nil || np.CDNURL == "" ||
		np.Format != plan.Format || np.Encrypted != plan.Encrypted || np.Preview != plan.Preview {
		return ""
	}
	return np.CDNURL
}

// backoffDownload sleeps with attempt-scaled backoff or until ctx done.
func backoffDownload(ctx context.Context, attempt int) {
	t := time.NewTimer(resumeBackoff * time.Duration(attempt))
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// totalLength returns the full entity size (from Content-Range on 206, else
// Content-Length).
func totalLength(resp *http.Response) int64 {
	if resp.StatusCode == http.StatusPartialContent {
		cr := resp.Header.Get("Content-Range")
		if i := strings.LastIndex(cr, "/"); i >= 0 {
			if v, err := strconv.ParseInt(strings.TrimSpace(cr[i+1:]), 10, 64); err == nil && v > 0 {
				return v
			}
		}
		return 0
	}
	if resp.ContentLength > 0 {
		return resp.ContentLength
	}
	return 0
}

// contentRangeStart parses start of "bytes N-M/T" or returns -1.
func contentRangeStart(resp *http.Response) int64 {
	cr := strings.TrimSpace(resp.Header.Get("Content-Range"))
	cr = strings.TrimSpace(strings.TrimPrefix(cr, "bytes"))
	dash := strings.IndexByte(cr, '-')
	if dash < 0 {
		return -1
	}
	v, err := strconv.ParseInt(strings.TrimSpace(cr[:dash]), 10, 64)
	if err != nil || v < 0 {
		return -1
	}
	return v
}

// responseValidator returns ETag or Last-Modified (for If-Range).
func responseValidator(resp *http.Response) string {
	if et := resp.Header.Get("ETag"); et != "" {
		return et
	}
	return resp.Header.Get("Last-Modified")
}

// SaveTrack downloads trackID to a file inside dir, choosing the filename from
// the track's title/artist and the resolved format's extension, and returns the
// written path. Downloads are premium-only ([ErrPremiumRequired]); a track whose
// only source is the 30-second preview is refused ([ErrPreviewOnly]) rather than
// saved as a clip. A partial file is removed on failure. If dir is empty the
// current working directory is used.
//
// After writing the audio, best-effort metadata tagging (ID3v2.4 or FLAC
// Vorbis+PICTURE) is performed using the track's ArtworkURL (upgraded size).
// Tagging failure is non-fatal: a warning is logged and the raw audio file is
// returned untouched.
func (c *Client) SaveTrack(ctx context.Context, trackID, dir string) (string, error) {
	if !c.LoggedIn() {
		return "", fmt.Errorf("not logged in")
	}
	if !c.Account().Premium {
		return "", ErrPremiumRequired
	}
	t, err := c.Track(trackID)
	if err != nil {
		return "", err
	}
	return c.saveTrack(ctx, t, dir, nil, nil)
}

// saveTrack performs the download + tagging for a Track value we already have
// (from Track/AlbumTracks/PlaylistTracks). It is the common impl for SaveTrack
// and the batch methods. plan may be the already-resolved stream plan (batch
// code resolves one per track for the skip-existing check); nil resolves here.
func (c *Client) saveTrack(ctx context.Context, t Track, dir string, plan *StreamPlan, fetch ArtworkFetcher) (string, error) {
	if plan == nil {
		var err error
		plan, err = c.PrepareStream(t.ID)
		if err != nil {
			return "", err
		}
	}
	if plan.Preview {
		return "", ErrPreviewOnly
	}

	ext := "mp3"
	if strings.Contains(strings.ToUpper(plan.Format), "FLAC") {
		ext = "flac"
	}
	name := trackFileName(t, ext)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	path := filepath.Join(dir, name)

	// Use a unique temp (os.CreateTemp with O_EXCL) so that two concurrent
	// downloads of the identical track cannot both write to the same .part and
	// corrupt it. The temp lives in the target dir so the final rename is
	// atomic on the same filesystem. On any error only this job's temp is
	// removed. SkipExisting checks (by callers) continue to use the final name.
	targetDir := dir
	if targetDir == "" {
		targetDir = "."
	}
	stem := strings.TrimSuffix(name, "."+ext)
	pattern := stem + "-*.part"
	var f *os.File
	var err error
	if testCreateTemp != nil {
		f, err = testCreateTemp(targetDir, pattern)
	} else {
		f, err = os.CreateTemp(targetDir, pattern)
	}
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	written, cl, derr := downloadToWriter(ctx, plan, f)
	if derr != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", derr
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}

	// Verify received bytes against declared Content-Length before the
	// final rename. Reject short files so the caller (batch or single) can
	// treat it as a failure and redownload.
	if cl > 0 {
		if fi, serr := os.Stat(tmp); serr == nil && fi.Size() != cl {
			_ = os.Remove(tmp)
			return "", fmt.Errorf("download: short file (got %d, declared %d)", fi.Size(), cl)
		}
	}
	_ = written // (== cl on success; size-preserving for both encrypted and plain)

	// Best-effort tagging, applied to the temp file before it becomes visible
	// under the final name. Never fails the download.
	au := upgradeArtworkURL(t.ArtworkURL)
	if tagErr := tagFile(ctx, tmp, t, au, t.TrackNumber, t.Year, fetch); tagErr != nil {
		odlog.Warn("download: tagging %s failed (non-fatal, file left as raw audio): %v", path, tagErr)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return path, nil
}

// trackFileName builds the on-disk filename for a track. The track ID makes
// the name unique, so same-title/same-artist tracks (live takes, deluxe
// editions) never collide — a collision would overwrite the first download,
// false-positive the SkipExisting check, and let a failed second transfer
// truncate the first. Batch skip checks and saveTrack MUST use the same
// formula so SkipExisting re-runs keep matching prior downloads.
//
// The [id] suffix is appended *after* truncating the title/artist portion (on
// a rune boundary) so that the uniqueness marker is never stripped and the
// resulting name is always valid UTF-8 even when the descriptive part is very
// long or contains multi-byte runes.
func trackFileName(t Track, ext string) string {
	desc := t.Name + " - " + t.ArtistLine()
	suffix := " [" + t.ID + "]"
	room := 180 - len(suffix)
	if room < 0 {
		room = 0
	}
	desc = truncateSafe(desc, room)
	cand := desc + suffix
	return sanitizeFilename(cand) + "." + ext
}

// truncateSafe returns at most max bytes of s, stopping at a UTF-8 rune
// boundary so the result is always valid UTF-8.
func truncateSafe(s string, max int) string {
	if len(s) <= max {
		return s
	}
	// Walk runes so we never cut inside a multi-byte sequence.
	var b strings.Builder
	n := 0
	for _, r := range s {
		rl := utf8.RuneLen(r)
		if n+rl > max {
			break
		}
		b.WriteRune(r)
		n += rl
	}
	return b.String()
}

// sanitizeFilename maps characters illegal on common filesystems (Windows is the
// strict one) to underscores and trims to a sane length, so a track title can be
// used verbatim as a filename on every platform.
func sanitizeFilename(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, s)
	s = strings.Trim(s, " .")
	if s == "" {
		s = "track"
	}
	// Cap the base name so path length stays well under filesystem limits.
	// Use rune-safe truncate (the caller of trackFileName already ensured the
	// id suffix fits, but other callers of sanitize get the same protection).
	if len(s) > 180 {
		s = strings.TrimSpace(truncateSafe(s, 180))
	}
	return s
}

// SaveAlbum downloads every track belonging to the album (via AlbumTracks) to
// files inside dir. Downloads are sequential. It respects the same premium
// requirement as SaveTrack. Per-track errors are collected; an aggregate error
// wrapping [ErrPartialDownload] is returned if any track failed, while
// successfully saved paths are still returned. Cancelling ctx stops the batch
// between tracks and returns the paths saved so far alongside the ctx error.
func (c *Client) SaveAlbum(ctx context.Context, albumID, dir string, opts DownloadOptions) ([]string, error) {
	if !c.LoggedIn() {
		return nil, fmt.Errorf("not logged in")
	}
	if !c.Account().Premium {
		return nil, ErrPremiumRequired
	}
	// Don't perform the network listing for an already-cancelled batch.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tracks, err := c.AlbumTracks(albumID)
	if err != nil {
		return nil, err
	}
	return c.saveBatch(ctx, tracks, dir, opts)
}

// SavePlaylist downloads every track in the playlist (via PlaylistTracks) to
// files inside dir. Same semantics as SaveAlbum.
func (c *Client) SavePlaylist(ctx context.Context, playlistID, dir string, opts DownloadOptions) ([]string, error) {
	if !c.LoggedIn() {
		return nil, fmt.Errorf("not logged in")
	}
	if !c.Account().Premium {
		return nil, ErrPremiumRequired
	}
	// Don't perform the network listing for an already-cancelled batch.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Strict listing: unlike the browse-oriented PlaylistTracks, a mid-fetch
	// page error must fail the download rather than silently truncate it.
	tracks, err := c.playlistTracksStrict(playlistID)
	if err != nil {
		return nil, err
	}
	return c.saveBatch(ctx, tracks, dir, opts)
}

func (c *Client) saveBatch(ctx context.Context, tracks []Track, dir string, opts DownloadOptions) ([]string, error) {
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	saver := opts.trackSaver
	if saver == nil {
		saver = c.saveTrack
	}
	fetch := opts.ArtworkFetcher

	getPlan := opts.planResolver
	if getPlan == nil {
		getPlan = c.PrepareStream
	}

	conc := opts.Concurrency
	if conc <= 0 {
		conc = 3
	}
	n := len(tracks)
	if conc > n {
		conc = n
	}
	if conc < 1 {
		conc = 1
	}

	// Work queue (sends in order; workers may complete out of order).
	type job struct {
		idx int
		tr  Track
	}
	jobs := make(chan job, n)
	for i, tr := range tracks {
		jobs <- job{idx: i, tr: tr}
	}
	close(jobs)

	var (
		mu       sync.Mutex
		saved    []string
		hadErr   bool
		firstErr error
	)

	var wg sync.WaitGroup
	for w := 0; w < conc; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				// Pre-check cancellation (between "tracks").
				if cerr := ctx.Err(); cerr != nil {
					prog := DownloadProgress{
						Index:   j.idx + 1,
						Total:   n,
						TrackID: j.tr.ID,
						Title:   j.tr.Name,
						Err:     cerr,
					}
					if opts.Progress != nil {
						opts.Progress(prog)
					}
					mu.Lock()
					if firstErr == nil {
						firstErr = cerr
					}
					hadErr = true
					mu.Unlock()
					continue
				}

				prog := DownloadProgress{
					Index:   j.idx + 1,
					Total:   n,
					TrackID: j.tr.ID,
					Title:   j.tr.Name,
				}

				plan, perr := getPlan(j.tr.ID)
				if perr != nil {
					prog.Err = perr
					if opts.Progress != nil {
						opts.Progress(prog)
					}
					mu.Lock()
					if firstErr == nil {
						firstErr = perr
					}
					hadErr = true
					mu.Unlock()
					continue
				}
				if plan.Preview {
					prog.Err = ErrPreviewOnly
					if opts.Progress != nil {
						opts.Progress(prog)
					}
					mu.Lock()
					if firstErr == nil {
						firstErr = ErrPreviewOnly
					}
					hadErr = true
					mu.Unlock()
					continue
				}

				ext := "mp3"
				if strings.Contains(strings.ToUpper(plan.Format), "FLAC") {
					ext = "flac"
				}
				name := trackFileName(j.tr, ext)
				path := filepath.Join(dir, name)

				if opts.SkipExisting {
					if _, err := os.Stat(path); err == nil {
						mu.Lock()
						saved = append(saved, path)
						mu.Unlock()
						if opts.Progress != nil {
							opts.Progress(prog)
						}
						continue
					}
				}

				pth, derr := saver(ctx, j.tr, dir, plan, fetch)
				if derr != nil {
					prog.Err = derr
					mu.Lock()
					if firstErr == nil {
						firstErr = derr
					}
					hadErr = true
					mu.Unlock()
				} else {
					mu.Lock()
					saved = append(saved, pth)
					mu.Unlock()
				}
				if opts.Progress != nil {
					opts.Progress(prog)
				}
			}
		}()
	}
	wg.Wait()

	// Cancellation takes precedence (mirrors sequential pre-check behavior).
	if cerr := ctx.Err(); cerr != nil {
		return saved, cerr
	}
	if hadErr {
		// Double-wrap so callers can match BOTH the ErrPartialDownload
		// sentinel and the concrete first failure via errors.Is/As.
		return saved, fmt.Errorf("%w: %w (%d failed, %d saved)", ErrPartialDownload, firstErr, n-len(saved), len(saved))
	}
	return saved, nil
}

// ErrPartialDownload is returned (wrapped) by SaveAlbum/SavePlaylist when one
// or more tracks could not be downloaded. The first error and counts are
// included in the message; successfully downloaded paths are still returned.
var ErrPartialDownload = errors.New("batch download had failures")
