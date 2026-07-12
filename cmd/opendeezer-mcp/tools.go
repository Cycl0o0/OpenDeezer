package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/control"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/discovery"
)

// tool is one MCP tool: a name + JSON-Schema for its arguments + a handler.
type tool struct {
	name        string
	description string
	schema      map[string]any
	run         func(args map[string]any) (string, error)
}

// readOnlyTools marks the tools that never mutate playback or device state.
// They are surfaced with the readOnlyHint annotation when the negotiated
// protocol revision supports tool annotations (2025-03-26+).
var readOnlyTools = map[string]bool{
	"get_status":     true,
	"search":         true,
	"list_playlists": true,
	"history_recent": true,
	"get_eq":         true,
	"whoami":         true,
	"list_devices":   true,
}

// target holds the control client the tools currently drive. It starts on the
// local client from the environment; the select_device tool re-points it at
// another discovered host at runtime (list_devices → select_device → any tool).
type target struct {
	mu   sync.Mutex
	c    *control.Client
	base string // current server URL (for reporting)

	// Defaults from the environment, restored by select_device without a url.
	defBase, defToken, defAccount string
}

func newTarget(base, token, account string) *target {
	return &target{
		c: control.NewClient(base, token, account), base: base,
		defBase: base, defToken: token, defAccount: account,
	}
}

// client returns the control client for the currently selected device.
func (t *target) client() *control.Client {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.c
}

// current returns the currently selected server URL.
func (t *target) current() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.base
}

// swap installs a new control client (already probed by the caller).
func (t *target) swap(c *control.Client, base string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.c, t.base = c, base
}

