package discord

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte(`{"hello":"world"}`)
	if err := writeFrame(&buf, opFrame, payload); err != nil {
		t.Fatal(err)
	}
	op, got, err := readFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if op != opFrame || !bytes.Equal(got, payload) {
		t.Fatalf("round-trip mismatch: op=%d got=%s", op, got)
	}
}

func TestActivityFramePlaying(t *testing.T) {
	r := &richPresence{appID: "x", pid: 42}
	b := r.activityFrame(State{
		Status: "playing", Title: "Song", Artist: "Artist", Album: "Album",
		PositionMS: 5000, DurationMS: 200000,
	})
	var p struct {
		Cmd  string `json:"cmd"`
		Args struct {
			PID      int `json:"pid"`
			Activity struct {
				Type       int            `json:"type"`
				Details    string         `json:"details"`
				State      string         `json:"state"`
				Timestamps map[string]any `json:"timestamps"`
			} `json:"activity"`
		} `json:"args"`
		Nonce string `json:"nonce"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if p.Cmd != "SET_ACTIVITY" || p.Args.PID != 42 {
		t.Fatalf("cmd/pid wrong: %+v", p)
	}
	a := p.Args.Activity
	if a.Type != 2 || a.Details != "Song" || a.State != "by Artist" {
		t.Fatalf("activity wrong: %+v", a)
	}
	if a.Timestamps["start"] == nil || a.Timestamps["end"] == nil {
		t.Fatalf("expected timestamps when playing: %+v", a.Timestamps)
	}
	if p.Nonce == "" {
		t.Fatal("nonce must be set")
	}
}

func TestClearFrameHasNullActivity(t *testing.T) {
	r := &richPresence{appID: "x", pid: 1}
	b := r.clearFrame()
	var p struct {
		Args struct {
			Activity json.RawMessage `json:"activity"`
		} `json:"args"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if string(p.Args.Activity) != "null" {
		t.Fatalf("clear activity = %s, want null", p.Args.Activity)
	}
}

func TestNewEmptyIsNoop(t *testing.T) {
	if _, ok := New("").(noop); !ok {
		t.Fatal("New(\"\") should be a no-op")
	}
	// A no-op Update/Close must not panic.
	p := New("")
	p.Update(State{Status: "playing", Title: "x"})
	p.Close()
}

func TestB19CloseBounded(t *testing.T) {
	origTimeout := closeTimeout
	closeTimeout = 10 * time.Millisecond
	defer func() { closeTimeout = origTimeout }()

	r := &richPresence{
		wake: make(chan struct{}, 1),
		done: make(chan struct{}),
	}

	start := time.Now()
	r.Close()
	elapsed := time.Since(start)

	// Since r.done is never closed, Close must timeout on closeTimeout (10ms)
	if elapsed < 10*time.Millisecond {
		t.Errorf("Close returned too early: %v", elapsed)
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("Close took too long: %v, want close to %v", elapsed, closeTimeout)
	}
}
