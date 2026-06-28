package deezer

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	odlog "github.com/Cycl0o0/OpenDeezer/internal/log"
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
		Results map[string]json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}

	// Diagnostics: log every key + a value preview so the real stream-url field can
	// be identified from a single playback attempt ($OPENDEEZER_LOG=debug).
	keys := make([]string, 0, len(r.Results))
	for k := range r.Results {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	odlog.Debug("episode %s gw keys: %s", episodeID, strings.Join(keys, ","))
	for _, k := range keys {
		odlog.Debug("episode %s field %s = %s", episodeID, k, preview(r.Results[k]))
	}

	// Fast path: the direct (DRM-free) MP3 URL field name has varied across the
	// API; try the known ones first.
	for _, k := range []string{"EPISODE_DIRECT_STREAM_URL", "DIRECT_STREAM_URL", "EPISODE_STREAM_URL", "MEDIA_URL"} {
		if raw, ok := r.Results[k]; ok {
			var u string
			if json.Unmarshal(raw, &u) == nil && strings.HasPrefix(u, "http") {
				odlog.Info("episode %s: stream url from field %s", episodeID, k)
				return &StreamPlan{CDNURL: u, TrackID: episodeID, Format: "MP3", Encrypted: false}, nil
			}
		}
	}

	// Generic fallback: scan all fields for an http(s) value that looks like a
	// media stream (not artwork), so a renamed field still resolves. Prefer keys
	// mentioning STREAM/MEDIA/URL; skip picture/image/cover fields.
	if u, k := findStreamURL(r.Results); u != "" {
		odlog.Info("episode %s: stream url detected from field %s", episodeID, k)
		return &StreamPlan{CDNURL: u, TrackID: episodeID, Format: "MP3", Encrypted: false}, nil
	}

	return nil, fmt.Errorf("episode %s: no direct stream url (gw keys: %s)", episodeID, strings.Join(keys, ","))
}

// preview renders a raw JSON value as a short string for logging.
func preview(raw json.RawMessage) string {
	s := string(raw)
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// findStreamURL scans gw results for the best http(s) URL that looks like an
// audio stream (skipping artwork), returning the value and its field name.
func findStreamURL(results map[string]json.RawMessage) (string, string) {
	var bestURL, bestKey string
	bestScore := 0
	for k, raw := range results {
		var v string
		if json.Unmarshal(raw, &v) != nil || !strings.HasPrefix(v, "http") {
			continue
		}
		ku := strings.ToUpper(k)
		if strings.Contains(ku, "PICTURE") || strings.Contains(ku, "IMAGE") || strings.Contains(ku, "COVER") {
			continue
		}
		lv := strings.ToLower(v)
		if strings.HasSuffix(lv, ".jpg") || strings.HasSuffix(lv, ".jpeg") || strings.HasSuffix(lv, ".png") || strings.HasSuffix(lv, ".gif") {
			continue
		}
		score := 1
		switch {
		case strings.Contains(ku, "STREAM"):
			score = 3
		case strings.Contains(ku, "MEDIA") || strings.Contains(ku, "URL"):
			score = 2
		}
		if score > bestScore {
			bestScore, bestURL, bestKey = score, v, k
		}
	}
	return bestURL, bestKey
}
