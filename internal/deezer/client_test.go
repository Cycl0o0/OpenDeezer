package deezer

import "testing"

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
