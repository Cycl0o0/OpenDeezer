package deezer

import (
	"encoding/json"
	"fmt"
)

// This file holds per-seed mixes: "start radio from this song / this artist".
// Like Flow (flow.go) these go through gw-light and follow the long-standing
// community conventions for the mix methods.

// TrackMix returns a mix seeded from one track ("song radio"). Uses the gw
// song.getSearchTrackMix method; start_with_input_track keeps the seed track
// as the first entry so playback can begin from the song the user picked.
func (c *Client) TrackMix(trackID string) ([]Track, error) {
	if !c.LoggedIn() {
		return nil, fmt.Errorf("not logged in")
	}
	body := fmt.Sprintf(`{"sng_id":%s,"start_with_input_track":"true"}`, jsonEsc(trackID))
	b, err := c.gw("song.getSearchTrackMix", body)
	if err != nil {
		return nil, err
	}
	return parseGwTrackList(b), nil
}

// ArtistMix returns Deezer's "smart radio" seeded from an artist ("artist
// radio"). Uses the gw smart.getSmartRadio method.
func (c *Client) ArtistMix(artistID string) ([]Track, error) {
	if !c.LoggedIn() {
		return nil, fmt.Errorf("not logged in")
	}
	body := fmt.Sprintf(`{"art_id":%s,"limit":50}`, jsonEsc(artistID))
	b, err := c.gw("smart.getSmartRadio", body)
	if err != nil {
		return nil, err
	}
	return parseGwTrackList(b), nil
}

// parseGwTrackList decodes a gw track-list response body. The results envelope
// is usually {results:{data:[...]}}; some radio/mix variants return a bare
// array ({results:[...]}), so both shapes are accepted (same tolerance as
// Flow). An unrecognized/empty body yields nil, not an error: the gw error
// envelope has already been checked by gw().
func parseGwTrackList(b []byte) []Track {
	var obj struct {
		Results struct {
			Data []gwTrackDTO `json:"data"`
		} `json:"results"`
	}
	if err := json.Unmarshal(b, &obj); err == nil && len(obj.Results.Data) > 0 {
		return gwTracksToTracks(obj.Results.Data)
	}
	var arr struct {
		Results []gwTrackDTO `json:"results"`
	}
	if err := json.Unmarshal(b, &arr); err == nil && len(arr.Results) > 0 {
		return gwTracksToTracks(arr.Results)
	}
	return nil
}
