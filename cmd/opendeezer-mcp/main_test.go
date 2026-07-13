package main

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/control"
)

// rec records the commands the fake control server received.
type rec struct {
	played      int
	repeat      string
	shuffleOn   bool
	shuffleSet  bool
	sleepMin    int
	sleepEOT    bool
	sleepCancel bool
	queueAddID  string
	queueAddNxt bool
	queueJumped int
	albumID     string
	historyNs   []int
}

// startControl spins up a real control server and returns its base URL + a
// recorder of the dispatched commands.
func startControl(t *testing.T) (string, *rec) {
	t.Helper()
	r := &rec{}
	srv := control.New(
		control.Config{Addr: "127.0.0.1:0"},
		func() control.State {
			return control.State{State: "playing", Volume: 0.4,
				Track: &control.Track{ID: "1", Title: "Song", Artist: "Artist"}}
		},
		func() control.Account { return control.Account{Name: "me"} },
		control.Commands{
			PlayPause:        func() { r.played++ },
			SetRepeat:        func(mode string) { r.repeat = mode },
			SetShuffle:       func(on bool) { r.shuffleOn, r.shuffleSet = on, true },
			SetSleepTimer:    func(min int, eot bool) { r.sleepMin, r.sleepEOT = min, eot },
			CancelSleepTimer: func() { r.sleepCancel = true },
			QueueAdd:         func(id string, next bool) error { r.queueAddID, r.queueAddNxt = id, next; return nil },
			QueueJump:        func(i int) error { r.queueJumped = i; return nil },
			PlayAlbum:        func(id string) error { r.albumID = id; return nil },
			HistoryRecent: func(n int) (json.RawMessage, error) {
				r.historyNs = append(r.historyNs, n)
				return json.RawMessage(`[{"id":"1","title":"Past Song"}]`), nil
			},
		},
		nil,
	)
	// Fake EQ bridge: echoes mutations back through State like the real engine.
	eq := control.EQState{Preset: "flat", GainsDB: make([]float64, 10),
		Bands:   []float64{31.5, 63, 125, 250, 500, 1000, 2000, 4000, 8000, 16000},
		Presets: []string{"flat", "rock"}}
	srv.SetEQ(&control.EQ{
		State:      func() control.EQState { return eq },
		SetEnabled: func(on bool) { eq.Enabled = on },
		SetMono:    func(on bool) { eq.Mono = on },
		SetPreamp:  func(db float64) { eq.PreampDB = db },
		SetPreset:  func(name string) error { eq.Preset = name; return nil },
		SetBand:    func(band int, db float64) error { eq.GainsDB[band] = db; eq.Preset = "custom"; return nil },
	})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return "http://" + srv.Addr(), r
}

// run feeds newline-delimited JSON-RPC requests through the MCP server and
// returns the decoded responses.
func run(t *testing.T, base string, lines ...string) []rpcResp {
	t.Helper()
	tgt := newTarget(base, "", "")
	s := &server{target: tgt, tools: buildTools(tgt), proto: protocolVersion}
	var out strings.Builder
	s.serve(strings.NewReader(strings.Join(lines, "\n")+"\n"), &out)

	var resps []rpcResp
	sc := bufio.NewScanner(strings.NewReader(out.String()))
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var r rpcResp
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("bad response %q: %v", sc.Text(), err)
		}
		resps = append(resps, r)
	}
	return resps
}

