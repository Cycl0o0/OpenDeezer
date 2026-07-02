package deezer

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	internaldeezer "github.com/Cycl0o0/OpenDeezer/internal/deezer"
)

const downloadUserAgent = "Mozilla/5.0 OpenDeezer/0.1"

// downloadClient is a dedicated client for CDN transfers. Unlike
// http.DefaultClient it bounds how long a stalled server may keep the transfer
// alive: the dial and TLS-handshake timeouts (mirroring http.DefaultTransport;
// ResponseHeaderTimeout does NOT cover connection setup — it only starts
// counting once the request has been written) guard connect/TLS stalls,
// ResponseHeaderTimeout guards the initial response, and IdleConnTimeout bounds
// reused connections. Bodies can still be arbitrarily long, so overall
// cancellation is left to the request context.
var downloadClient = &http.Client{
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		// Setting DialContext would otherwise disable the automatic HTTP/2
		// upgrade; keep parity with http.DefaultTransport.
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	},
}

// DownloadTrack fetches and decrypts a Deezer track to w. plan must come from
// [Client.PrepareStream] (for tracks) or [Client.PodcastEpisodeStream] (for
// podcast episodes, which are not encrypted).
//
// The bytes written form a valid MP3 or FLAC file and can be piped directly to
// a decoder or saved to disk.
//
//	plan, err := client.PrepareStream("3135556")
//	if err != nil { log.Fatal(err) }
//	f, _ := os.Create("track." + strings.ToLower(plan.Format))
//	defer f.Close()
//	if err := deezer.DownloadTrack(plan, f); err != nil { log.Fatal(err) }
//
// DownloadTrack cannot be cancelled once started; use [DownloadTrackContext] to
// abort a download or bound its total duration.
func DownloadTrack(plan *StreamPlan, w io.Writer) error {
	return DownloadTrackContext(context.Background(), plan, w)
}

// DownloadTrackContext is [DownloadTrack] with cancellation. Cancelling ctx
// aborts the in-flight CDN read and unblocks the copy, so callers can implement
// a "cancel download" action or a per-download deadline.
func DownloadTrackContext(ctx context.Context, plan *StreamPlan, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, plan.CDNURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", downloadUserAgent)
	resp, err := downloadClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("CDN returned %s", resp.Status)
	}

	// Podcast episodes and other unencrypted streams pass through directly.
	if !plan.Encrypted {
		_, err = io.Copy(w, resp.Body)
		return err
	}

	// Encrypted tracks use Deezer's BF_CBC_STRIPE scheme: every third 2048-byte
	// chunk is decrypted with a per-track Blowfish key; the rest are plain.
	dec, err := internaldeezer.NewStripeDecryptor(plan.TrackID)
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
			// Flush the trailing partial chunk (always plaintext).
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
