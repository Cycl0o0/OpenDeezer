package deezer

import (
	"fmt"
	"io"
	"net/http"

	internaldeezer "github.com/Cycl0o0/OpenDeezer/internal/deezer"
)

const downloadUserAgent = "Mozilla/5.0 OpenDeezer/0.1"

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
func DownloadTrack(plan *StreamPlan, w io.Writer) error {
	req, err := http.NewRequest(http.MethodGet, plan.CDNURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", downloadUserAgent)
	resp, err := http.DefaultClient.Do(req)
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