func TestInitializeAndToolsList(t *testing.T) {
	base, _ := startControl(t)
	resps := run(t, base,
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`, // no response
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)
	if len(resps) != 2 {
		t.Fatalf("got %d responses, want 2 (notification must not reply)", len(resps))
	}
	res := resps[0].Result.(map[string]any)
	if res["protocolVersion"] != protocolVersion {
		t.Fatalf("protocolVersion = %v", res["protocolVersion"])
	}
	tools := resps[1].Result.(map[string]any)["tools"].([]any)
	if len(tools) < 10 {
		t.Fatalf("expected >=10 tools, got %d", len(tools))
	}
}

func TestToolCallGetStatusAndPlayPause(t *testing.T) {
	base, r := startControl(t)
	resps := run(t, base,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_status","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"play_pause","arguments":{}}}`,
	)
	if len(resps) != 2 {
		t.Fatalf("got %d responses, want 2", len(resps))
	}
	text := contentText(t, resps[0])
	if !strings.Contains(text, `"state": "playing"`) || !strings.Contains(text, `"title": "Song"`) {
		t.Fatalf("get_status text = %s", text)
	}
	if r.played != 1 {
		t.Fatalf("play_pause not dispatched (played=%d)", r.played)
	}
}

func TestToolCallValidationError(t *testing.T) {
	base, _ := startControl(t)
	resps := run(t, base,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"set_volume","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"bogus","arguments":{}}}`,
	)
	for _, r := range resps {
		res := r.Result.(map[string]any)
		if res["isError"] != true {
			t.Fatalf("expected isError for %v", res)
		}
	}
}

func TestToolCallEQ(t *testing.T) {
	base, _ := startControl(t)
	resps := run(t, base,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"set_eq","arguments":{"enabled":true,"band":3,"gain_db":4.5}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_eq","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"set_eq","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"set_eq","arguments":{"band":3}}}`,
	)
	if len(resps) != 4 {
		t.Fatalf("got %d responses, want 4", len(resps))
	}
	text := contentText(t, resps[1])
	if !strings.Contains(text, `"enabled": true`) || !strings.Contains(text, `"preset": "custom"`) || !strings.Contains(text, "4.5") {
		t.Fatalf("get_eq text = %s", text)
	}
	for _, r := range resps[2:] { // no args / band without gain_db must error
		if r.Result.(map[string]any)["isError"] != true {
			t.Fatalf("expected isError, got %v", r.Result)
		}
	}
}

func TestToolCallSetModesAndSleep(t *testing.T) {
	base, r := startControl(t)
	resps := run(t, base,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"set_repeat","arguments":{"mode":"one"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"set_shuffle","arguments":{"on":true}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"set_sleep_timer","arguments":{"minutes":30}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"cancel_sleep_timer","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"set_repeat","arguments":{"mode":"loud"}}}`, // bad mode
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"set_shuffle","arguments":{}}}`,             // missing on
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"set_sleep_timer","arguments":{}}}`,         // no minutes, no eot
	)
	if len(resps) != 7 {
		t.Fatalf("got %d responses, want 7", len(resps))
	}
	if r.repeat != "one" {
		t.Errorf("set_repeat not dispatched (repeat=%q)", r.repeat)
	}
	if !r.shuffleSet || !r.shuffleOn {
		t.Errorf("set_shuffle not dispatched (set=%v on=%v)", r.shuffleSet, r.shuffleOn)
	}
	if r.sleepMin != 30 || r.sleepEOT {
		t.Errorf("set_sleep_timer got min=%d eot=%v", r.sleepMin, r.sleepEOT)
	}
	if !r.sleepCancel {
		t.Error("cancel_sleep_timer not dispatched")
	}
	for _, resp := range resps[4:] {
		if resp.Result.(map[string]any)["isError"] != true {
			t.Errorf("expected isError, got %v", resp.Result)
		}
	}
}

