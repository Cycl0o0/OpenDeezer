package deezer

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTrackIDOf(t *testing.T) {
	cases := []struct{ uri, want string }{
		{"deezer:track:3135556", "3135556"},
		{"https://www.deezer.com/track/3135556", "3135556"},
		{"3135556", "3135556"},
		// Share links carry query params whose digits must not pollute the id.
		{"https://www.deezer.com/track/3135556?utm_campaign=clipboard-generic&utm_content=track-3135556", "3135556"},
		{"https://www.deezer.com/en/track/3135556?host=0", "3135556"},
		{"https://www.deezer.com/en/track/3135556/", "3135556"},
		{"https://www.deezer.com/en/track/3135556#foo", "3135556"},
		// Negative SNG_IDs (user uploads) keep their sign.
		{"-123456789", "-123456789"},
		{"deezer:track:-123456789", "-123456789"},
		// Non-numeric input is rejected instead of digit-filtered.
		{"garbage", ""},
		{"https://www.deezer.com/en/album/", ""},
		{"", ""},
		{"-", ""},
	}
	for _, tc := range cases {
		if got := TrackIDOf(tc.uri); got != tc.want {
			t.Errorf("TrackIDOf(%q) = %q, want %q", tc.uri, got, tc.want)
		}
	}
}

func TestJSONEsc(t *testing.T) {
	cases := []struct{ in, want string }{
		{"3135556", `"3135556"`},
		{`123","foo":"bar`, `"123\",\"foo\":\"bar"`}, // injection attempt stays one string
	}
	for _, tc := range cases {
		if got := jsonEsc(tc.in); got != tc.want {
			t.Errorf("jsonEsc(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestClientSearch(t *testing.T) {
	// (a) All four categories succeed concurrently — verify results are merged correctly
	t.Run("all success", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/search":
				_, _ = w.Write([]byte(`{"data": [{"id": "111", "title": "Track 1", "duration": "180", "explicit_lyrics": false, "artist": {"id": "222", "name": "Artist 1"}, "album": {"title": "Album 1", "cover_medium": "cover_url"}}]}`))
			case "/search/album":
				_, _ = w.Write([]byte(`{"data": [{"id": "333", "title": "Album 1", "artist": {"id": "222", "name": "Artist 1"}, "cover_medium": "cover_url"}]}`))
			case "/search/artist":
				_, _ = w.Write([]byte(`{"data": [{"id": "222", "name": "Artist 1", "picture_medium": "pic_url", "nb_fan": 1000}]}`))
			case "/search/playlist":
				_, _ = w.Write([]byte(`{"data": [{"id": "444", "title": "Playlist 1", "user": {"name": "Owner 1"}, "nb_tracks": 10, "picture_medium": "pic_url"}]}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()

		c := New("dummy_arl")
		c.restURLOverride = ts.URL

		res, err := c.Search("query")
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if res == nil {
			t.Fatal("expected non-nil results")
		}

		if len(res.Tracks) != 1 || res.Tracks[0].ID != "111" || res.Tracks[0].Name != "Track 1" {
			t.Errorf("unexpected tracks: %+v", res.Tracks)
		}
		if len(res.Albums) != 1 || res.Albums[0].ID != "333" || res.Albums[0].Name != "Album 1" {
			t.Errorf("unexpected albums: %+v", res.Albums)
		}
		if len(res.Artists) != 1 || res.Artists[0].ID != "222" || res.Artists[0].Name != "Artist 1" {
			t.Errorf("unexpected artists: %+v", res.Artists)
		}
		if len(res.Playlists) != 1 || res.Playlists[0].ID != "444" || res.Playlists[0].Name != "Playlist 1" {
			t.Errorf("unexpected playlists: %+v", res.Playlists)
		}
	})

	// (b) One category returns a 500/garbage JSON — verify the other three still come back and error is nil
	t.Run("one category fails", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/search":
				// Fail tracks with 500
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`internal server error`))
			case "/search/album":
				_, _ = w.Write([]byte(`{"data": [{"id": "333", "title": "Album 1", "artist": {"id": "222", "name": "Artist 1"}, "cover_medium": "cover_url"}]}`))
			case "/search/artist":
				// Garbage JSON
				_, _ = w.Write([]byte(`{invalid-json`))
			case "/search/playlist":
				_, _ = w.Write([]byte(`{"data": [{"id": "444", "title": "Playlist 1", "user": {"name": "Owner 1"}, "nb_tracks": 10, "picture_medium": "pic_url"}]}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()

		c := New("dummy_arl")
		c.restURLOverride = ts.URL

		res, err := c.Search("query")
		if err != nil {
			t.Fatalf("expected error to be nil for partial failure, got: %v", err)
		}
		if res == nil {
			t.Fatal("expected non-nil results")
		}

		// Tracks failed (500) and Artists failed (garbage JSON), so they should be empty
		if len(res.Tracks) != 0 {
			t.Errorf("expected 0 tracks, got: %+v", res.Tracks)
		}
		if len(res.Artists) != 0 {
			t.Errorf("expected 0 artists, got: %+v", res.Artists)
		}
		// Albums and playlists should succeed
		if len(res.Albums) != 1 || res.Albums[0].ID != "333" {
			t.Errorf("unexpected albums: %+v", res.Albums)
		}
		if len(res.Playlists) != 1 || res.Playlists[0].ID != "444" {
			t.Errorf("unexpected playlists: %+v", res.Playlists)
		}
	})

	// (c) All four fail — verify a non-nil error
	t.Run("all fail", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`internal server error`))
		}))
		defer ts.Close()

		c := New("dummy_arl")
		c.restURLOverride = ts.URL

		res, err := c.Search("query")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if res != nil {
			t.Errorf("expected nil results when all fail, got: %+v", res)
		}

		// Verify that all 4 errors are joined / present in the error message
		errMsg := err.Error()
		for _, cat := range []string{"tracks", "albums", "artists", "playlists"} {
			if !strings.Contains(errMsg, cat) {
				t.Errorf("expected error message to contain %q, but got: %q", cat, errMsg)
			}
		}
	})
}
