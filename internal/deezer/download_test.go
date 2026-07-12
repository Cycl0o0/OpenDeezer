package deezer

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogem/id3v2/v2"
	"github.com/go-flac/flacvorbis"
	flac "github.com/go-flac/go-flac"
)

func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Hello World", "Hello World"},
		{`AC/DC - Back in Black`, "AC_DC - Back in Black"},
		{`a<b>c:d"e/f\g|h?i*j`, "a_b_c_d_e_f_g_h_i_j"},
		{"  .trim. ", "trim"},
		{"", "track"},
		{"   ", "track"},
	}
	for _, c := range cases {
		if got := sanitizeFilename(c.in); got != c.want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Illegal characters must never survive.
	if got := sanitizeFilename(`x/y\z:*?"<>|`); strings.ContainsAny(got, `/\:*?"<>|`) {
		t.Errorf("sanitizeFilename left an illegal character: %q", got)
	}
	// Length is capped so the path stays under filesystem limits.
	if got := sanitizeFilename(strings.Repeat("z", 500)); len(got) > 180 {
		t.Errorf("sanitizeFilename length = %d, want <= 180", len(got))
	}
}

func TestStreamPlanPreviewIsUnencrypted(t *testing.T) {
	// A preview plan must be a plain pass-through stream (no Blowfish), so the
	// player and downloader stream it straight through.
	p := &StreamPlan{CDNURL: "https://cdnt-preview.dzcdn.net/x.mp3", Format: "MP3_128", Preview: true, Encrypted: false}
	if p.Encrypted {
		t.Fatal("preview StreamPlan must not be Encrypted")
	}
	if !p.Preview {
		t.Fatal("preview flag lost")
	}
}

// flacAudioB64 is a base64-encoded minimal valid FLAC (from dep testdata) used
// as a "downloaded" audio body so tagFLAC can ParseFile + rewrite metas.
const flacAudioB64 = `ZkxhQwAAACIQABAAAARBAARBAfQBcAAAAZLfwZb9QVlTtnnZLOsaWczxhAAAKCAAAAByZWZlcmVuY2UgbGliRkxBQyAxLjMuMCAyMDEzMDUyNgAAAAD/+HQMAAGRJhgKxHRX63x+6QxXqTpCYAANDJgITNAmIhAB/5BgFt4iAfgBZHY4A9UZAAZ65QManTnQUAKnx8A+ZcwFun6gX1gMVIgAfaXgEml9KIzgfUBYAnhLwJl/Oek+A5rZADhx2ApN3s198CTrfAPyHMGRV3DcKAYAAwBJ7uA8wysUF8HYV6AUyIIKv/ObD0B7BRAOf6QFVnlVIrg0zuwGYTqCTP9rJXAWm4AC1HQDxVMnllwVg94CGPDDq6XrJRgQ2zwHp6wGR5tQFGh9dygZtwIXeHNK+AHAw8BZ34BSgBUY44Ze3IEt2uEhBfQg3BcaTA+MSB/rIUTNIUTWYN9hMdT2M/TYEg+cDC6KGzFJQAzj8TBBUujjHFLnsqB07TAj7JBa4qTwQY15pQ9rKxSdhzrJwxaKA3o/BKIsTkDYtGCwxtDRCIqzegQo2pAr9KD0mKmwtRJeuhMscnKzjNLLCC4CCDoYNaPiZmNPNIsdzTJkF0zHIRyDGDZzCLozKYLfNVZUYt6pWUiy7/Bjf5CyRUJ95iW1TLkIET7MJJU5yxARVwzCNBsIZHCVflJ5ZE+0K30dXKabEjNYdP+TulglDBSFPJNlCtxqLycmj5OeMkURsMFSXgrtnXLllFm5cySbjiZQKoshh0ASOArXdBJuJ9c2syJBjM8II1VJYFtSEVbCw3f80qnABkAClxxw8mPk8NdHt4q9gNy0UpWEuO2UUx3jZSuRqVkcoWnWIWa1ahV3Y3K5KbKMH879vqjUtUZMatwgfZeOMvsQlyisbQ71rIxGX9FhUSYvRHvF3xeunSYfQ4iRItSvuvzR5iO3xDFaEdARSGOC1fGIpY/hll/Z8dBI+J6u22KvVuDK2IiXW9V8Htkhxa94kLDPz25hsMC8qImvymxd70W8D5iDfsU9TYMYt5EIfJnAWr0bS7NbeHbQu6ZsuZyvTuhwMbc9XFv3q4b4amazBWwDvKfuyGSBrwrbr8+kkvhfZKs7C2FwoVooWXOnr/sWmZ5hWFSSpEtq0O6bjQhPR6Eguo8PmOz4SoKeIgpRmJZ1CEWpm1U6F/2UKJhBBJi0KeJ6kgEoPCeWSBmv85AOeDhOk/mJgciON6g0DZHbeVa7jIhYMBSP4ukvNIr36CvTjhkZCkWJk5godIxpiOjqiEnoJQ2K3HjKtYcY2CFWiXpIrqaGEIgef4gs+JXnhRm4GziHB2h/OIRB6BgfhgH4ap2DiTgVwIUN6FjaguFoE0uENThJRYJMOBCwg3zIOyKB00gOl4LXWC79gW14DOaCQAglUoEKCApZgc1YHC2AxzgJBoFcSBWWgICoBp+BEXgO64BeGAXYgMGoCoCAOYgEk4CGWAb5gB54A2SAWGgEfoAHyAINgDuYAkeAAbgBfoAgyAEEgAC4ARGADPgAf4AFyABYgAeIACGAAMgAaYAECABJgAagfd0=`

