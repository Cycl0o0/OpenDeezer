package main

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Cycl0o0/OpenDeezer/internal/control"
)

// startControl spins up a real control server and returns its base URL + a
// pointer to a counter incremented when PlayPause is called.
func startControl(t *testing.T) (string, *int) {
	t.Helper()
	played := 0
	srv := control.New(
		control.Config{Addr: "127.0.0.1:0"},
		func() control.State {
			return control.State{State: "playing", Volume: 0.4,
				Track: &control.Track{ID: "1", Title: "Song", Artist: "Artist"}}
		},
		func() control.Account { return control.Account{Name: "me"} },
		control.Commands{PlayPause: func() { played++ }},
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
	return "http://" + srv.Addr(), &played
}

// run feeds newline-delimited JSON-RPC requests through the MCP server and
// returns the decoded responses.
func run(t *testing.T, base string, lines ...string) []rpcResp {
	t.Helper()
	c := control.NewClient(base, "", "")
	s := &server{client: c, tools: buildTools(c)}
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
	base, played := startControl(t)
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
	if *played != 1 {
		t.Fatalf("play_pause not dispatched (played=%d)", *played)
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

func contentText(t *testing.T, r rpcResp) string {
	t.Helper()
	res, ok := r.Result.(map[string]any)
	if !ok {
		t.Fatalf("no result map in %+v", r)
	}
	content := res["content"].([]any)
	return content[0].(map[string]any)["text"].(string)
}
