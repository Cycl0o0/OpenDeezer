package impex

import (
	"context"
	"fmt"
	"strings"

	"github.com/Cycl0o0/OpenDeezer/v3/internal/deezer"
)

// Resolver looks imported rows up on Deezer. Narrow so tests use a fake.
type Resolver interface {
	ByISRC(isrc string) (deezer.Track, error)
	Search(artist, title string) ([]deezer.Track, error)
}

// PlaylistCreator creates the destination playlist. *deezer.Client satisfies
// it directly (CreatePlaylist in library.go).
type PlaylistCreator interface {
	CreatePlaylist(title string, trackIDs []string) (string, error)
}

// clientResolver is the production Resolver backed by *deezer.Client
// (TrackByISRC / SearchTracks, both public REST).
type clientResolver struct{ c *deezer.Client }

// NewClientResolver adapts a *deezer.Client into a Resolver.
func NewClientResolver(c *deezer.Client) Resolver { return clientResolver{c} }

func (r clientResolver) ByISRC(isrc string) (deezer.Track, error) { return r.c.TrackByISRC(isrc) }
func (r clientResolver) Search(artist, title string) ([]deezer.Track, error) {
	return r.c.SearchTracks(artist, title)
}

// ImportResult is what ImportPlaylist produced.
type ImportResult struct {
	PlaylistID string
	Matched    []deezer.Track // resolved tracks, input order
	Unmatched  []Row          // rows that resolved to nothing
}

// ImportPlaylist resolves rows against Deezer (ISRC first, then a fuzzy
// artist+title search) and creates a playlist with everything that matched.
// progress, when non-nil, is called after each row with i = rows processed so
// far (1..total). Unmatched rows are collected, not fatal; only zero matches
// (or a creation failure) is an error — the partial result is still returned
// so the caller can show what went wrong.
func ImportPlaylist(ctx context.Context, r Resolver, pc PlaylistCreator, title string,
	rows []Row, progress func(i, total int, matched bool)) (*ImportResult, error) {
	res := &ImportResult{}
	total := len(rows)
	for i, row := range rows {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		t, ok := resolveRow(r, row)
		if ok {
			res.Matched = append(res.Matched, t)
		} else {
			res.Unmatched = append(res.Unmatched, row)
		}
		if progress != nil {
			progress(i+1, total, ok)
		}
	}
	if len(res.Matched) == 0 {
		return res, fmt.Errorf("no rows matched a Deezer track (%d tried)", total)
	}
	ids := make([]string, len(res.Matched))
	for i, t := range res.Matched {
		ids[i] = t.ID
	}
	id, err := pc.CreatePlaylist(title, ids)
	if err != nil {
		return res, fmt.Errorf("create playlist: %w", err)
	}
	res.PlaylistID = id
	return res, nil
}

// resolveRow tries ISRC first (exact, catalog-level id), then a fuzzy
// artist+title search.
func resolveRow(r Resolver, row Row) (deezer.Track, bool) {
	if isrc := strings.TrimSpace(row.ISRC); isrc != "" {
		if t, err := r.ByISRC(isrc); err == nil && t.ID != "" {
			return t, true
		}
	}
	if strings.TrimSpace(row.Title) == "" {
		return deezer.Track{}, false
	}
	if cands, err := r.Search(row.Artist, row.Title); err == nil && len(cands) > 0 {
		if t, ok := bestMatch(cands, row.Artist, row.Title); ok {
			return t, true
		}
	}
	// The targeted artist+title query can be too strict (e.g. artist name
	// spelled differently) or return only near-misses that bestMatch rejects;
	// retry once as a plain full-text search before giving up. bestMatch keeps
	// the same acceptance criteria, so this only widens the candidate pool,
	// never what counts as a match.
	if strings.TrimSpace(row.Artist) == "" {
		return deezer.Track{}, false
	}
	cands, err := r.Search("", row.Artist+" "+row.Title)
	if err != nil || len(cands) == 0 {
		return deezer.Track{}, false
	}
	return bestMatch(cands, row.Artist, row.Title)
}

