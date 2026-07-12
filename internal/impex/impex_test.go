package impex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/deezer"
)

func track(id, title, artist, album string) deezer.Track {
	return deezer.Track{
		ID:         id,
		Name:       title,
		DurationMS: 200000,
		Artists:    []deezer.Artist{{ID: "a" + id, Name: artist}},
		AlbumName:  album,
	}
}

// ---- parsing ----

func TestParseCSVExportifyStyle(t *testing.T) {
	// Real Exportify files carry many more columns, in arbitrary order.
	in := `Track URI,Track Name,Artist URI(s),Artist Name(s),Album Name,Track Duration (ms),ISRC,Added At
spotify:track:x,One More Time,spotify:artist:y,Daft Punk,Discovery,320000,GBDUW0000059,2020-01-01
spotify:track:z,"Harder, Better, Faster, Stronger",spotify:artist:y,Daft Punk,Discovery,224000,GBDUW0000061,2020-01-02
`
	rows, err := ParseCSV(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	want0 := Row{Title: "One More Time", Artist: "Daft Punk", Album: "Discovery", ISRC: "GBDUW0000059"}
	if rows[0] != want0 {
		t.Errorf("row 0 = %+v, want %+v", rows[0], want0)
	}
	if rows[1].Title != "Harder, Better, Faster, Stronger" || rows[1].ISRC != "GBDUW0000061" {
		t.Errorf("row 1 = %+v", rows[1])
	}
}

func TestParseCSVHeaderAliasesAndBOM(t *testing.T) {
	in := "\uFEFFTitle,Artist,Album\nSong A,Ann,Album X\n,Empty Title Skipped,\n"
	rows, err := ParseCSV(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(rows) != 1 || rows[0].Title != "Song A" || rows[0].Artist != "Ann" || rows[0].Album != "Album X" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestParseCSVPlainTextFallback(t *testing.T) {
	in := "# my playlist\nDaft Punk - One More Time\n\nQueen - Bohemian Rhapsody\nJustATitle\n"
	rows, err := ParseCSV(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3: %+v", len(rows), rows)
	}
	if rows[0] != (Row{Title: "One More Time", Artist: "Daft Punk"}) {
		t.Errorf("row 0 = %+v", rows[0])
	}
	if rows[2] != (Row{Title: "JustATitle"}) {
		t.Errorf("row 2 = %+v", rows[2])
	}
}

func TestParseCSVEmptyInput(t *testing.T) {
	if _, err := ParseCSV(strings.NewReader("")); err == nil {
		t.Error("empty input should be an error")
	}
	if _, err := ParseCSV(strings.NewReader("# only comments\n\n")); err == nil {
		t.Error("comment-only input should be an error")
	}
}

// ---- export ----

func TestExportCSVRoundTrip(t *testing.T) {
	tracks := []deezer.Track{
		track("1", "One More Time", "Daft Punk", "Discovery"),
		track("2", `Say "Hello"`, "Ann, Bob", "Greetings"),
	}
	var buf bytes.Buffer
	if err := ExportCSV(&buf, "My List", tracks); err != nil {
		t.Fatalf("ExportCSV: %v", err)
	}
	rows, err := ParseCSV(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("round-trip rows = %+v", rows)
	}
	if rows[0].Title != "One More Time" || rows[0].Artist != "Daft Punk" || rows[0].Album != "Discovery" {
		t.Errorf("row 0 = %+v", rows[0])
	}
	// Quotes and commas must survive CSV quoting.
	if rows[1].Title != `Say "Hello"` || rows[1].Artist != "Ann, Bob" {
		t.Errorf("row 1 = %+v", rows[1])
	}
	// ISRC column exists but is empty (deezer.Track carries no ISRC).
	if rows[0].ISRC != "" {
		t.Errorf("ISRC = %q, want empty", rows[0].ISRC)
	}
}

func TestExportM3U(t *testing.T) {
	var buf bytes.Buffer
	tr := track("3135556", "Harder", "Daft Punk", "Discovery")
	if err := ExportM3U(&buf, "Mix\nTape", []deezer.Track{tr}); err != nil {
		t.Fatalf("ExportM3U: %v", err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "#EXTM3U\n") {
		t.Errorf("missing #EXTM3U header: %q", out)
	}
	if !strings.Contains(out, "#PLAYLIST:Mix Tape\n") {
		t.Errorf("playlist name not sanitized/present: %q", out)
	}
	if !strings.Contains(out, "#EXTINF:200,Daft Punk - Harder\n") {
		t.Errorf("missing EXTINF: %q", out)
	}
	if !strings.Contains(out, "https://www.deezer.com/track/3135556\n") {
		t.Errorf("missing track URL: %q", out)
	}
}

func TestExportJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := ExportJSON(&buf, "My List", []deezer.Track{track("1", "A", "X", "Al")}); err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}
	var got jsonPlaylist
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != "My List" || len(got.Tracks) != 1 || got.Tracks[0].ID != "1" ||
		got.Tracks[0].Title != "A" || len(got.Tracks[0].Artists) != 1 || got.Tracks[0].Artists[0] != "X" {
		t.Errorf("got %+v", got)
	}
}

// ---- normalization ----

func TestNormalize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Beyoncé feat. JAY-Z", "beyonce"},
		{"One More Time (Radio Edit)", "one more time radio edit"},
		{"AC/DC", "ac dc"},
		{"Simon & Garfunkel", "simon and garfunkel"},
		{"  Múm — Ballad  ", "mum ballad"},
		{"Sigur Rós", "sigur ros"},
		{"song ft. someone", "song"},
	}
	for _, tc := range cases {
		if got := normalize(tc.in); got != tc.want {
			t.Errorf("normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := normalizeBase("One More Time (Radio Edit) [feat. Nobody]"); got != "one more time" {
		t.Errorf("normalizeBase = %q, want %q", got, "one more time")
	}
}

// ---- resolution ----

type fakeResolver struct {
	byISRC   map[string]deezer.Track
	bySearch map[string][]deezer.Track // key: artist + "|" + title
	searches int
}

func (f *fakeResolver) ByISRC(isrc string) (deezer.Track, error) {
	if t, ok := f.byISRC[isrc]; ok {
		return t, nil
	}
	return deezer.Track{}, errors.New("not found")
}

func (f *fakeResolver) Search(artist, title string) ([]deezer.Track, error) {
	f.searches++
	return f.bySearch[artist+"|"+title], nil
}

type fakeCreator struct {
	title string
	ids   []string
	err   error
}

func (f *fakeCreator) CreatePlaylist(title string, trackIDs []string) (string, error) {
	f.title, f.ids = title, trackIDs
	if f.err != nil {
		return "", f.err
	}
	return "pl123", nil
}

func TestImportPlaylist(t *testing.T) {
	res := &fakeResolver{
		byISRC: map[string]deezer.Track{
			"GBDUW0000059": track("10", "One More Time", "Daft Punk", "Discovery"),
		},
		bySearch: map[string][]deezer.Track{
			// Accents + feat. credit on the candidate; plain text in the row.
			"Beyonce|Halo": {track("20", "Halo (Live)", "Nobody", ""), track("21", "Halo", "Beyoncé feat. Jay-Z", "I Am")},
			// Only matches via the base-title (parentheticals stripped) pass.
			"Queen|Bohemian Rhapsody": {track("30", "Bohemian Rhapsody (Remastered 2011)", "Queen", "")},
		},
	}
	pc := &fakeCreator{}
	rows := []Row{
		{Title: "ignored by isrc", Artist: "x", ISRC: "GBDUW0000059"},
		{Title: "Halo", Artist: "Beyonce"},
		{Title: "Bohemian Rhapsody", Artist: "Queen"},
		{Title: "Nonexistent Song", Artist: "Nobody Ever"},
	}
	var calls []bool
	got, err := ImportPlaylist(context.Background(), res, pc, "Imported", rows,
		func(i, total int, matched bool) {
			if total != 4 || i != len(calls)+1 {
				t.Errorf("progress(i=%d, total=%d) out of order", i, total)
			}
			calls = append(calls, matched)
		})
	if err != nil {
		t.Fatalf("ImportPlaylist: %v", err)
	}
	if got.PlaylistID != "pl123" || pc.title != "Imported" {
		t.Errorf("playlist = %q title %q", got.PlaylistID, pc.title)
	}
	wantIDs := []string{"10", "21", "30"}
	if len(pc.ids) != 3 || pc.ids[0] != wantIDs[0] || pc.ids[1] != wantIDs[1] || pc.ids[2] != wantIDs[2] {
		t.Errorf("created with ids %v, want %v", pc.ids, wantIDs)
	}
	if len(got.Unmatched) != 1 || got.Unmatched[0].Title != "Nonexistent Song" {
		t.Errorf("unmatched = %+v", got.Unmatched)
	}
	wantCalls := []bool{true, true, true, false}
	if len(calls) != 4 {
		t.Fatalf("progress called %d times, want 4", len(calls))
	}
	for i := range wantCalls {
		if calls[i] != wantCalls[i] {
			t.Errorf("progress[%d] matched = %v, want %v", i, calls[i], wantCalls[i])
		}
	}
}

func TestImportPlaylistNoMatches(t *testing.T) {
	res := &fakeResolver{}
	pc := &fakeCreator{}
	got, err := ImportPlaylist(context.Background(), res, pc, "T",
		[]Row{{Title: "X", Artist: "Y"}}, nil)
	if err == nil {
		t.Fatal("want error when nothing matches")
	}
	if pc.ids != nil {
		t.Error("playlist must not be created with zero tracks")
	}
	if len(got.Unmatched) != 1 {
		t.Errorf("unmatched = %+v", got.Unmatched)
	}
}

func TestImportPlaylistContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ImportPlaylist(ctx, &fakeResolver{}, &fakeCreator{}, "T",
		[]Row{{Title: "X"}}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestImportPlaylistBroadFallbackAfterNearMiss(t *testing.T) {
	// The targeted artist+title search returns candidates, but none passes
	// bestMatch (wrong artist on a same-base-title live cut); the broad
	// full-text fallback has the real track. Previously the fallback only ran
	// when the targeted search came back empty, so this row failed to resolve.
	res := &fakeResolver{
		bySearch: map[string][]deezer.Track{
			"Daft Punk|Around the World": {track("41", "Around the World (Live 2007)", "Somebody Else", "")},
			"|Daft Punk Around the World": {
				track("41", "Around the World (Live 2007)", "Somebody Else", ""),
				track("40", "Around the World", "Daft Punk", "Homework"),
			},
		},
	}
	pc := &fakeCreator{}
	got, err := ImportPlaylist(context.Background(), res, pc, "T",
		[]Row{{Title: "Around the World", Artist: "Daft Punk"}}, nil)
	if err != nil {
		t.Fatalf("ImportPlaylist: %v", err)
	}
	if len(got.Matched) != 1 || got.Matched[0].ID != "40" {
		t.Errorf("matched = %+v, want the broad-search hit (id 40)", got.Matched)
	}
	if res.searches != 2 {
		t.Errorf("searches = %d, want 2 (targeted near-miss, then broad)", res.searches)
	}
}

func TestImportPlaylistTitleOnlyFallbackQuery(t *testing.T) {
	// The targeted artist+title search returns nothing; the plain-title
	// fallback ("artist title" as one query) finds it.
	res := &fakeResolver{
		bySearch: map[string][]deezer.Track{
			"|Daft Punk Around the World": {track("40", "Around the World", "Daft Punk", "Homework")},
		},
	}
	pc := &fakeCreator{}
	got, err := ImportPlaylist(context.Background(), res, pc, "T",
		[]Row{{Title: "Around the World", Artist: "Daft Punk"}}, nil)
	if err != nil {
		t.Fatalf("ImportPlaylist: %v", err)
	}
	if len(got.Matched) != 1 || got.Matched[0].ID != "40" {
		t.Errorf("matched = %+v", got.Matched)
	}
	if res.searches != 2 {
		t.Errorf("searches = %d, want 2 (targeted then fallback)", res.searches)
	}
}
