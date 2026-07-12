package deezer

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// This file holds the client capabilities the playlist import/export package
// (internal/impex) needs: ISRC lookup and a track-scoped search. Both use the
// public REST API, so they work without a Premium plan.

// TrackByISRC resolves a track by its ISRC via the public REST API
// (/track/isrc:<code>). A code Deezer doesn't know yields an error (the REST
// error envelope surfaces through restGet), never an empty Track.
func (c *Client) TrackByISRC(isrc string) (Track, error) {
	isrc = strings.TrimSpace(isrc)
	if isrc == "" {
		return Track{}, fmt.Errorf("empty ISRC")
	}
	b, err := c.restGet("/track/isrc:" + url.PathEscape(isrc))
	if err != nil {
		return Track{}, err
	}
	var dto restTrackDTO
	if err := json.Unmarshal(b, &dto); err != nil {
		return Track{}, err
	}
	t := dto.toTrack()
	if t.ID == "" || t.ID == "0" {
		return Track{}, fmt.Errorf("isrc %s: not found", isrc)
	}
	return t, nil
}

// SearchTracks searches tracks only. With a non-empty artist it uses Deezer's
// advanced query syntax (artist:"..." track:"...") for a targeted match;
// otherwise it falls back to a plain track search on the title.
//
// Live-observed quirk (2026-07): the advanced syntax is precise when it hits
// (artist:"eminem" track:"lose yourself" -> exact match) but returns zero
// results for some multi-word artist phrases (artist:"daft punk"), while a
// plain combined query finds those tracks fine. Callers that get an empty
// result from a targeted query should retry with artist=="" and the artist
// folded into the title (impex.ImportPlaylist's resolver does exactly that).
func (c *Client) SearchTracks(artist, title string) ([]Track, error) {
	// The advanced syntax delimits terms with double quotes, so embedded quotes
	// must be dropped (jsonEsc-style escaping does not apply to search queries).
	clean := func(s string) string {
		return strings.TrimSpace(strings.ReplaceAll(s, `"`, " "))
	}
	artist, title = clean(artist), clean(title)
	var q string
	switch {
	case artist != "" && title != "":
		q = fmt.Sprintf(`artist:"%s" track:"%s"`, artist, title)
	case title != "":
		q = title
	case artist != "":
		q = artist
	default:
		return nil, fmt.Errorf("empty search")
	}
	b, err := c.restGet("/search/track?q=" + url.QueryEscape(q) + "&limit=25")
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []restTrackDTO `json:"data"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	out := make([]Track, 0, len(r.Data))
	for _, t := range r.Data {
		out = append(out, t.toTrack())
	}
	return out, nil
}