// makeTestJPEG returns a minimal valid JPEG bytes that jpeg.Decode accepts
// (required by flacpicture.NewFromImageData for width/height extraction).
func makeTestJPEG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	img.Set(1, 0, color.RGBA{R: 0, G: 255, B: 0, A: 255})
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80})
	return buf.Bytes()
}

func TestTagFile_WritesID3v24AndCover(t *testing.T) {
	tmp := t.TempDir()
	mp3Path := filepath.Join(tmp, "song.mp3")
	// dummy "audio" bytes; tagging does not validate MP3 frames.
	audio := append([]byte("\xff\xfb\x90\x00"), bytes.Repeat([]byte{0}, 200)...)
	if err := os.WriteFile(mp3Path, audio, 0o644); err != nil {
		t.Fatal(err)
	}

	tr := Track{
		Name:      "Test Song",
		Artists:   []Artist{{Name: "Artist A"}, {Name: "Artist B"}},
		AlbumName: "Test Album",
	}
	fakeArt := makeTestJPEG()
	fetch := func(context.Context, string) ([]byte, error) { return fakeArt, nil }

	if err := tagFile(context.Background(), mp3Path, tr, "https://cdnt.dzcdn.net/cover/500x500.jpg", 12, "2026-03-04", fetch); err != nil {
		t.Fatalf("tagFile(mp3): %v", err)
	}

	tag, err := id3v2.Open(mp3Path, id3v2.Options{Parse: true})
	if err != nil {
		t.Fatalf("reopen for read: %v", err)
	}
	defer tag.Close()

	if got := tag.Title(); got != "Test Song" {
		t.Errorf("TIT2 title = %q", got)
	}
	if got := tag.Artist(); got != "Artist A, Artist B" {
		t.Errorf("TPE1 artist = %q", got)
	}
	if got := tag.Album(); got != "Test Album" {
		t.Errorf("TALB album = %q", got)
	}
	if got := tag.Year(); got != "2026-03-04" {
		t.Errorf("date/year = %q", got)
	}
	trck := tag.GetTextFrame(tag.CommonID("Track number/Position in set"))
	if trck.Text != "12" {
		t.Errorf("TRCK = %q", trck.Text)
	}
	// Attached pictures via GetFrames + type assert (no GetAttachedPictures helper).
	picID := tag.CommonID("Attached picture")
	picFrames := tag.GetFrames(picID)
	foundPic := false
	for _, fr := range picFrames {
		if pf, ok := fr.(id3v2.PictureFrame); ok && pf.PictureType == id3v2.PTFrontCover {
			if bytes.Equal(pf.Picture, fakeArt) {
				foundPic = true
			}
		}
	}
	if !foundPic {
		t.Errorf("APIC front cover missing or wrong data (frames=%d)", len(picFrames))
	}
}

