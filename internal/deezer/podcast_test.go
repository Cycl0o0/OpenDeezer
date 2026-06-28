package deezer

import (
	"encoding/json"
	"testing"
)

func raw(v string) json.RawMessage { b, _ := json.Marshal(v); return b }

func TestFindStreamURL(t *testing.T) {
	cases := []struct {
		name    string
		in      map[string]json.RawMessage
		wantURL string
	}{
		{
			name: "prefers stream field over generic url and skips artwork",
			in: map[string]json.RawMessage{
				"EPISODE_PICTURE":           raw("https://cdn/img.jpg"),
				"SOME_URL":                  raw("https://cdn/page"),
				"EPISODE_DIRECT_STREAM_URL": raw("https://cdn/audio.mp3"),
			},
			wantURL: "https://cdn/audio.mp3",
		},
		{
			name: "ignores image urls even with url-ish keys",
			in: map[string]json.RawMessage{
				"COVER_URL": raw("https://cdn/cover.png"),
				"PICTURE":   raw("https://cdn/p.jpeg"),
			},
			wantURL: "",
		},
		{
			name: "falls back to any non-image http value",
			in: map[string]json.RawMessage{
				"TITLE": raw("Episode 1"),
				"BLOB":  raw("https://cdn/stream/file"),
			},
			wantURL: "https://cdn/stream/file",
		},
		{
			name:    "empty when nothing matches",
			in:      map[string]json.RawMessage{"TITLE": raw("x"), "DUR": raw("123")},
			wantURL: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := findStreamURL(c.in)
			if got != c.wantURL {
				t.Fatalf("findStreamURL = %q, want %q", got, c.wantURL)
			}
		})
	}
}
