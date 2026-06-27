package deezer

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// SearchPodcasts finds shows via the public REST /search/podcast endpoint.
func (c *Client) SearchPodcasts(query string) ([]Podcast, error) {
	b, err := c.restGet("/search/podcast?q=" + url.QueryEscape(query) + "&limit=40")
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []struct {
			ID            json.Number `json:"id"`
			Title         string      `json:"title"`
			Description   string      `json:"description"`
			PictureMedium string      `json:"picture_medium"`
			NbEpisodes    int         `json:"nb_episodes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	out := make([]Podcast, 0, len(r.Data))
	for _, p := range r.Data {
		out = append(out, Podcast{
			ID: p.ID.String(), Name: p.Title, Description: p.Description,
			ArtworkURL: p.PictureMedium, EpisodeCount: p.NbEpisodes,
		})
	}
	return out, nil
}

// PodcastEpisodes lists a show's episodes (public REST).
func (c *Client) PodcastEpisodes(podcastID string) ([]Episode, error) {
	b, err := c.restGet("/podcast/" + podcastID + "/episodes?limit=100")
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []struct {
			ID            json.Number `json:"id"`
			Title         string      `json:"title"`
			Description   string      `json:"description"`
			ReleaseDate   string      `json:"release_date"`
			Duration      json.Number `json:"duration"`
			PictureMedium string      `json:"picture_medium"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	out := make([]Episode, 0, len(r.Data))
	for _, e := range r.Data {
		dur, _ := e.Duration.Int64()
		out = append(out, Episode{
			ID: e.ID.String(), Title: e.Title, Description: e.Description,
			ReleaseDate: e.ReleaseDate, DurationMS: dur * 1000, ArtworkURL: e.PictureMedium,
		})
	}
	return out, nil
}

// PodcastEpisodeStream resolves an episode to a plain (unencrypted) MP3 stream
// via gw episode.getData. The player decodes it directly (no Blowfish).
func (c *Client) PodcastEpisodeStream(episodeID string) (*StreamPlan, error) {
	if !c.LoggedIn() {
		return nil, fmt.Errorf("not logged in")
	}
	b, err := c.gw("episode.getData", fmt.Sprintf(`{"episode_id":"%s"}`, episodeID))
	if err != nil {
		return nil, err
	}
	var r struct {
		Results struct {
			URL string `json:"EPISODE_DIRECT_STREAM_URL"`
		} `json:"results"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.Results.URL == "" {
		return nil, fmt.Errorf("episode %s: no stream url", episodeID)
	}
	return &StreamPlan{CDNURL: r.Results.URL, TrackID: episodeID, Format: "MP3", Encrypted: false}, nil
}
