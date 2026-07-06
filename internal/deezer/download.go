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
	plan, err := c.PrepareStream(trackID)
	if err != nil {
		return "", err
	}
	if plan.Preview {
		return "", ErrPreviewOnly
	}

	ext := "mp3"
	if strings.Contains(strings.ToUpper(plan.Format), "FLAC") {
		ext = "flac"
	}
	name := sanitizeFilename(t.Name+" - "+t.ArtistLine()) + "." + ext
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	path := filepath.Join(dir, name)

	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	if err := DownloadTrackContext(ctx, plan, f); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
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
	if len(s) > 180 {
		s = strings.TrimSpace(s[:180])
	}
	return s
}