func toolSpecs(tools []tool, withAnnotations bool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		spec := map[string]any{
			"name":        t.name,
			"description": t.description,
			"inputSchema": t.schema,
		}
		if withAnnotations && readOnlyTools[t.name] {
			spec["annotations"] = map[string]any{"readOnlyHint": true}
		}
		out = append(out, spec)
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

// argIndex reads a required queue index: a non-negative integer. JSON numbers
// arrive as float64, so it also rejects fractional values.
func argIndex(args map[string]any, key string) (int, error) {
	f, err := argFloat(args, key)
	if err != nil {
		return 0, err
	}
	if f < 0 || f > math.MaxInt32 || f != math.Trunc(f) {
		return 0, fmt.Errorf("argument %q must be a non-negative integer", key)
	}
	return int(f), nil
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

// buildTools wires the MCP tools to the target's control client. Each handler
// fetches the client at call time so select_device retargets every tool.
func buildTools(tgt *target) []tool {
	noArgs := objSchema(nil)
	return []tool{
		{"get_status", "Get the current playback status: state, current track, position, volume, repeat, shuffle and the queue.", noArgs,
			func(map[string]any) (string, error) { return stateText(tgt.client().Status()) }},
		{"play_pause", "Toggle play/pause.", noArgs,
			func(map[string]any) (string, error) { return stateText(tgt.client().PlayPause()) }},
		{"next", "Skip to the next track.", noArgs,
			func(map[string]any) (string, error) { return stateText(tgt.client().Next()) }},
		{"prev", "Go to the previous track.", noArgs,
			func(map[string]any) (string, error) { return stateText(tgt.client().Prev()) }},
		{"stop", "Stop playback.", noArgs,
			func(map[string]any) (string, error) { return stateText(tgt.client().Stop()) }},
		{"restart", "Restart the current track from the beginning.", noArgs,
			func(map[string]any) (string, error) { return stateText(tgt.client().Restart()) }},
		{"cycle_repeat", "Cycle the repeat mode (off -> all -> one).", noArgs,
			func(map[string]any) (string, error) { return stateText(tgt.client().CycleRepeat()) }},
		{"toggle_shuffle", "Toggle shuffle on/off.", noArgs,
			func(map[string]any) (string, error) { return stateText(tgt.client().ToggleShuffle()) }},
		{"set_repeat", "Set the repeat mode to an exact value (off, all or one).",
			objSchema(map[string]any{"mode": map[string]any{"type": "string", "enum": []string{"off", "all", "one"}, "description": "Repeat mode"}}, "mode"),
			func(args map[string]any) (string, error) {
				mode, err := argString(args, "mode")
				if err != nil {
					return "", err
				}
				if mode != "off" && mode != "all" && mode != "one" {
					return "", fmt.Errorf("mode must be off, all or one")
				}
				return stateText(tgt.client().SetRepeat(mode))
			}},
		{"set_shuffle", "Set shuffle to an exact value (on or off).",
			objSchema(map[string]any{"on": map[string]any{"type": "boolean", "description": "true = shuffle on, false = shuffle off"}}, "on"),
			func(args map[string]any) (string, error) {
				on, ok, err := argBoolOpt(args, "on")
				if err != nil {
					return "", err
				}
				if !ok {
					return "", fmt.Errorf("missing argument %q", "on")
				}
				return stateText(tgt.client().SetShuffle(on))
			}},
		{"set_volume", "Set the playback volume.",
			objSchema(map[string]any{"volume": map[string]any{"type": "number", "minimum": 0, "maximum": 1, "description": "Volume from 0.0 to 1.0"}}, "volume"),
			func(args map[string]any) (string, error) {
				v, err := argFloat(args, "volume")
				if err != nil {
					return "", err
				}
				return stateText(tgt.client().SetVolume(v))
			}},
		{"seek", "Seek to an absolute position in the current track (milliseconds).",
			objSchema(map[string]any{"positionMs": map[string]any{"type": "integer", "minimum": 0, "description": "Absolute position in milliseconds"}}, "positionMs"),
			func(args map[string]any) (string, error) {
				ms, err := argFloat(args, "positionMs")
				if err != nil {
					return "", err
				}
				return stateText(tgt.client().Seek(int64(ms)))
			}},
		{"play_track", "Play a specific Deezer track by id (replaces the queue).",
			objSchema(map[string]any{"id": map[string]any{"type": "string", "description": "Deezer track id"}}, "id"),
			func(args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				return stateText(tgt.client().PlayTrack(id))
			}},
		{"play_playlist", "Play a Deezer playlist by id from the top.",
			objSchema(map[string]any{"id": map[string]any{"type": "string", "description": "Deezer playlist id"}}, "id"),
			func(args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				return stateText(tgt.client().PlayPlaylist(id))
			}},
		{"play_album", "Play a Deezer album by id from its first track (replaces the queue).",
			objSchema(map[string]any{"id": map[string]any{"type": "string", "description": "Deezer album id"}}, "id"),
			func(args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				return stateText(tgt.client().PlayAlbum(id))
			}},
		{"play_mix_track", "Play a Deezer mix (radio) seeded by a track id.",
			objSchema(map[string]any{"id": map[string]any{"type": "string", "description": "Deezer track id to seed the mix"}}, "id"),
			func(args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				return stateText(tgt.client().PlayMixTrack(id))
			}},
		{"play_mix_artist", "Play a Deezer mix (radio) seeded by an artist id.",
			objSchema(map[string]any{"id": map[string]any{"type": "string", "description": "Deezer artist id to seed the mix"}}, "id"),
			func(args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				return stateText(tgt.client().PlayMixArtist(id))
			}},
		{"queue_add", "Add a track to the queue: append it at the end, or insert it right after the current track (next).",
			objSchema(map[string]any{
				"id":   map[string]any{"type": "string", "description": "Deezer track id"},
				"next": map[string]any{"type": "boolean", "description": "true = play next (insert after the current track); false or omitted = append at the end"},
			}, "id"),
			func(args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				next, _, err := argBoolOpt(args, "next")
				if err != nil {
					return "", err
				}
				return stateText(tgt.client().QueueAdd(id, next))
			}},
		{"queue_jump", "Jump playback to a queue entry by index (0-based, as listed by get_status).",
			objSchema(map[string]any{"index": map[string]any{"type": "integer", "minimum": 0, "description": "Queue index, 0-based"}}, "index"),
			func(args map[string]any) (string, error) {
				i, err := argIndex(args, "index")
				if err != nil {
					return "", err
				}
				return stateText(tgt.client().QueueJump(i))
			}},
		{"queue_remove", "Remove a queue entry by index (0-based, as listed by get_status).",
			objSchema(map[string]any{"index": map[string]any{"type": "integer", "minimum": 0, "description": "Queue index, 0-based"}}, "index"),
			func(args map[string]any) (string, error) {
				i, err := argIndex(args, "index")
				if err != nil {
					return "", err
				}
				return stateText(tgt.client().QueueRemove(i))
			}},
		{"queue_move", "Move a queue entry from one index to another (both 0-based).",
			objSchema(map[string]any{
				"from": map[string]any{"type": "integer", "minimum": 0, "description": "Current queue index of the entry, 0-based"},
				"to":   map[string]any{"type": "integer", "minimum": 0, "description": "Destination queue index, 0-based"},
			}, "from", "to"),
			func(args map[string]any) (string, error) {
				from, err := argIndex(args, "from")
				if err != nil {
					return "", err
				}
				to, err := argIndex(args, "to")
				if err != nil {
					return "", err
				}
				return stateText(tgt.client().QueueMove(from, to))
			}},
		{"history_recent", "Get the recently played tracks, most recent first.",
			objSchema(map[string]any{"n": map[string]any{"type": "integer", "minimum": 1, "maximum": 500, "description": "Number of entries to return (default 50)"}}),
			func(args map[string]any) (string, error) {
				n := 50
				if f, ok, err := argFloatOpt(args, "n"); err != nil {
					return "", err
				} else if ok {
					if f < 1 || f > 500 || f != math.Trunc(f) {
						return "", fmt.Errorf("n must be an integer between 1 and 500")
					}
					n = int(f)
				}
				raw, err := tgt.client().HistoryRecent(n)
				return string(raw), err
			}},
		{"search", "Search Deezer for tracks, albums, artists and playlists.",
			objSchema(map[string]any{"query": map[string]any{"type": "string", "description": "Search text"}}, "query"),
			func(args map[string]any) (string, error) {
				q, err := argString(args, "query")
				if err != nil {
					return "", err
				}
				raw, err := tgt.client().Search(q)
				return string(raw), err
			}},
		{"list_playlists", "List the logged-in user's playlists.", noArgs,
			func(map[string]any) (string, error) {
				raw, err := tgt.client().Playlists()
				return string(raw), err
			}},
		{"set_sleep_timer", "Arm the sleep timer: playback fades out and pauses after the given minutes, or when the current track ends (end_of_track).",
			objSchema(map[string]any{
				"minutes":      map[string]any{"type": "integer", "minimum": 1, "description": "Pause after this many minutes (ignored when end_of_track is true)"},
				"end_of_track": map[string]any{"type": "boolean", "description": "Pause when the current track finishes instead of after a duration"},
			}),
			func(args map[string]any) (string, error) {
				eot, _, err := argBoolOpt(args, "end_of_track")
				if err != nil {
					return "", err
				}
				minutes, hasMin, err := argFloatOpt(args, "minutes")
				if err != nil {
					return "", err
				}
				if !eot && (!hasMin || minutes < 1) {
					return "", fmt.Errorf("give minutes (>= 1) or end_of_track: true")
				}
				return stateText(tgt.client().SetSleepTimer(int(minutes), eot))
			}},
		{"cancel_sleep_timer", "Disarm the sleep timer and restore full volume.", noArgs,
			func(map[string]any) (string, error) { return stateText(tgt.client().CancelSleepTimer()) }},
		{"whoami", "Get the controlled device's identity: account name, plan, auth mode, client id, device label and version. Also reports which server URL is currently targeted.", noArgs,
			func(map[string]any) (string, error) {
				w, err := tgt.client().Whoami()
				if err != nil {
					return "", err
				}
				b, _ := json.MarshalIndent(map[string]any{"target": tgt.current(), "whoami": w}, "", "  ")
				return string(b), nil
			}},
		{"list_devices", "Discover OpenDeezer devices on the local network (2-second probe). Pass a result's addr to select_device to control it.", noArgs,
			func(map[string]any) (string, error) {
				devices, err := discovery.Discover(2*time.Second, 0)
				if err != nil {
					return "", err
				}
				b, _ := json.MarshalIndent(map[string]any{"count": len(devices), "devices": devices}, "", "  ")
				return string(b), nil
			}},
		{"select_device", "Point all other tools at another OpenDeezer device. Give the device url (host:port from list_devices, or a full http URL) plus credentials for its auth mode; the local account id from the environment is reused by default (same-account auth). Omit url to go back to the default local device.",
			objSchema(map[string]any{
				"url":     map[string]any{"type": "string", "description": "Device control address: host:port or http://host:port. Omit to reselect the default device from the environment"},
				"token":   map[string]any{"type": "string", "description": "Bearer token when the device uses token auth"},
				"account": map[string]any{"type": "string", "description": "Deezer user id when the device uses same-account auth (defaults to $OPENDEEZER_CONTROL_ACCOUNT)"},
			}),
			func(args map[string]any) (string, error) {
				urlArg, hasURL, err := argStringOpt(args, "url")
				if err != nil {
					return "", err
				}
				token, hasToken, err := argStringOpt(args, "token")
				if err != nil {
					return "", err
				}
				account, hasAccount, err := argStringOpt(args, "account")
				if err != nil {
					return "", err
				}
				base, tok, acct := tgt.defBase, tgt.defToken, tgt.defAccount
				if hasURL {
					base = urlArg
					if !strings.Contains(base, "://") {
						base = "http://" + base
					}
					// Never send the local token to a different device; the
					// account id IS shared across devices (same-account auth).
					tok = ""
				}
				if hasToken {
					tok = token
				}
				if hasAccount {
					acct = account
				}
				// Probe before switching so a typo'd address doesn't strand
				// every other tool on an unreachable target.
				next := control.NewClient(base, tok, acct)
				w, err := next.Whoami()
				if err != nil {
					return "", fmt.Errorf("device at %s not reachable: %w", base, err)
				}
				tgt.swap(next, base)
				b, _ := json.MarshalIndent(map[string]any{"selected": base, "whoami": w}, "", "  ")
				return string(b), nil
			}},
		{"get_eq", "Get the 10-band equalizer state: enabled, mono downmix, preamp, per-band gains (dB), band center frequencies and the available presets.", noArgs,
			func(map[string]any) (string, error) { return eqText(tgt.client().EQ()) }},
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
				return eqText(tgt.client().SetEQ(q))
			}},
	}
}
