package deezer

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bogem/id3v2/v2"
	"github.com/go-flac/flacpicture"
	"github.com/go-flac/flacvorbis"
	flac "github.com/go-flac/go-flac"

	odlog "github.com/Cycl0o0/OpenDeezer/v2/internal/log"
)

// ArtworkFetcher fetches bytes for a Deezer artwork URL (or upgraded size).
// Supply a no-op or fake for tests so no network calls are made.
type ArtworkFetcher func(ctx context.Context, artworkURL string) ([]byte, error)

// upgradeArtworkURL follows existing patterns (gwCover uses 250x250; API
// responses use cover_medium / picture_medium). We request a reasonably large
// 500x500 variant suitable for embedded covers.
func upgradeArtworkURL(u string) string {
	if u == "" {
		return ""
	}
	for _, old := range []string{"250x250-000000-80-0-0.jpg", "250x250", "120x120", "56x56", "80x80"} {
		if strings.Contains(u, old) {
			rep := "500x500"
			if strings.Contains(old, "000000") {
				rep = "500x500-000000-80-0-0.jpg"
			}
			return strings.Replace(u, old, rep, 1)
		}
	}
	return u
}

func defaultArtworkFetcher(ctx context.Context, url string) ([]byte, error) {
	if url == "" {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", downloadUserAgent)
	resp, err := downloadClient.Do(req)
	if err != nil {
		return nil, classifyNet(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artwork CDN returned %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// tagFile attaches metadata (and optional cover) to a freshly written audio
// file. On any error the audio bytes are left untouched (no partial tag
// overwrite that would corrupt playback) and the error is returned for the
// caller to log as non-fatal.
func tagFile(ctx context.Context, path string, t Track, artworkURL string, trackNumber int, date string, fetch ArtworkFetcher) error {
	if fetch == nil {
		fetch = defaultArtworkFetcher
	}
	var art []byte
	if artworkURL != "" {
		if b, err := fetch(ctx, artworkURL); err == nil && len(b) > 0 {
			art = b
		} else if err != nil {
			odlog.Debug("tag: artwork fetch for %s: %v (continuing without art)", path, err)
		}
	}

	// saveTrack tags the in-progress "<name>.<ext>.part" temp file before
	// renaming it over the final name, so dispatch on the real extension
	// underneath the .part suffix.
	lower := strings.ToLower(strings.TrimSuffix(path, ".part"))
	if strings.HasSuffix(lower, ".flac") {
		return tagFLAC(path, t, art, trackNumber, date)
	}
	return tagMP3(path, t, art, trackNumber, date)
}

func tagMP3(path string, t Track, art []byte, trackNumber int, date string) error {
	tag, err := id3v2.Open(path, id3v2.Options{Parse: false})
	if err != nil {
		return fmt.Errorf("id3v2 open: %w", err)
	}
	defer tag.Close()

	tag.SetDefaultEncoding(id3v2.EncodingUTF8)
	tag.SetTitle(t.Name)
	tag.SetArtist(t.ArtistLine())
	tag.SetAlbum(t.AlbumName)
	if date != "" {
		tag.SetYear(date)
	}
	if trackNumber > 0 {
		id := tag.CommonID("Track number/Position in set")
		tag.AddTextFrame(id, id3v2.EncodingUTF8, fmt.Sprintf("%d", trackNumber))
	}
	if len(art) > 0 {
		pic := id3v2.PictureFrame{
			Encoding:    id3v2.EncodingUTF8,
			MimeType:    "image/jpeg",
			PictureType: id3v2.PTFrontCover,
			Description: "Front cover",
			Picture:     art,
		}
		tag.AddAttachedPicture(pic)
	}
	if err := tag.Save(); err != nil {
		return fmt.Errorf("id3v2 save: %w", err)
	}
	return nil
}

func tagFLAC(path string, t Track, art []byte, trackNumber int, date string) error {
	f, err := flac.ParseFile(path)
	if err != nil {
		return fmt.Errorf("flac parse: %w", err)
	}

	// Preserve StreamInfo (must be first). Drop prior comment/picture blocks
	// so we control the final set.
	newMeta := make([]*flac.MetaDataBlock, 0, len(f.Meta)+2)
	if len(f.Meta) > 0 {
		newMeta = append(newMeta, f.Meta[0])
	}
	for _, m := range f.Meta[1:] {
		if m.Type != flac.VorbisComment && m.Type != flac.Picture {
			newMeta = append(newMeta, m)
		}
	}

	cmts := flacvorbis.New()
	cmts.Add(flacvorbis.FIELD_TITLE, t.Name)
	cmts.Add(flacvorbis.FIELD_ARTIST, t.ArtistLine())
	cmts.Add(flacvorbis.FIELD_ALBUM, t.AlbumName)
	if trackNumber > 0 {
		cmts.Add(flacvorbis.FIELD_TRACKNUMBER, fmt.Sprintf("%d", trackNumber))
	}
	if date != "" {
		cmts.Add(flacvorbis.FIELD_DATE, date)
	}
	cm := cmts.Marshal()
	newMeta = append(newMeta, &cm)

	if len(art) > 0 {
		p, perr := flacpicture.NewFromImageData(flacpicture.PictureTypeFrontCover, "Front cover", art, "image/jpeg")
		if perr == nil {
			pm := p.Marshal()
			newMeta = append(newMeta, &pm)
		}
	}

	f.Meta = newMeta

	// go-flac's Save is an in-place O_TRUNC rewrite of path: a short write
	// (ENOSPC, crash) would destroy the just-downloaded audio while tagging
	// errors are treated as non-fatal by callers. Write the tagged copy next
	// to the original and atomically rename it over path only on full
	// success; on any error the original bytes are never touched.
	mode := os.FileMode(0o644)
	if fi, serr := os.Stat(path); serr == nil {
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tag-*")
	if err != nil {
		return fmt.Errorf("flac tag temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(f.Marshal()); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("flac tag write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("flac tag close: %w", err)
	}
	_ = os.Chmod(tmpName, mode)
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("flac tag rename: %w", err)
	}
	return nil
}
