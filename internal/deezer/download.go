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
	"strings"
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

// DownloadTrack fetches and decrypts a Deezer stream to w. plan must come from
// [Client.PrepareStream] (tracks) or [Client.PodcastEpisodeStream] (episodes,
// which are unencrypted). The bytes written form a valid MP3 or FLAC file.
func DownloadTrack(plan *StreamPlan, w io.Writer) error {
	return DownloadTrackContext(context.Background(), plan, w)
}

// DownloadTrackContext is [DownloadTrack] with cancellation. Cancelling ctx
// aborts the in-flight CDN read and unblocks the copy.
func DownloadTrackContext(ctx context.Context, plan *StreamPlan, w io.Writer) error {
	if plan == nil || plan.CDNURL == "" {
		return fmt.Errorf("download: empty stream plan")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, plan.CDNURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", downloadUserAgent)
	resp, err := downloadClient.Do(req)
	if err != nil {
		return classifyNet(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("CDN returned %s", resp.Status)
	}

	// Unencrypted streams (podcast episodes, 30-second previews) pass straight
	// through with no decryption.
	if !plan.Encrypted {
		_, err = io.Copy(w, resp.Body)
		return err
	}

	// Encrypted tracks use BF_CBC_STRIPE: every third 2048-byte chunk is
	// Blowfish-decrypted with a per-track key; the rest are plaintext.
	dec, err := NewStripeDecryptor(plan.TrackID)
	if err != nil {
		return err
	}
	buf := make([]byte, 64*1024)
	var plain []byte
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			plain = dec.Feed(buf[:n], plain[:0])
			if _, werr := w.Write(plain); werr != nil {
				return werr
			}
		}
		if rerr == io.EOF {
			plain = dec.Finish(plain[:0])
			if len(plain) > 0 {
				_, err = w.Write(plain)
			}
			return err
		}
		if rerr != nil {
			return rerr
		}
	}
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
	if err := DownloadTrackContext(ctx, plan, f); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}

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

	var saved []string
	var hadErr bool
	var firstErr error

	for i, tr := range tracks {
		// Surface cancellation to the caller: silently returning the partial
		// path list with a nil error would report a truncated batch as success.
		if cerr := ctx.Err(); cerr != nil {
			return saved, cerr
		}

		prog := DownloadProgress{
			Index:   i + 1,
			Total:   len(tracks),
			TrackID: tr.ID,
			Title:   tr.Name,
		}

		// Prepare plan here so we can compute filename for skip check.
		plan, perr := getPlan(tr.ID)
		if perr != nil {
			prog.Err = perr
			if firstErr == nil {
				firstErr = perr
			}
			hadErr = true
			if opts.Progress != nil {
				opts.Progress(prog)
			}
			continue
		}
		if plan.Preview {
			prog.Err = ErrPreviewOnly
			if firstErr == nil {
				firstErr = ErrPreviewOnly
			}
			hadErr = true
			if opts.Progress != nil {
				opts.Progress(prog)
			}
			continue
		}

		ext := "mp3"
		if strings.Contains(strings.ToUpper(plan.Format), "FLAC") {
			ext = "flac"
		}
		// Must match saveTrack's naming exactly so SkipExisting stays
		// consistent across runs.
		name := trackFileName(tr, ext)
		path := filepath.Join(dir, name)

		if opts.SkipExisting {
			if _, err := os.Stat(path); err == nil {
				saved = append(saved, path)
				if opts.Progress != nil {
					opts.Progress(prog)
				}
				continue
			}
		}

		// Thread the plan we already resolved for the skip check into the
		// saver so each track costs exactly one PrepareStream.
		pth, derr := saver(ctx, tr, dir, plan, fetch)
		if derr != nil {
			prog.Err = derr
			if firstErr == nil {
				firstErr = derr
			}
			hadErr = true
		} else {
			saved = append(saved, pth)
		}
		if opts.Progress != nil {
			opts.Progress(prog)
		}
	}

	if hadErr {
		// Double-wrap so callers can match BOTH the ErrPartialDownload
		// sentinel and the concrete first failure via errors.Is/As.
		return saved, fmt.Errorf("%w: %w (%d failed, %d saved)", ErrPartialDownload, firstErr, len(tracks)-len(saved), len(saved))
	}
	return saved, nil
}

// ErrPartialDownload is returned (wrapped) by SaveAlbum/SavePlaylist when one
// or more tracks could not be downloaded. The first error and counts are
// included in the message; successfully downloaded paths are still returned.
var ErrPartialDownload = errors.New("batch download had failures")
