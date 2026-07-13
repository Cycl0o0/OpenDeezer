// Package impex imports and exports playlists as local files (CSV in the
// common Exportify style, extended M3U, JSON) and resolves imported rows back
// to Deezer tracks (ISRC-first, then a fuzzy artist+title search). All file
// handling is local; only resolution/creation touches the Deezer API, through
// narrow interfaces so it stays fully testable offline.
package impex

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"

	"github.com/Cycl0o0/OpenDeezer/v3/internal/deezer"
)

// csvHeader matches Exportify's column names for the fields this app has.
// deezer.Track carries no ISRC, so that column is emitted empty — kept in the
// header so re-imports and third-party tools see the familiar shape.
var csvHeader = []string{"Track Name", "Artist Name(s)", "Album Name", "ISRC"}

// ExportCSV writes tracks as an Exportify-style CSV. The playlist name is not
// part of the CSV format (Exportify carries it in the filename), so name is
// unused here; it is kept for signature symmetry with the other exporters.
func ExportCSV(w io.Writer, name string, tracks []deezer.Track) error {
	_ = name
	cw := csv.NewWriter(w)
	if err := cw.Write(csvHeader); err != nil {
		return err
	}
	for _, t := range tracks {
		if err := cw.Write([]string{t.Name, t.ArtistLine(), t.AlbumName, ""}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// ExportM3U writes an extended M3U playlist. Each entry's location is the
// track's public Deezer URL so the id survives a round-trip; #EXTINF carries
// duration and "Artist - Title" as usual.
func ExportM3U(w io.Writer, name string, tracks []deezer.Track) error {
	if _, err := fmt.Fprintln(w, "#EXTM3U"); err != nil {
		return err
	}
	if name != "" {
		if _, err := fmt.Fprintf(w, "#PLAYLIST:%s\n", sanitizeLine(name)); err != nil {
			return err
		}
	}
	for _, t := range tracks {
		secs := t.DurationMS / 1000
		label := sanitizeLine(t.ArtistLine() + " - " + t.Name)
		if _, err := fmt.Fprintf(w, "#EXTINF:%d,%s\nhttps://www.deezer.com/track/%s\n",
			secs, label, t.ID); err != nil {
			return err
		}
	}
	return nil
}

// sanitizeLine keeps M3U directives one-line even if metadata embeds newlines.
func sanitizeLine(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\n' || r == '\r' {
			r = ' '
		}
		out = append(out, r)
	}
	return string(out)
}

// jsonPlaylist is the JSON export schema.
type jsonPlaylist struct {
	Name   string      `json:"name"`
	Tracks []jsonTrack `json:"tracks"`
}

type jsonTrack struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Artists    []string `json:"artists"`
	Album      string   `json:"album,omitempty"`
	DurationMS int64    `json:"durationMs,omitempty"`
	ISRC       string   `json:"isrc,omitempty"` // always empty today (Track carries no ISRC)
}

// ExportJSON writes tracks as a self-describing JSON document.
func ExportJSON(w io.Writer, name string, tracks []deezer.Track) error {
	p := jsonPlaylist{Name: name, Tracks: make([]jsonTrack, 0, len(tracks))}
	for _, t := range tracks {
		artists := make([]string, 0, len(t.Artists))
		for _, a := range t.Artists {
			artists = append(artists, a.Name)
		}
		p.Tracks = append(p.Tracks, jsonTrack{
			ID:         t.ID,
			Title:      t.Name,
			Artists:    artists,
			Album:      t.AlbumName,
			DurationMS: t.DurationMS,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}
