package deezer

import (
	"encoding/json"
	"fmt"
)

// Flow returns Deezer's personalized "Flow" radio: an endless, taste-based
// track stream. Uses the gw radio.getUserRadio method (community convention);
// needs live validation against a real account.
func (c *Client) Flow() ([]Track, error) {
	if !c.LoggedIn() {
		return nil, fmt.Errorf("not logged in")
	}
	b, err := c.gw("radio.getUserRadio", fmt.Sprintf(`{"user_id":"%s"}`, c.uid()))
	if err != nil {
		return nil, err
	}
	// results is usually {data:[...]}; some variants return a bare array.
	var obj struct {
		Results struct {
			Data []gwTrackDTO `json:"data"`
		} `json:"results"`
	}
	if err := json.Unmarshal(b, &obj); err == nil && len(obj.Results.Data) > 0 {
		return gwTracksToTracks(obj.Results.Data), nil
	}
	var arr struct {
		Results []gwTrackDTO `json:"results"`
	}
	if err := json.Unmarshal(b, &arr); err == nil && len(arr.Results) > 0 {
		return gwTracksToTracks(arr.Results), nil
	}
	return nil, nil
}

// gwTracksToTracks maps gw DTOs to Tracks.
func gwTracksToTracks(in []gwTrackDTO) []Track {
	out := make([]Track, 0, len(in))
	for _, t := range in {
		out = append(out, t.toTrack())
	}
	return out
}
