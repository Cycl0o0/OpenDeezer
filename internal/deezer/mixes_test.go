package deezer

import "testing"

func TestParseGwTrackListObjectEnvelope(t *testing.T) {
	body := []byte(`{"error":{},"results":{"data":[
		{"SNG_ID":"3135556","SNG_TITLE":"Harder, Better, Faster, Stronger",
		 "DURATION":"224","ART_ID":"27","ART_NAME":"Daft Punk",
		 "ALB_TITLE":"Discovery","ALB_PICTURE":"abc123","EXPLICIT_LYRICS":"0"},
		{"SNG_ID":916424,"SNG_TITLE":"One More Time","DURATION":320,
		 "ART_ID":27,"ART_NAME":"Daft Punk","ALB_TITLE":"Discovery",
		 "ALB_PICTURE":"abc123","EXPLICIT_LYRICS":"1"}
	]}}`)
	got := parseGwTrackList(body)
	if len(got) != 2 {
		t.Fatalf("got %d tracks, want 2", len(got))
	}
	if got[0].ID != "3135556" || got[0].Name != "Harder, Better, Faster, Stronger" {
		t.Errorf("track 0 = %q/%q", got[0].ID, got[0].Name)
	}
	if got[0].DurationMS != 224000 {
		t.Errorf("track 0 duration = %d, want 224000", got[0].DurationMS)
	}
	if got[0].Explicit {
		t.Error("track 0 should not be explicit")
	}
	// Numeric ids/durations (gw mixes string and number types) must decode too.
	if got[1].ID != "916424" || got[1].DurationMS != 320000 || !got[1].Explicit {
		t.Errorf("track 1 = %+v", got[1])
	}
	if len(got[1].Artists) != 1 || got[1].Artists[0].Name != "Daft Punk" {
		t.Errorf("track 1 artists = %+v", got[1].Artists)
	}
}

func TestParseGwTrackListBareArrayEnvelope(t *testing.T) {
	body := []byte(`{"error":[],"results":[
		{"SNG_ID":"1","SNG_TITLE":"A","DURATION":"10","ART_ID":"2","ART_NAME":"X"}
	]}`)
	got := parseGwTrackList(body)
	if len(got) != 1 || got[0].ID != "1" || got[0].Name != "A" {
		t.Fatalf("got %+v, want one track id=1", got)
	}
}

func TestParseGwTrackListEmpty(t *testing.T) {
	for _, body := range []string{
		`{"error":{},"results":{"data":[]}}`,
		`{"error":{},"results":[]}`,
		`{"error":{},"results":null}`,
		`not json`,
	} {
		if got := parseGwTrackList([]byte(body)); got != nil {
			t.Errorf("parseGwTrackList(%q) = %+v, want nil", body, got)
		}
	}
}

func TestMixesRequireLogin(t *testing.T) {
	c := New("dummy")
	if _, err := c.TrackMix("3135556"); err == nil {
		t.Error("TrackMix on a logged-out client should fail")
	}
	if _, err := c.ArtistMix("27"); err == nil {
		t.Error("ArtistMix on a logged-out client should fail")
	}
}
