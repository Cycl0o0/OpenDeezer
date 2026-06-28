package main

import (
	"encoding/json"
	"fmt"

	"github.com/Cycl0o0/OpenDeezer/internal/control"
)

// tool is one MCP tool: a name + JSON-Schema for its arguments + a handler.
type tool struct {
	name        string
	description string
	schema      map[string]any
	run         func(args map[string]any) (string, error)
}

func toolSpecs(tools []tool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"name":        t.name,
			"description": t.description,
			"inputSchema": t.schema,
		})
	}
	return out
}

// schema helpers.
func objSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if props == nil {
		s["properties"] = map[string]any{}
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func argString(args map[string]any, key string) (string, error) {
	v, ok := args[key]
	if !ok {
		return "", fmt.Errorf("missing argument %q", key)
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return "", fmt.Errorf("argument %q must be a non-empty string", key)
	}
	return s, nil
}

func argFloat(args map[string]any, key string) (float64, error) {
	v, ok := args[key]
	if !ok {
		return 0, fmt.Errorf("missing argument %q", key)
	}
	f, ok := v.(float64) // JSON numbers decode to float64
	if !ok {
		return 0, fmt.Errorf("argument %q must be a number", key)
	}
	return f, nil
}

// stateText renders a (State, error) result as pretty JSON text.
func stateText(st control.State, err error) (string, error) {
	if err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	return string(b), nil
}

// buildTools wires the MCP tools to the control client.
func buildTools(c *control.Client) []tool {
	noArgs := objSchema(nil)
	return []tool{
		{"get_status", "Get the current playback status: state, current track, position, volume, repeat, shuffle and the queue.", noArgs,
			func(map[string]any) (string, error) { return stateText(c.Status()) }},
		{"play_pause", "Toggle play/pause.", noArgs,
			func(map[string]any) (string, error) { return stateText(c.PlayPause()) }},
		{"next", "Skip to the next track.", noArgs,
			func(map[string]any) (string, error) { return stateText(c.Next()) }},
		{"prev", "Go to the previous track.", noArgs,
			func(map[string]any) (string, error) { return stateText(c.Prev()) }},
		{"stop", "Stop playback.", noArgs,
			func(map[string]any) (string, error) { return stateText(c.Stop()) }},
		{"restart", "Restart the current track from the beginning.", noArgs,
			func(map[string]any) (string, error) { return stateText(c.Restart()) }},
		{"cycle_repeat", "Cycle the repeat mode (off -> all -> one).", noArgs,
			func(map[string]any) (string, error) { return stateText(c.CycleRepeat()) }},
		{"toggle_shuffle", "Toggle shuffle on/off.", noArgs,
			func(map[string]any) (string, error) { return stateText(c.ToggleShuffle()) }},
		{"set_volume", "Set the playback volume.",
			objSchema(map[string]any{"volume": map[string]any{"type": "number", "minimum": 0, "maximum": 1, "description": "Volume from 0.0 to 1.0"}}, "volume"),
			func(args map[string]any) (string, error) {
				v, err := argFloat(args, "volume")
				if err != nil {
					return "", err
				}
				return stateText(c.SetVolume(v))
			}},
		{"seek", "Seek to an absolute position in the current track (milliseconds).",
			objSchema(map[string]any{"positionMs": map[string]any{"type": "integer", "minimum": 0, "description": "Absolute position in milliseconds"}}, "positionMs"),
			func(args map[string]any) (string, error) {
				ms, err := argFloat(args, "positionMs")
				if err != nil {
					return "", err
				}
				return stateText(c.Seek(int64(ms)))
			}},
		{"play_track", "Play a specific Deezer track by id (replaces the queue).",
			objSchema(map[string]any{"id": map[string]any{"type": "string", "description": "Deezer track id"}}, "id"),
			func(args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				return stateText(c.PlayTrack(id))
			}},
		{"play_playlist", "Play a Deezer playlist by id from the top.",
			objSchema(map[string]any{"id": map[string]any{"type": "string", "description": "Deezer playlist id"}}, "id"),
			func(args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				return stateText(c.PlayPlaylist(id))
			}},
		{"search", "Search Deezer for tracks, albums, artists and playlists.",
			objSchema(map[string]any{"query": map[string]any{"type": "string", "description": "Search text"}}, "query"),
			func(args map[string]any) (string, error) {
				q, err := argString(args, "query")
				if err != nil {
					return "", err
				}
				raw, err := c.Search(q)
				return string(raw), err
			}},
		{"list_playlists", "List the logged-in user's playlists.", noArgs,
			func(map[string]any) (string, error) {
				raw, err := c.Playlists()
				return string(raw), err
			}},
	}
}