// bestMatch picks the first candidate whose normalized title+artist agree with
// the row: pass 1 requires an exact normalized title, pass 2 relaxes the title
// to its base form (parentheticals and feat. credits stripped). An empty input
// artist matches any candidate artist.
func bestMatch(cands []deezer.Track, artist, title string) (deezer.Track, bool) {
	wantTitle := normalize(title)
	wantBase := normalizeBase(title)
	wantArtist := normalize(artist)
	for _, t := range cands {
		if normalize(t.Name) == wantTitle && artistsMatch(t, wantArtist) {
			return t, true
		}
	}
	for _, t := range cands {
		if normalizeBase(t.Name) == wantBase && artistsMatch(t, wantArtist) {
			return t, true
		}
	}
	return deezer.Track{}, false
}

// artistsMatch reports whether the normalized input artist plausibly names one
// of the track's credited artists (either direction of containment, so
// "beyonce" matches "Beyoncé feat. Jay-Z" and vice versa).
func artistsMatch(t deezer.Track, wantArtist string) bool {
	if wantArtist == "" {
		return true
	}
	for _, a := range t.Artists {
		got := normalize(a.Name)
		if got == "" {
			continue
		}
		if strings.Contains(got, wantArtist) || strings.Contains(wantArtist, got) {
			return true
		}
	}
	// Multi-artist credit lines ("A, B") joined may contain the input too.
	line := normalize(t.ArtistLine())
	return line != "" && (strings.Contains(line, wantArtist) || strings.Contains(wantArtist, line))
}

// normalize lowercases, folds common accents to ASCII, unifies "&"/"and",
// drops feat./ft./featuring credits, strips punctuation and collapses spaces —
// so "Beyoncé feat. JAY-Z" and "beyonce" compare equal.
func normalize(s string) string {
	s = foldAccents(strings.ToLower(s))
	s = stripFeat(s)
	s = strings.ReplaceAll(s, "&", " and ")
	var b strings.Builder
	b.Grow(len(s))
	space := true // collapse runs; also trims leading spaces
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			space = false
		default:
			if !space {
				b.WriteByte(' ')
				space = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// normalizeBase is normalize with parenthesized/bracketed segments removed
// first, so "One More Time (Radio Edit)" matches "One More Time".
func normalizeBase(s string) string {
	return normalize(stripBrackets(s))
}

// stripFeat removes a "feat./ft./featuring <credits>" tail. Input is already
// lowercase. Bracketed feat. segments are handled by stripBrackets in the base
// form; this catches the unbracketed "song feat. someone" style too.
func stripFeat(s string) string {
	for _, marker := range []string{" feat. ", " feat ", " ft. ", " ft ", " featuring ", "(feat", "[feat", "(ft", "[ft"} {
		if i := strings.Index(s, marker); i >= 0 {
			s = s[:i]
		}
	}
	return s
}

// stripBrackets removes (...) and [...] segments.
func stripBrackets(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	depth := 0
	for _, r := range s {
		switch r {
		case '(', '[':
			depth++
		case ')', ']':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// accentFold maps common accented Latin runes to ASCII. Enough for music
// metadata matching without pulling x/text into the direct dependency set.
var accentFold = map[rune]string{
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'æ': "ae",
	'ç': "c", 'č': "c", 'ć': "c",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ě': "e", 'ę': "e",
	'ì': "i", 'í': "i", 'î': "i", 'ï': "i",
	'ñ': "n", 'ń': "n",
	'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o", 'œ': "oe",
	'ù': "u", 'ú': "u", 'û': "u", 'ü': "u", 'ů': "u",
	'ý': "y", 'ÿ': "y",
	'ß': "ss", 'š': "s", 'ś': "s",
	'ž': "z", 'ź': "z", 'ż': "z",
	'ł': "l", 'đ': "d", 'ð': "d", 'þ': "th",
}

// foldAccents rewrites accented runes to their ASCII base (input lowercase).
func foldAccents(s string) string {
	// Fast path: pure ASCII needs no rewrite.
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if rep, ok := accentFold[r]; ok {
			b.WriteString(rep)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