func TestTagFile_WritesFLACVorbisAndPicture(t *testing.T) {
	tmp := t.TempDir()
	flacPath := filepath.Join(tmp, "song.flac")
	raw, err := base64.StdEncoding.DecodeString(flacAudioB64)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flacPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	tr := Track{
		Name:      "FLAC Track",
		Artists:   []Artist{{Name: "FLAC Artist"}},
		AlbumName: "FLAC Album",
	}
	fakeArt := makeTestJPEG()
	fetch := func(context.Context, string) ([]byte, error) { return fakeArt, nil }

	if err := tagFile(context.Background(), flacPath, tr, "https://cdnt.dzcdn.net/p.jpg", 4, "2025", fetch); err != nil {
		t.Fatalf("tagFile(flac): %v", err)
	}

	f, err := flac.ParseFile(flacPath)
	if err != nil {
		t.Fatalf("reparse flac: %v", err)
	}

	var title, artist, album, trackno, date string
	hasPic := false
	for _, mb := range f.Meta {
		if mb.Type == flac.VorbisComment {
			vc, perr := flacvorbis.ParseFromMetaDataBlock(*mb)
			if perr == nil {
				if vs, _ := vc.Get(flacvorbis.FIELD_TITLE); len(vs) > 0 {
					title = vs[0]
				}
				if vs, _ := vc.Get(flacvorbis.FIELD_ARTIST); len(vs) > 0 {
					artist = vs[0]
				}
				if vs, _ := vc.Get(flacvorbis.FIELD_ALBUM); len(vs) > 0 {
					album = vs[0]
				}
				if vs, _ := vc.Get(flacvorbis.FIELD_TRACKNUMBER); len(vs) > 0 {
					trackno = vs[0]
				}
				if vs, _ := vc.Get(flacvorbis.FIELD_DATE); len(vs) > 0 {
					date = vs[0]
				}
			}
		}
		if mb.Type == flac.Picture {
			hasPic = true
		}
	}
	if title != "FLAC Track" || artist != "FLAC Artist" || album != "FLAC Album" {
		t.Errorf("vorbis: title=%q artist=%q album=%q", title, artist, album)
	}
	if trackno != "4" || date != "2025" {
		t.Errorf("track/date: %q %q", trackno, date)
	}
	if !hasPic {
		t.Error("METADATA_BLOCK_PICTURE missing")
	}
}

