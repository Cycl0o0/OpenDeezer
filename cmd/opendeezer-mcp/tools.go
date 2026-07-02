package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

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

// Optional-argument variants: present reports whether the key was given at
// all, so absent keys are skipped while wrong-typed ones still error.
func argStringOpt(args map[string]any, key string) (val string, present bool, err error) {
	v, ok := args[key]
	if !ok {
		return "", false, nil
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return "", false, fmt.Errorf("argument %q must be a non-empty string", key)
	}
	return s, true, nil
}

func argFloatOpt(args map[string]any, key string) (val float64, present bool, err error) {
	v, ok := args[key]
	if !ok {
		return 0, false, nil
	}
	f, ok := v.(float64) // JSON numbers decode to float64
	if !ok {
		return 0, false, fmt.Errorf("argument %q must be a number", key)
	}
	return f, true, nil
}

func argBoolOpt(args map[string]any, key string) (val bool, present bool, err error) {
	v, ok := args[key]
	if !ok {
		return false, false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, false, fmt.Errorf("argument %q must be a boolean", key)
	}
	return b, true, nil
}

// bit renders a bool as the "0"/"1" the control API's query params expect.
func bit(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// stateText renders a (State, error) result as pretty JSON text.
func stateText(st control.State, err error) (string, error) {
	if err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	return string(b), nil
}

// eqText renders an (EQState, error) result as pretty JSON text.
func eqText(st control.EQState, err error) (string, error) {
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
		{"get_eq", "Get the 10-band equalizer state: enabled, mono downmix, preamp, per-band gains (dB), band center frequencies and the available presets.", noArgs,
			func(map[string]any) (string, error) { return eqText(c.EQ()) }},
		{"set_eq", "Change the equalizer. Every argument is optional but at least one must be given; band and gain_db go together. Editing a band switches the preset to \"custom\".",
			objSchema(map[string]any{
				"enabled":   map[string]any{"type": "boolean", "description": "Turn the equalizer on or off"},
				"mono":      map[string]any{"type": "boolean", "description": "Turn mono downmix on or off (independent of the EQ)"},
				"preset":    map[string]any{"type": "string", "description": "Preset name: flat, bass-boost, bass-reducer, treble-boost, vocal, rock, pop, jazz, classical or electronic"},
				"band":      map[string]any{"type": "integer", "minimum": 0, "maximum": 9, "description": "Band index 0..9 (31.5 Hz .. 16 kHz); requires gain_db"},
				"gain_db":   map[string]any{"type": "number", "minimum": -12, "maximum": 12, "description": "Gain for band, in dB"},
				"preamp_db": map[string]any{"type": "number", "minimum": -12, "maximum": 12, "description": "Output preamp in dB"},
			}),
			func(args map[string]any) (string, error) {
				q := url.Values{}
				if v, ok, err := argBoolOpt(args, "enabled"); err != nil {
					return "", err
				} else if ok {
					q.Set("on", bit(v))
				}
				if v, ok, err := argBoolOpt(args, "mono"); err != nil {
					return "", err
				} else if ok {
					q.Set("mono", bit(v))
				}
				if v, ok, err := argStringOpt(args, "preset"); err != nil {
					return "", err
				} else if ok {
					q.Set("preset", v)
				}
				band, hasBand, err := argFloatOpt(args, "band")
				if err != nil {
					return "", err
				}
				gain, hasGain, err := argFloatOpt(args, "gain_db")
				if err != nil {
					return "", err
				}
				if hasBand != hasGain {
					return "", fmt.Errorf("band and gain_db must be given together")
				}
				if hasBand {
					q.Set("band", strconv.Itoa(int(band)))
					q.Set("db", strconv.FormatFloat(gain, 'f', -1, 64))
				}
				if v, ok, err := argFloatOpt(args, "preamp_db"); err != nil {
					return "", err
				} else if ok {
					q.Set("preamp", strconv.FormatFloat(v, 'f', -1, 64))
				}
				if len(q) == 0 {
					return "", fmt.Errorf("give at least one of enabled, mono, preset, band+gain_db, preamp_db")
				}
				return eqText(c.SetEQ(q))
			}},
	}
}
