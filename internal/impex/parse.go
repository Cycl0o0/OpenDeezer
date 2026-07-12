package impex

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"
)

// Row is one imported playlist line, before resolution against Deezer.
type Row struct {
	Title  string
	Artist string
	Album  string
	ISRC   string
}

// ParseCSV reads a playlist from r. Two formats are accepted:
//
//   - CSV with an Exportify-style header (Track Name / Artist Name(s) /
//     Album Name / ISRC, with common aliases like Title/Artist/Album), in any
//     column order, extra columns ignored;
//   - a plain text fallback: one "Artist - Title" per line (comment lines
//     starting with '#' — e.g. an M3U's directives — and blank lines are
//     skipped; a line without " - " becomes a title-only row).
//
// Rows without a title are dropped. An input yielding no rows is an error.
func ParseCSV(r io.Reader) ([]Row, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if rows, ok := parseHeaderedCSV(data); ok {
		return rows, nil
	}
	rows := parsePlainLines(data)
	if len(rows) == 0 {
		return nil, fmt.Errorf("no tracks found in input")
	}
	return rows, nil
}

// header aliases, all matched lowercase/trimmed.
var (
	titleCols  = map[string]bool{"track name": true, "title": true, "name": true, "track": true, "song": true}
	artistCols = map[string]bool{"artist name(s)": true, "artist name": true, "artist": true, "artists": true, "artist(s)": true}
	albumCols  = map[string]bool{"album name": true, "album": true}
	isrcCols   = map[string]bool{"isrc": true}
)

// parseHeaderedCSV returns (rows, true) when data is a CSV whose first record
// contains a recognizable title column; (nil, false) otherwise so the caller
// can fall back to plain-text parsing.
func parseHeaderedCSV(data []byte) ([]Row, bool) {
	cr := csv.NewReader(strings.NewReader(string(data)))
	cr.FieldsPerRecord = -1 // tolerate ragged rows
	cr.LazyQuotes = true

	header, err := cr.Read()
	if err != nil {
		return nil, false
	}
	titleIdx, artistIdx, albumIdx, isrcIdx := -1, -1, -1, -1
	for i, h := range header {
		key := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\uFEFF")))
		switch {
		case titleCols[key] && titleIdx < 0:
			titleIdx = i
		case artistCols[key] && artistIdx < 0:
			artistIdx = i
		case albumCols[key] && albumIdx < 0:
			albumIdx = i
		case isrcCols[key] && isrcIdx < 0:
			isrcIdx = i
		}
	}
	if titleIdx < 0 {
		return nil, false
	}
	at := func(rec []string, i int) string {
		if i < 0 || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}
	var rows []Row
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue // skip a malformed record rather than losing the rest
		}
		row := Row{
			Title:  at(rec, titleIdx),
			Artist: at(rec, artistIdx),
			Album:  at(rec, albumIdx),
			ISRC:   at(rec, isrcIdx),
		}
		if row.Title == "" {
			continue
		}
		rows = append(rows, row)
	}
	return rows, true
}

// parsePlainLines parses the "Artist - Title" text fallback.
func parsePlainLines(data []byte) []Row {
	var rows []Row
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		artist, title, found := strings.Cut(line, " - ")
		if !found {
			rows = append(rows, Row{Title: line})
			continue
		}
		artist, title = strings.TrimSpace(artist), strings.TrimSpace(title)
		if title == "" {
			title, artist = artist, ""
		}
		if title == "" {
			continue
		}
		rows = append(rows, Row{Title: title, Artist: artist})
	}
	return rows
}