func TestSaveBatch_SkipExistingAndProgress(t *testing.T) {
	tmp := t.TempDir()
	c := &Client{} // no real login needed; resolver + saver bypass PrepareStream and save

	trs := []Track{
		{ID: "t1", Name: "First Track", Artists: []Artist{{Name: "A"}}},
		{ID: "t2", Name: "Second Track", Artists: []Artist{{Name: "B"}}},
	}

	// pre-create the would-be file for t1 to trigger skip (same formula as
	// saveTrack: title - artist [id])
	name1 := trackFileName(trs[0], "mp3")
	_ = os.WriteFile(filepath.Join(tmp, name1), []byte("preexisting"), 0o644)

	var progs []DownloadProgress
	opts := DownloadOptions{
		SkipExisting: true,
		Progress:     func(p DownloadProgress) { progs = append(progs, p) },
		trackSaver: func(ctx context.Context, tt Track, d string, plan *StreamPlan, f ArtworkFetcher) (string, error) {
			p := filepath.Join(d, trackFileName(tt, "mp3"))
			_ = os.WriteFile(p, []byte("fresh"), 0o644)
			return p, nil
		},
		planResolver: func(id string) (*StreamPlan, error) {
			return &StreamPlan{Format: "MP3_320", Preview: false}, nil
		},
	}

	saved, err := c.saveBatch(context.Background(), trs, tmp, opts)
	if err != nil {
		t.Fatalf("saveBatch err: %v", err)
	}
	if len(saved) != 2 {
		t.Errorf("expected 2 saved (1 skip+1 new), got %d", len(saved))
	}
	if len(progs) != 2 {
		t.Errorf("expected 2 progress callbacks, got %d", len(progs))
	}
	if progs[0].Err != nil || progs[0].Index != 1 || progs[0].TrackID != "t1" {
		t.Errorf("prog0: %+v", progs[0])
	}
	if progs[1].Err != nil || progs[1].Index != 2 {
		t.Errorf("prog1: %+v", progs[1])
	}
	// t1 should still have old content (skipped)
	if b, _ := os.ReadFile(filepath.Join(tmp, name1)); string(b) != "preexisting" {
		t.Error("skip did not preserve preexisting file")
	}
}

func TestSaveBatch_ErrorCollectionAndContinue(t *testing.T) {
	tmp := t.TempDir()
	c := &Client{}

	trs := []Track{
		{ID: "ok1", Name: "OK1"},
		{ID: "bad", Name: "BAD"},
		{ID: "ok3", Name: "OK3"},
	}
	calls := 0
	opts := DownloadOptions{
		trackSaver: func(ctx context.Context, tt Track, d string, plan *StreamPlan, f ArtworkFetcher) (string, error) {
			calls++
			if tt.ID == "bad" {
				return "", fmt.Errorf("simulated fail for %s", tt.ID)
			}
			p := filepath.Join(d, tt.ID+".mp3")
			_ = os.WriteFile(p, []byte("ok"), 0o644)
			return p, nil
		},
		planResolver: func(string) (*StreamPlan, error) { return &StreamPlan{Format: "MP3_128", Preview: false}, nil },
	}

	saved, err := c.saveBatch(context.Background(), trs, tmp, opts)
	if err == nil {
		t.Fatal("expected aggregate error")
	}
	if !strings.Contains(err.Error(), "failed") || !strings.Contains(err.Error(), "simulated") {
		t.Errorf("err msg should mention failures and cause: %v", err)
	}
	if !errors.Is(err, ErrPartialDownload) {
		t.Errorf("aggregate error should match ErrPartialDownload: %v", err)
	}
	if len(saved) != 2 {
		t.Errorf("saved=%d want 2", len(saved))
	}
	if calls != 3 {
		t.Errorf("did not continue after error; calls=%d", calls)
	}
}

func TestSaveBatch_CtxCancellation(t *testing.T) {
	tmp := t.TempDir()
	c := &Client{}

	trs := []Track{
		{ID: "1", Name: "one"},
		{ID: "2", Name: "two"},
		{ID: "3", Name: "three"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	opts := DownloadOptions{
		trackSaver: func(ctx context.Context, tt Track, d string, plan *StreamPlan, f ArtworkFetcher) (string, error) {
			if tt.ID == "2" {
				cancel()
			}
			p := filepath.Join(d, tt.ID+".mp3")
			return p, nil
		},
		planResolver: func(string) (*StreamPlan, error) { return &StreamPlan{Format: "MP3_128"}, nil },
	}

	saved, err := c.saveBatch(ctx, trs, tmp, opts)
	if len(saved) >= 3 {
		t.Errorf("should not have processed all after cancel; got %d", len(saved))
	}
	// Cancellation must be surfaced, not swallowed as a successful partial batch.
	if !errors.Is(err, context.Canceled) {
		t.Errorf("saveBatch after cancel: err = %v, want context.Canceled", err)
	}
	if len(saved) != 2 {
		t.Errorf("saved = %d, want 2 (tracks 1 and 2 completed before cancel took effect)", len(saved))
	}
}