func TestToolCallSleepEndOfTrack(t *testing.T) {
	base, r := startControl(t)
	run(t, base,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"set_sleep_timer","arguments":{"end_of_track":true}}}`,
	)
	if !r.sleepEOT {
		t.Fatalf("end_of_track sleep timer not dispatched (min=%d eot=%v)", r.sleepMin, r.sleepEOT)
	}
}

func TestToolCallQueuePlayHistory(t *testing.T) {
	base, r := startControl(t)
	resps := run(t, base,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"queue_add","arguments":{"id":"3135556","next":true}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"queue_jump","arguments":{"index":2}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"play_album","arguments":{"id":"302127"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"history_recent","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"history_recent","arguments":{"n":5}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"queue_add","arguments":{}}}`,                      // missing id
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"queue_add","arguments":{"id":"1","next":"yes"}}}`, // next not a boolean
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"play_album","arguments":{}}}`,                     // missing id
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"history_recent","arguments":{"n":0}}}`,            // out of range
		`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"queue_jump","arguments":{"index":-1}}}`,          // negative index
		`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"queue_jump","arguments":{"index":1.5}}}`,         // fractional index
		`{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"queue_move","arguments":{"from":0}}}`,            // missing to
	)
	if len(resps) != 12 {
		t.Fatalf("got %d responses, want 12", len(resps))
	}
	if r.queueAddID != "3135556" || !r.queueAddNxt {
		t.Errorf("queue_add not dispatched (id=%q next=%v)", r.queueAddID, r.queueAddNxt)
	}
	if r.queueJumped != 2 {
		t.Errorf("queue_jump not dispatched (index=%d)", r.queueJumped)
	}
	if r.albumID != "302127" {
		t.Errorf("play_album not dispatched (id=%q)", r.albumID)
	}
	// history_recent without n must default to 50; with n it passes it through.
	if len(r.historyNs) != 2 || r.historyNs[0] != 50 || r.historyNs[1] != 5 {
		t.Errorf("history_recent dispatched with n=%v, want [50 5]", r.historyNs)
	}
	if text := contentText(t, resps[3]); !strings.Contains(text, "Past Song") {
		t.Errorf("history_recent text = %s", text)
	}
	for _, resp := range resps[5:] {
		if resp.Result.(map[string]any)["isError"] != true {
			t.Errorf("expected isError, got %v", resp.Result)
		}
	}
}

func TestToolCallWhoami(t *testing.T) {
	base, _ := startControl(t)
	resps := run(t, base,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"whoami","arguments":{}}}`,
	)
	text := contentText(t, resps[0])
	if !strings.Contains(text, `"name": "me"`) || !strings.Contains(text, base) {
		t.Fatalf("whoami text = %s", text)
	}
}

