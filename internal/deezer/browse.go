package deezer

import (
	"encoding/json"
	"fmt"
)

// ---- shared REST DTOs ----

type restAlbumDTO struct {
	ID          json.Number `json:"id"`
	Title       string      `json:"title"`
	Artist      restArtist  `json:"artist"`
	CoverMedium string      `json:"cover_medium"`
}

func (a restAlbumDTO) toAlbum() Album {
	return Album{
		ID:         a.ID.String(),
		Name:       a.Title,
		Artists:    []Artist{{ID: a.Artist.ID.String(), Name: a.Artist.Name}},
		ArtworkURL: a.CoverMedium,
	}
}

type restArtistDTO struct {
	ID            json.Number `json:"id"`
	Name          string      `json:"name"`
	PictureMedium string      `json:"picture_medium"`
	NbFan         int         `json:"nb_fan"`
}

func (a restArtistDTO) toArtistInfo() ArtistInfo {
	return ArtistInfo{
		ID:         a.ID.String(),
		Name:       a.Name,
		ArtworkURL: a.PictureMedium,
		NbFans:     a.NbFan,
	}
}

// Charts fetches the global top tracks/albums/artists/playlists from the public
// REST /chart endpoint (no auth). Pass genreID "0" for the global chart.
func (c *Client) Charts(genreID string) (*Chart, error) {
	if genreID == "" {
		genreID = "0"
	}
	b, err := c.restGet("/chart/" + genreID + "?limit=50")
	if err != nil {
		return nil, err
	}
	var r struct {
		Tracks    struct{ Data []restTrackDTO }  `json:"tracks"`
		Albums    struct{ Data []restAlbumDTO }  `json:"albums"`
		Artists   struct{ Data []restArtistDTO } `json:"artists"`
		Playlists struct {
			Data []struct {
				ID            json.Number           `json:"id"`
				Title         string                `json:"title"`
				User          struct{ Name string } `json:"user"`
				NbTracks      int                   `json:"nb_tracks"`
				PictureMedium string                `json:"picture_medium"`
			}
		} `json:"playlists"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	ch := &Chart{}
	for _, t := range r.Tracks.Data {
		ch.Tracks = append(ch.Tracks, t.toTrack())
	}
	for _, a := range r.Albums.Data {
		ch.Albums = append(ch.Albums, a.toAlbum())
	}
	for _, a := range r.Artists.Data {
		ch.Artists = append(ch.Artists, a.toArtistInfo())
	}
	for _, p := range r.Playlists.Data {
		ch.Playlists = append(ch.Playlists, Playlist{
			ID: p.ID.String(), Name: p.Title, Owner: p.User.Name,
			TrackCount: p.NbTracks, ArtworkURL: p.PictureMedium,
		})
	}
	return ch, nil
}

// ArtistTop lists an artist's most popular tracks (public REST).
func (c *Client) ArtistTop(id string) ([]Track, error) {
	b, err := c.restGet("/artist/" + id + "/top?limit=50")
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

// ArtistProfile fetches an artist's profile plus top tracks, albums and related
// artists in one call (all public REST). A failure on any sub-list is tolerated
// so a partial profile still renders.
func (c *Client) ArtistProfile(id string) (*ArtistPage, error) {
	b, err := c.restGet("/artist/" + id)
	if err != nil {
		return nil, err
	}
	var info restArtistDTO
	if err := json.Unmarshal(b, &info); err != nil {
		return nil, err
	}
	page := &ArtistPage{Artist: info.toArtistInfo()}

	page.Top, _ = c.ArtistTop(id)

	if ab, err := c.restGet("/artist/" + id + "/albums?limit=50"); err == nil {
		var r struct {
			Data []restAlbumDTO `json:"data"`
		}
		if json.Unmarshal(ab, &r) == nil {
			for _, a := range r.Data {
				page.Albums = append(page.Albums, a.toAlbum())
			}
		}
	}
	if rb, err := c.restGet("/artist/" + id + "/related?limit=20"); err == nil {
		var r struct {
			Data []restArtistDTO `json:"data"`
		}
		if json.Unmarshal(rb, &r) == nil {
			for _, a := range r.Data {
				page.Related = append(page.Related, a.toArtistInfo())
			}
		}
	}
	return page, nil
}

// Lyrics fetches a track's lyrics via gw song.getLyrics, returning both the
// plain text and, when Deezer provides them, time-synced lines.
func (c *Client) Lyrics(trackID string) (*Lyrics, error) {
	b, err := c.gw("song.getLyrics", fmt.Sprintf(`{"sng_id":%s}`, jsonEsc(trackID)))
	if err != nil {
		return nil, err
	}
	var r struct {
		Results struct {
			LyricsText string `json:"LYRICS_TEXT"`
			Sync       []struct {
				Milliseconds json.Number `json:"milliseconds"`
				Line         string      `json:"line"`
			} `json:"LYRICS_SYNC_JSON"`
		} `json:"results"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	lyr := &Lyrics{Plain: r.Results.LyricsText}
	for _, s := range r.Results.Sync {
		ms, _ := s.Milliseconds.Int64()
		// Sync arrays include blank separator entries; keep timing, drop noise.
		lyr.Synced = append(lyr.Synced, LyricLine{TimeMS: ms, Text: s.Line})
	}
	return lyr, nil
}
