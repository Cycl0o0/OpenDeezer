package deezer

import (
	"context"
	"io"

	internaldeezer "github.com/Cycl0o0/OpenDeezer/v2/internal/deezer"
)

// ---- download errors ----

// ErrPremiumRequired is returned by [Client.SaveTrack] when the account is not
// entitled to full-track streaming (Deezer Free). Use errors.Is to detect it.
var ErrPremiumRequired = internaldeezer.ErrPremiumRequired

// ErrPreviewOnly is returned by [Client.SaveTrack] when the only source Deezer
// offers is the 30-second preview. Use errors.Is to detect it.
var ErrPreviewOnly = internaldeezer.ErrPreviewOnly

// DownloadTrack fetches and decrypts a Deezer stream to w. plan must come from
// [Client.PrepareStream] (tracks) or [Client.PodcastEpisodeStream] (episodes,
// which are unencrypted). The bytes written form a valid MP3 or FLAC file and
// can be piped directly to a decoder or saved to disk.
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
	return internaldeezer.DownloadTrack(plan, w)
}

// DownloadTrackContext is [DownloadTrack] with cancellation. Cancelling ctx
// aborts the in-flight CDN read and unblocks the copy, so callers can implement
// a "cancel download" action or a per-download deadline.
func DownloadTrackContext(ctx context.Context, plan *StreamPlan, w io.Writer) error {
	return internaldeezer.DownloadTrackContext(ctx, plan, w)
}

// SaveTrack downloads trackID into dir, choosing a filename from the track's
// title/artist and the resolved format's extension, and returns the written
// path. Downloads are premium-only ([ErrPremiumRequired]); a track available
// only as a 30-second preview is refused ([ErrPreviewOnly]). If dir is empty the
// working directory is used.
func (cl *Client) SaveTrack(ctx context.Context, trackID, dir string) (string, error) {
	return cl.c.SaveTrack(ctx, trackID, dir)
}