// TestProtocolNegotiation verifies that a known-compatible newer protocol
// version is echoed back (unlocking tool annotations) while an unknown one
// falls back to the pinned baseline (no annotations emitted).
func TestProtocolNegotiation(t *testing.T) {
	base, _ := startControl(t)

	// Known newer revision → echoed, annotations present on read-only tools.
	resps := run(t, base,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)
	if v := resps[0].Result.(map[string]any)["protocolVersion"]; v != "2025-03-26" {
		t.Fatalf("protocolVersion = %v, want echoed 2025-03-26", v)
	}
	if !toolHasReadOnlyHint(t, resps[1], "get_status") {
		t.Error("get_status should carry readOnlyHint on 2025-03-26")
	}
	if toolHasReadOnlyHint(t, resps[1], "play_pause") {
		t.Error("play_pause must not carry readOnlyHint")
	}

	// Unknown revision → pinned baseline, no annotations field at all.
	resps = run(t, base,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2099-01-01"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)
	if v := resps[0].Result.(map[string]any)["protocolVersion"]; v != protocolVersion {
		t.Fatalf("protocolVersion = %v, want pinned %s", v, protocolVersion)
	}
	if toolHasReadOnlyHint(t, resps[1], "get_status") {
		t.Error("annotations must not be emitted on the 2024-11-05 baseline")
	}
}

func toolHasReadOnlyHint(t *testing.T, r rpcResp, name string) bool {
	t.Helper()
	for _, raw := range r.Result.(map[string]any)["tools"].([]any) {
		spec := raw.(map[string]any)
		if spec["name"] != name {
			continue
		}
		ann, ok := spec["annotations"].(map[string]any)
		return ok && ann["readOnlyHint"] == true
	}
	t.Fatalf("tool %s not listed", name)
	return false
}

func TestSelectDevice(t *testing.T) {
	base, _ := startControl(t)

	// A second device with a distinct track title.
	srv2 := control.New(
		control.Config{Addr: "127.0.0.1:0"},
		func() control.State {
			return control.State{State: "paused", Track: &control.Track{ID: "2", Title: "Other Song"}}
		},
		func() control.Account { return control.Account{Name: "other"} },
		control.Commands{},
		nil,
	)
	if err := srv2.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv2.Close)

	resps := run(t, base,
		// host:port form (as returned by list_devices) must be accepted.
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"select_device","arguments":{"url":"`+srv2.Addr()+`"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_status","arguments":{}}}`,
		// Omitting url resets to the default device from the environment.
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"select_device","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_status","arguments":{}}}`,
		// An unreachable device must error and leave the target unchanged.
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"select_device","arguments":{"url":"127.0.0.1:1"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"get_status","arguments":{}}}`,
	)
	if len(resps) != 6 {
		t.Fatalf("got %d responses, want 6", len(resps))
	}
	if text := contentText(t, resps[0]); !strings.Contains(text, `"name": "other"`) {
		t.Fatalf("select_device text = %s", text)
	}
	if text := contentText(t, resps[1]); !strings.Contains(text, "Other Song") {
		t.Fatalf("get_status after select = %s", text)
	}
	if text := contentText(t, resps[3]); !strings.Contains(text, `"title": "Song"`) {
		t.Fatalf("get_status after reset = %s", text)
	}
	if resps[4].Result.(map[string]any)["isError"] != true {
		t.Fatal("selecting an unreachable device must error")
	}
	if text := contentText(t, resps[5]); !strings.Contains(text, `"title": "Song"`) {
		t.Fatalf("target changed after failed select: %s", text)
	}
}

// TestParseErrorRecovery verifies the read loop never dies on a bad input line:
// a garbled (non-JSON) line and an oversized line (past maxLineBytes) each draw
// a JSON-RPC -32700 parse error with a null id, and the server keeps serving —
// the valid requests before and after both dispatch.
func TestParseErrorRecovery(t *testing.T) {
	base, r := startControl(t)
	oversized := strings.Repeat("A", maxLineBytes+1024) // one line past the cap
	resps := run(t, base,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"play_pause","arguments":{}}}`,
		"this is not json at all", // garbled → -32700, loop continues
		oversized,                 // over the line cap → -32700, loop continues
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"play_pause","arguments":{}}}`,
	)
	if len(resps) != 4 {
		t.Fatalf("got %d responses, want 4 (2 ok + 2 parse errors)", len(resps))
	}
	// The two valid play_pause calls (first and last) must both have dispatched,
	// proving the server survived both parse errors in between.
	if r.played != 2 {
		t.Fatalf("play_pause dispatched %d times, want 2 (server should survive parse errors)", r.played)
	}
	// The two middle responses are parse errors with a null id.
	for i, resp := range resps {
		wantErr := i == 1 || i == 2
		gotErr := resp.Error != nil
		if gotErr != wantErr {
			t.Fatalf("response %d: error=%v, want error=%v (%+v)", i, gotErr, wantErr, resp)
		}
		if wantErr {
			if resp.Error.Code != -32700 {
				t.Errorf("response %d: code=%d, want -32700", i, resp.Error.Code)
			}
			if string(resp.ID) != "null" {
				t.Errorf("response %d: id=%q, want null", i, string(resp.ID))
			}
		}
	}
}

func contentText(t *testing.T, r rpcResp) string {
	t.Helper()
	res, ok := r.Result.(map[string]any)
	if !ok {
		t.Fatalf("no result map in %+v", r)
	}
	content := res["content"].([]any)
	return content[0].(map[string]any)["text"].(string)
}
