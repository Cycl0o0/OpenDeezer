package control

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestServer starts a server on a random loopback port and returns it + base URL.
func newTestServer(t *testing.T, cfg Config, status func() State, cmds Commands) (*Server, string) {
	t.Helper()
	cfg.Addr = "127.0.0.1:0"
	s := New(cfg, status, nil, cmds, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s, "http://" + s.Addr()
}

func TestStatusAndCommandDispatch(t *testing.T) {
	var played int
	st := State{State: "playing", Volume: 0.5}
	_, base := newTestServer(t, Config{}, func() State { return st },
		Commands{PlayPause: func() { played++ }})

	resp, err := http.Get(base + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got State
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.State != "playing" || got.Volume != 0.5 {
		t.Fatalf("status = %+v", got)
	}

	r2, err := http.Post(base+"/playpause", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if played != 1 {
		t.Fatalf("playpause called %d times, want 1", played)
	}
}

// getWith does a GET with optional auth headers and returns the status code.
func getWith(t *testing.T, url string, headers map[string]string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestTokenAuth(t *testing.T) {
	_, base := newTestServer(t, Config{Token: "secret"},
		func() State { return State{} }, Commands{})

	if code := getWith(t, base+"/status", nil); code != http.StatusUnauthorized {
		t.Fatalf("no-token status = %d, want 401", code)
	}
	if code := getWith(t, base+"/status", map[string]string{"X-OpenDeezer-Token": "wrong"}); code != http.StatusUnauthorized {
		t.Fatalf("wrong-token status = %d, want 401", code)
	}
	if code := getWith(t, base+"/status", map[string]string{"X-OpenDeezer-Token": "secret"}); code != http.StatusOK {
		t.Fatalf("token status = %d, want 200", code)
	}
	// Query-string token is NOT accepted (header-only).
	if code := getWith(t, base+"/status?token=secret", nil); code != http.StatusUnauthorized {
		t.Fatalf("query-token status = %d, want 401", code)
	}
}

func TestSameAccountRejectsWhenUnknown(t *testing.T) {
	// nil account provider => account id "" => every authed request is rejected.
	_, base := newTestServer(t, Config{SameAccountOnly: true},
		func() State { return State{} }, Commands{})

	if code := getWith(t, base+"/status", map[string]string{"X-OpenDeezer-Account": "12345"}); code != http.StatusUnauthorized {
		t.Fatalf("same-account (unknown) status = %d, want 401", code)
	}
}

func TestSameAccountMatch(t *testing.T) {
	s := New(Config{Addr: "127.0.0.1:0", SameAccountOnly: true},
		func() State { return State{} },
		func() Account { return Account{UserID: "42", Name: "me"} },
		Commands{}, nil)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	base := "http://" + s.Addr()

	if code := getWith(t, base+"/status", map[string]string{"X-OpenDeezer-Account": "999"}); code != http.StatusUnauthorized {
		t.Fatalf("wrong account = %d, want 401", code)
	}
	if code := getWith(t, base+"/status", map[string]string{"X-OpenDeezer-Account": "42"}); code != http.StatusOK {
		t.Fatalf("matching account = %d, want 200", code)
	}
}

// TestMutationRequiresPostAndNoOrigin covers the CSRF defenses.
func TestMutationRequiresPostAndNoOrigin(t *testing.T) {
	var played int
	_, base := newTestServer(t, Config{},
		func() State { return State{} },
		Commands{PlayPause: func() { played++ }})

	// GET on a mutating endpoint -> 405 (not executed).
	if code := getWith(t, base+"/playpause", nil); code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /playpause = %d, want 405", code)
	}
	// POST with a browser Origin header -> 403 (CSRF blocked).
	req, _ := http.NewRequest(http.MethodPost, base+"/playpause", nil)
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin POST = %d, want 403", resp.StatusCode)
	}
	if played != 0 {
		t.Fatalf("command ran despite blocked requests (played=%d)", played)
	}
	// Plain POST (no Origin) -> 200 and runs.
	r2, err := http.Post(base+"/playpause", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if played != 1 {
		t.Fatalf("playpause ran %d times, want 1", played)
	}
}

func TestStartRefusesOpenModeOnLAN(t *testing.T) {
	// No token + no same-account on a non-loopback bind must fail closed.
	s := New(Config{Addr: "0.0.0.0:0"}, func() State { return State{} }, nil, Commands{}, nil)
	if err := s.Start(); err == nil {
		s.Close()
		t.Fatal("Start should refuse unauthenticated none-mode on a non-loopback address")
	}
	// Same config but loopback is allowed (localhost-only use).
	s2 := New(Config{Addr: "127.0.0.1:0"}, func() State { return State{} }, nil, Commands{}, nil)
	if err := s2.Start(); err != nil {
		t.Fatalf("loopback none-mode should be allowed: %v", err)
	}
	s2.Close()
}

// TestHostHeaderValidation covers the DNS-rebinding guard: requests with a DNS
// name in the Host header are rejected, while localhost / literal-IP hosts pass.
func TestHostHeaderValidation(t *testing.T) {
	_, base := newTestServer(t, Config{},
		func() State { return State{State: "playing"} }, Commands{})

	// Baseline: the loopback IP host used by the test client is accepted.
	if code := getWith(t, base+"/status", nil); code != http.StatusOK {
		t.Fatalf("IP-literal host = %d, want 200", code)
	}

	// A rebound DNS name in the Host header is rejected before reaching handlers.
	req, _ := http.NewRequest(http.MethodGet, base+"/status", nil)
	req.Host = "attacker.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("DNS-name host = %d, want 403", resp.StatusCode)
	}

	// localhost is an accepted form.
	req2, _ := http.NewRequest(http.MethodGet, base+"/status", nil)
	req2.Host = "localhost:7654"
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("localhost host = %d, want 200", resp2.StatusCode)
	}

	// A LAN IP literal is accepted (an IP cannot be DNS-rebound).
	req3, _ := http.NewRequest(http.MethodGet, base+"/status", nil)
	req3.Host = "192.168.1.42:7654"
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("LAN IP host = %d, want 200", resp3.StatusCode)
	}
}

func TestWhoamiIsUnauthenticated(t *testing.T) {
	s, base := newTestServer(t, Config{Token: "secret"},
		func() State { return State{} }, Commands{})
	s.SetVersion("1.2.3")

	resp, err := http.Get(base + "/whoami") // no token, still 200
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("whoami status = %d, want 200", resp.StatusCode)
	}
	var who Whoami
	if err := json.NewDecoder(resp.Body).Decode(&who); err != nil {
		t.Fatal(err)
	}
	if who.Auth != "token" || who.Version != "1.2.3" {
		t.Fatalf("whoami = %+v", who)
	}
}

func nextSSEState(t *testing.T, scanner *bufio.Scanner) State {
	t.Helper()
	event := ""
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if event != "state" || data.Len() == 0 {
				event = ""
				data.Reset()
				continue
			}
			var state State
			if err := json.Unmarshal([]byte(data.String()), &state); err != nil {
				t.Fatalf("decode SSE state %q: %v", data.String(), err)
			}
			return state
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	t.Fatalf("SSE stream ended before a state event: %v", scanner.Err())
	return State{}
}

func openEventStream(t *testing.T, ctx context.Context, url string, headers map[string]string) (*http.Response, *bufio.Scanner) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("GET events status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		resp.Body.Close()
		t.Fatalf("events Content-Type = %q, want text/event-stream", got)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024), 8<<20)
	return resp, scanner
}

func eventSubscriberCount(s *Server) int {
	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()
	return len(s.eventSubscribers)
}

func waitForEventSubscriberCount(t *testing.T, s *Server, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := eventSubscriberCount(s); got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("event subscriber count = %d, want %d", eventSubscriberCount(s), want)
}

func TestEventsInitialSnapshotAndNotify(t *testing.T) {
	var stateMu sync.RWMutex
	state := State{State: "stopped", Volume: 0.25, Repeat: "off"}
	snapshot := func() State {
		stateMu.RLock()
		defer stateMu.RUnlock()
		return state
	}
	s, base := newTestServer(t, Config{}, snapshot, Commands{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, scanner := openEventStream(t, ctx, base+"/events", nil)
	defer resp.Body.Close()
	initial := nextSSEState(t, scanner)
	if initial.State != "stopped" || initial.Volume != 0.25 {
		t.Fatalf("initial SSE state = %+v", initial)
	}

	stateMu.Lock()
	state.State = "playing"
	state.PositionMS = 1234
	stateMu.Unlock()
	s.NotifyStateChanged()
	changed := nextSSEState(t, scanner)
	if changed.State != "playing" || changed.PositionMS != 1234 {
		t.Fatalf("notified SSE state = %+v", changed)
	}
}

func TestEventsFallbackDetectsSnapshotChange(t *testing.T) {
	var stateMu sync.RWMutex
	state := State{State: "paused"}
	snapshot := func() State {
		stateMu.RLock()
		defer stateMu.RUnlock()
		return state
	}
	_, base := newTestServer(t, Config{}, snapshot, Commands{})

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	resp, scanner := openEventStream(t, ctx, base+"/events", nil)
	defer resp.Body.Close()
	_ = nextSSEState(t, scanner)

	stateMu.Lock()
	state.State = "playing"
	state.PositionMS = 99
	stateMu.Unlock()
	changed := nextSSEState(t, scanner)
	if changed.State != "playing" || changed.PositionMS != 99 {
		t.Fatalf("fallback SSE state = %+v", changed)
	}
}

func TestEventsReturnToStateSeenBeforeSubscription(t *testing.T) {
	var stateMu sync.RWMutex
	state := State{State: "stopped", PositionMS: 10}
	snapshot := func() State {
		stateMu.RLock()
		defer stateMu.RUnlock()
		return state
	}
	s, base := newTestServer(t, Config{}, snapshot, Commands{})

	// The server has already existed in A. Connect while it is in B, then
	// return to A. Per-subscriber diffing must not suppress that B→A event.
	stateMu.Lock()
	state = State{State: "playing", PositionMS: 20}
	stateMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, scanner := openEventStream(t, ctx, base+"/events", nil)
	defer resp.Body.Close()
	if initial := nextSSEState(t, scanner); initial.State != "playing" {
		t.Fatalf("initial B state = %+v", initial)
	}

	stateMu.Lock()
	state = State{State: "stopped", PositionMS: 10}
	stateMu.Unlock()
	s.NotifyStateChanged()
	if returned := nextSSEState(t, scanner); returned.State != "stopped" || returned.PositionMS != 10 {
		t.Fatalf("returned A state = %+v", returned)
	}
}

func TestEventsAuthAndEventSourceSessionQuery(t *testing.T) {
	_, tokenBase := newTestServer(t, Config{Token: "secret"}, func() State { return State{} }, Commands{})
	if code := getWith(t, tokenBase+"/events", nil); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /events = %d, want 401", code)
	}

	s, sessionBase := newTestServer(t, Config{WebRemote: true}, func() State {
		return State{State: "playing"}
	}, Commands{})
	s.injectSession("event-source-session", time.Now().Add(time.Hour))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, scanner := openEventStream(t, ctx, sessionBase+"/events?session=event-source-session", nil)
	if got := nextSSEState(t, scanner); got.State != "playing" {
		resp.Body.Close()
		t.Fatalf("query-session SSE state = %+v", got)
	}
	resp.Body.Close()

	// The browser exception is endpoint-only; other reads remain header-only.
	if code := getWith(t, sessionBase+"/status?session=event-source-session", nil); code != http.StatusUnauthorized {
		t.Fatalf("query session on /status = %d, want 401", code)
	}
}

func TestEventsDisconnectUnsubscribes(t *testing.T) {
	s, base := newTestServer(t, Config{}, func() State { return State{State: "stopped"} }, Commands{})
	ctx, cancel := context.WithCancel(context.Background())
	resp, scanner := openEventStream(t, ctx, base+"/events", nil)
	_ = nextSSEState(t, scanner)
	waitForEventSubscriberCount(t, s, 1)

	cancel()
	resp.Body.Close()
	waitForEventSubscriberCount(t, s, 0)
}

func TestEventsSlowSubscriberDoesNotBlockAnother(t *testing.T) {
	var stateMu sync.RWMutex
	state := State{State: "stopped"}
	s := New(Config{}, func() State {
		stateMu.RLock()
		defer stateMu.RUnlock()
		return state
	}, nil, Commands{}, nil)
	s.startEventLoop()
	defer s.stopEventLoop()

	slow := s.subscribeEvents()
	fast := s.subscribeEvents()
	defer s.unsubscribeEvents(slow)
	defer s.unsubscribeEvents(fast)
	slow <- struct{}{} // occupy the slow subscriber's only buffer slot

	stateMu.Lock()
	state.State = "playing"
	state.PositionMS = 42
	stateMu.Unlock()
	s.NotifyStateChanged()

	select {
	case <-fast:
	case <-time.After(time.Second):
		t.Fatal("fast subscriber was blocked by a full slow subscriber")
	}

	select {
	case <-slow:
		// A notification is only a prompt to take a fresh snapshot, so the
		// already-buffered signal naturally coalesces to the newest state.
		payload, err := s.stateEventPayload()
		if err != nil {
			t.Fatal(err)
		}
		var got State
		if err := json.Unmarshal([]byte(payload), &got); err != nil {
			t.Fatal(err)
		}
		if got.State != "playing" || got.PositionMS != 42 {
			t.Fatalf("slow subscriber fresh state = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("slow subscriber lost its coalesced notification")
	}
}

func TestClientEventsReceivesStatesAndUsesAuth(t *testing.T) {
	initial := State{
		State:      "paused",
		PositionMS: 1200,
		DurationMS: 5000,
		Volume:     0.4,
		Repeat:     "off",
	}
	changed := initial
	changed.State = "playing"
	changed.PositionMS = 1400

	releaseChange := make(chan struct{})
	requestErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			requestErr <- fmt.Errorf("path = %q, want /events", r.URL.Path)
			return
		}
		if got := r.Header.Get("X-OpenDeezer-Token"); got != "token" {
			requestErr <- fmt.Errorf("token header = %q, want token", got)
			return
		}
		if got := r.Header.Get("X-OpenDeezer-Account"); got != "account" {
			requestErr <- fmt.Errorf("account header = %q, want account", got)
			return
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			requestErr <- fmt.Errorf("Accept header = %q, want text/event-stream", got)
			return
		}
		requestErr <- nil

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, ": keepalive\n\nevent: ignored\ndata: {}\n\nevent: state\ndata: not-json\n\n")
		writeClientStateEvent(t, w, initial, true)
		w.(http.Flusher).Flush()

		select {
		case <-releaseChange:
		case <-r.Context().Done():
			return
		}
		writeClientStateEvent(t, w, changed, true)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	client := NewClient(server.URL, "token", "account")
	// A regular request would expire at this deadline. Events must be governed
	// only by ctx because the server's keepalive interval can be longer.
	client.http.Timeout = 20 * time.Millisecond
	states, err := client.Events(ctx)
	if err != nil {
		cancel()
		t.Fatalf("Events: %v", err)
	}
	if err := <-requestErr; err != nil {
		cancel()
		t.Fatal(err)
	}

	assertClientState(t, receiveClientState(t, states), initial)
	time.Sleep(30 * time.Millisecond)
	close(releaseChange)
	assertClientState(t, receiveClientState(t, states), changed)

	cancel()
	select {
	case _, ok := <-states:
		if ok {
			t.Fatal("event channel remained open after context cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("event channel did not close after context cancellation")
	}
}

func TestClientEventsClosesOnDisconnect(t *testing.T) {
	want := State{State: "stopped", Volume: 1, Repeat: "all"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Deliberately omit the final blank line: EOF still dispatches the event.
		writeClientStateEvent(t, w, want, false)
	}))
	defer server.Close()

	states, err := NewClient(server.URL, "", "").Events(context.Background())
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	assertClientState(t, receiveClientState(t, states), want)
	select {
	case _, ok := <-states:
		if ok {
			t.Fatal("event channel remained open after server disconnect")
		}
	case <-time.After(time.Second):
		t.Fatal("event channel did not close after server disconnect")
	}
}

func TestClientEventsRejectsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	states, err := NewClient(server.URL, "", "").Events(context.Background())
	if err == nil {
		t.Fatal("Events succeeded for an unauthorized response")
	}
	if states != nil {
		t.Fatal("Events returned a channel for an unauthorized response")
	}
	if !strings.Contains(err.Error(), "401 Unauthorized") {
		t.Fatalf("Events error = %q, want HTTP status", err)
	}
}

func writeClientStateEvent(t *testing.T, w http.ResponseWriter, state State, terminate bool) {
	t.Helper()
	b, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal State: %v", err)
	}
	_, _ = fmt.Fprintf(w, "event: state\ndata: %s\n", b)
	if terminate {
		_, _ = fmt.Fprint(w, "\n")
	}
}

func receiveClientState(t *testing.T, states <-chan State) State {
	t.Helper()
	select {
	case state, ok := <-states:
		if !ok {
			t.Fatal("event channel closed before state arrived")
		}
		return state
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for state event")
		return State{}
	}
}

func assertClientState(t *testing.T, got, want State) {
	t.Helper()
	if got.State != want.State || got.PositionMS != want.PositionMS ||
		got.DurationMS != want.DurationMS || got.Volume != want.Volume ||
		got.Repeat != want.Repeat {
		t.Fatalf("state = %+v, want %+v", got, want)
	}
}

// postWith does a POST with optional headers and returns the status code.
func postWith(t *testing.T, url string, headers map[string]string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func newMutationTestServer(cmds Commands) *Server {
	return New(Config{}, func() State { return State{State: "stopped"} }, nil, cmds, nil)
}

func assertMutationResponse(t *testing.T, rr *httptest.ResponseRecorder, wantStatus int, wantError string) {
	t.Helper()
	if rr.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, wantStatus, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if wantError != "" {
		var body map[string]string
		if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
			t.Fatalf("decode error body: %v", err)
		}
		if len(body) != 1 || body["error"] != wantError {
			t.Fatalf("error body = %#v, want only error=%q", body, wantError)
		}
		return
	}
	var state State
	if err := json.NewDecoder(rr.Body).Decode(&state); err != nil {
		t.Fatalf("decode success state: %v", err)
	}
	if state.State != "stopped" {
		t.Fatalf("success state = %+v, want stopped snapshot", state)
	}
}

type extendedControlCall struct {
	id         string
	next       bool
	index      int
	from       int
	to         int
	historyLen int
}

type invalidControlRequest struct {
	path    string
	message string
}

func serveControlRoute(t *testing.T, cmds Commands, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	s := newMutationTestServer(cmds)
	mux := http.NewServeMux()
	s.routes(mux)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(method, path, nil))
	return rr
}

func TestExtendedControlEndpoints(t *testing.T) {
	type endpoint struct {
		name      string
		method    string
		validPath string
		invalid   []invalidControlRequest
		wantCall  extendedControlCall
		nilStatus int
		history   bool
		install   func(*Commands, *extendedControlCall, *int, error)
	}

	const (
		idError    = "id must be a positive decimal integer"
		indexError = "i must be a non-negative integer"
		moveError  = "from and to must be non-negative integers"
		historyErr = "n must be an integer between 1 and 500"
	)
	endpoints := []endpoint{
		{
			name: "queue add", method: http.MethodPost, validPath: "/queue/add?id=123&next=1",
			wantCall: extendedControlCall{id: "123", next: true}, nilStatus: http.StatusNotImplemented,
			invalid: []invalidControlRequest{
				{path: "/queue/add?next=1", message: idError},
				{path: "/queue/add?id=abc&next=1", message: idError},
				{path: "/queue/add?id=0&next=1", message: idError},
				{path: "/queue/add?id=18446744073709551616&next=1", message: idError},
				{path: "/queue/add?id=1&id=2&next=1", message: idError},
				{path: "/queue/add?id=123", message: "next must be 0 or 1"},
				{path: "/queue/add?id=123&next=true", message: "next must be 0 or 1"},
				{path: "/queue/add?id=123&next=0&next=1", message: "next must be 0 or 1"},
			},
			install: func(cmds *Commands, got *extendedControlCall, calls *int, result error) {
				cmds.QueueAdd = func(id string, next bool) error {
					(*calls)++
					got.id, got.next = id, next
					return result
				}
			},
		},
		{
			name: "queue jump", method: http.MethodPost, validPath: "/queue/jump?i=0",
			wantCall: extendedControlCall{index: 0}, nilStatus: http.StatusNotImplemented,
			invalid: []invalidControlRequest{
				{path: "/queue/jump", message: indexError},
				{path: "/queue/jump?i=-1", message: indexError},
				{path: "/queue/jump?i=%2B1", message: indexError},
				{path: "/queue/jump?i=one", message: indexError},
				{path: "/queue/jump?i=1&i=2", message: indexError},
				{path: "/queue/jump?i=999999999999999999999999", message: indexError},
			},
			install: func(cmds *Commands, got *extendedControlCall, calls *int, result error) {
				cmds.QueueJump = func(index int) error {
					(*calls)++
					got.index = index
					return result
				}
			},
		},
		{
			name: "queue remove", method: http.MethodPost, validPath: "/queue/remove?i=2",
			wantCall: extendedControlCall{index: 2}, nilStatus: http.StatusNotImplemented,
			invalid: []invalidControlRequest{
				{path: "/queue/remove", message: indexError},
				{path: "/queue/remove?i=-1", message: indexError},
				{path: "/queue/remove?i=x", message: indexError},
				{path: "/queue/remove?i=1&i=2", message: indexError},
			},
			install: func(cmds *Commands, got *extendedControlCall, calls *int, result error) {
				cmds.QueueRemove = func(index int) error {
					(*calls)++
					got.index = index
					return result
				}
			},
		},
		{
			name: "queue move", method: http.MethodPost, validPath: "/queue/move?from=0&to=3",
			wantCall: extendedControlCall{from: 0, to: 3}, nilStatus: http.StatusNotImplemented,
			invalid: []invalidControlRequest{
				{path: "/queue/move?from=0", message: moveError},
				{path: "/queue/move?from=-1&to=2", message: moveError},
				{path: "/queue/move?from=1&to=x", message: moveError},
				{path: "/queue/move?from=1&from=2&to=3", message: moveError},
			},
			install: func(cmds *Commands, got *extendedControlCall, calls *int, result error) {
				cmds.QueueMove = func(from, to int) error {
					(*calls)++
					got.from, got.to = from, to
					return result
				}
			},
		},
		{
			name: "play album", method: http.MethodPost, validPath: "/play/album?id=456",
			wantCall: extendedControlCall{id: "456"}, nilStatus: http.StatusNotImplemented,
			invalid: []invalidControlRequest{
				{path: "/play/album", message: idError},
				{path: "/play/album?id=album", message: idError},
				{path: "/play/album?id=1&id=2", message: idError},
			},
			install: func(cmds *Commands, got *extendedControlCall, calls *int, result error) {
				cmds.PlayAlbum = func(id string) error {
					(*calls)++
					got.id = id
					return result
				}
			},
		},
		{
			name: "play track mix", method: http.MethodPost, validPath: "/play/mix/track?id=789",
			wantCall: extendedControlCall{id: "789"}, nilStatus: http.StatusNotImplemented,
			invalid: []invalidControlRequest{
				{path: "/play/mix/track", message: idError},
				{path: "/play/mix/track?id=-1", message: idError},
				{path: "/play/mix/track?id=7&id=8", message: idError},
			},
			install: func(cmds *Commands, got *extendedControlCall, calls *int, result error) {
				cmds.PlayMixTrack = func(id string) error {
					(*calls)++
					got.id = id
					return result
				}
			},
		},
		{
			name: "play artist mix", method: http.MethodPost, validPath: "/play/mix/artist?id=321",
			wantCall: extendedControlCall{id: "321"}, nilStatus: http.StatusNotImplemented,
			invalid: []invalidControlRequest{
				{path: "/play/mix/artist", message: idError},
				{path: "/play/mix/artist?id=artist", message: idError},
				{path: "/play/mix/artist?id=3&id=4", message: idError},
			},
			install: func(cmds *Commands, got *extendedControlCall, calls *int, result error) {
				cmds.PlayMixArtist = func(id string) error {
					(*calls)++
					got.id = id
					return result
				}
			},
		},
		{
			name: "recent history", method: http.MethodGet, validPath: "/history/recent?n=10",
			wantCall: extendedControlCall{historyLen: 10}, nilStatus: http.StatusNotFound, history: true,
			invalid: []invalidControlRequest{
				{path: "/history/recent?n=", message: historyErr},
				{path: "/history/recent?n=0", message: historyErr},
				{path: "/history/recent?n=-1", message: historyErr},
				{path: "/history/recent?n=501", message: historyErr},
				{path: "/history/recent?n=ten", message: historyErr},
				{path: "/history/recent?n=1&n=2", message: historyErr},
			},
			install: func(cmds *Commands, got *extendedControlCall, calls *int, result error) {
				cmds.HistoryRecent = func(n int) (json.RawMessage, error) {
					(*calls)++
					got.historyLen = n
					return json.RawMessage(`[{"trackId":"1"}]`), result
				}
			},
		},
	}

	for _, endpoint := range endpoints {
		t.Run(endpoint.name, func(t *testing.T) {
			t.Run("valid", func(t *testing.T) {
				var cmds Commands
				var got extendedControlCall
				calls := 0
				endpoint.install(&cmds, &got, &calls, nil)
				rr := serveControlRoute(t, cmds, endpoint.method, endpoint.validPath)
				if endpoint.history {
					if rr.Code != http.StatusOK || rr.Header().Get("Content-Type") != "application/json" {
						t.Fatalf("history response = %d/%q, body=%s", rr.Code, rr.Header().Get("Content-Type"), rr.Body.String())
					}
					if gotBody := strings.TrimSpace(rr.Body.String()); gotBody != `[{"trackId":"1"}]` {
						t.Fatalf("history body = %s", gotBody)
					}
				} else {
					assertMutationResponse(t, rr, http.StatusOK, "")
				}
				if calls != 1 || got != endpoint.wantCall {
					t.Fatalf("callback calls/args = %d/%+v, want 1/%+v", calls, got, endpoint.wantCall)
				}
			})

			for _, invalid := range endpoint.invalid {
				t.Run("invalid "+invalid.path, func(t *testing.T) {
					var cmds Commands
					var got extendedControlCall
					calls := 0
					endpoint.install(&cmds, &got, &calls, nil)
					rr := serveControlRoute(t, cmds, endpoint.method, invalid.path)
					assertMutationResponse(t, rr, http.StatusBadRequest, invalid.message)
					if calls != 0 {
						t.Fatalf("callback ran %d times for invalid input", calls)
					}
				})
			}

			t.Run("unsupported", func(t *testing.T) {
				rr := serveControlRoute(t, Commands{}, endpoint.method, endpoint.validPath)
				assertMutationResponse(t, rr, endpoint.nilStatus, "not available")
			})

			t.Run("callback conflict", func(t *testing.T) {
				var cmds Commands
				var got extendedControlCall
				calls := 0
				endpoint.install(&cmds, &got, &calls, errors.New("cannot apply"))
				rr := serveControlRoute(t, cmds, endpoint.method, endpoint.validPath)
				assertMutationResponse(t, rr, http.StatusConflict, "cannot apply")
				if calls != 1 {
					t.Fatalf("callback calls = %d, want 1", calls)
				}
			})
		})
	}

	t.Run("queue add next zero", func(t *testing.T) {
		gotNext := true
		cmds := Commands{QueueAdd: func(_ string, next bool) error { gotNext = next; return nil }}
		rr := serveControlRoute(t, cmds, http.MethodPost, "/queue/add?id=1&next=0")
		assertMutationResponse(t, rr, http.StatusOK, "")
		if gotNext {
			t.Fatal("next=0 was not passed as false")
		}
	})

	t.Run("history default", func(t *testing.T) {
		gotN := 0
		cmds := Commands{HistoryRecent: func(n int) (json.RawMessage, error) {
			gotN = n
			return json.RawMessage(`[]`), nil
		}}
		rr := serveControlRoute(t, cmds, http.MethodGet, "/history/recent")
		if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "[]" {
			t.Fatalf("default history response = %d %s", rr.Code, rr.Body.String())
		}
		if gotN != defaultHistoryRecent {
			t.Fatalf("default history n = %d, want %d", gotN, defaultHistoryRecent)
		}
	})

	t.Run("empty history normalizes to array", func(t *testing.T) {
		cmds := Commands{HistoryRecent: func(int) (json.RawMessage, error) { return nil, nil }}
		rr := serveControlRoute(t, cmds, http.MethodGet, "/history/recent?n=1")
		if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "[]" {
			t.Fatalf("empty history response = %d %s", rr.Code, rr.Body.String())
		}
	})
}

func TestExtendedControlClientRoundTrips(t *testing.T) {
	type observedRequest struct {
		method  string
		uri     string
		token   string
		account string
	}
	tests := []struct {
		name   string
		method string
		uri    string
		invoke func(*Client) error
	}{
		{name: "queue add", method: http.MethodPost, uri: "/queue/add?id=123&next=1", invoke: func(c *Client) error { _, err := c.QueueAdd("123", true); return err }},
		{name: "queue jump", method: http.MethodPost, uri: "/queue/jump?i=2", invoke: func(c *Client) error { _, err := c.QueueJump(2); return err }},
		{name: "queue remove", method: http.MethodPost, uri: "/queue/remove?i=3", invoke: func(c *Client) error { _, err := c.QueueRemove(3); return err }},
		{name: "queue move", method: http.MethodPost, uri: "/queue/move?from=1&to=4", invoke: func(c *Client) error { _, err := c.QueueMove(1, 4); return err }},
		{name: "play album", method: http.MethodPost, uri: "/play/album?id=456", invoke: func(c *Client) error { _, err := c.PlayAlbum("456"); return err }},
		{name: "play track mix", method: http.MethodPost, uri: "/play/mix/track?id=789", invoke: func(c *Client) error { _, err := c.PlayMixTrack("789"); return err }},
		{name: "play artist mix", method: http.MethodPost, uri: "/play/mix/artist?id=321", invoke: func(c *Client) error { _, err := c.PlayMixArtist("321"); return err }},
		{name: "recent history", method: http.MethodGet, uri: "/history/recent?n=25", invoke: func(c *Client) error {
			history, err := c.HistoryRecent(25)
			if err == nil && string(history) != `[{"trackId":"1"}]` {
				return fmt.Errorf("history = %s", history)
			}
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seen := make(chan observedRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen <- observedRequest{
					method: r.Method, uri: r.URL.RequestURI(),
					token: r.Header.Get("X-OpenDeezer-Token"), account: r.Header.Get("X-OpenDeezer-Account"),
				}
				w.Header().Set("Content-Type", "application/json")
				if tt.name == "recent history" {
					_, _ = io.WriteString(w, `[{"trackId":"1"}]`)
					return
				}
				_ = json.NewEncoder(w).Encode(State{State: "stopped"})
			}))
			defer server.Close()

			client := NewClient(server.URL, "secret", "42")
			if err := tt.invoke(client); err != nil {
				t.Fatal(err)
			}
			got := <-seen
			if got.method != tt.method || got.uri != tt.uri || got.token != "secret" || got.account != "42" {
				t.Fatalf("request = %+v, want method=%s uri=%s token/account", got, tt.method, tt.uri)
			}
		})
	}
}

func TestWebRemoteQueueCapabilityGating(t *testing.T) {
	html := string(remoteHTML)
	for _, required := range []string{
		"queueCapabilities = {jump: null, remove: null}",
		"QUEUE_PROBE_INDEX = 2147483647",
		"probeQueueCapability('/queue/jump?i=' + QUEUE_PROBE_INDEX)",
		"probeQueueCapability('/queue/remove?i=' + QUEUE_PROBE_INDEX)",
		"if (err && (err.status === 404 || err.status === 501)) return false;",
		"return null; // transient/unknown failure: retry without hiding support",
		"queueProbeInFlight || queueProbeRetryTimer",
		"queueProbeRetryTimer = setTimeout(function()",
		"queueCapabilities.jump === null || queueCapabilities.remove === null",
		"runQueueAction('/queue/jump?i=' + index, 'jump')",
		"runQueueAction('/queue/remove?i=' + index, 'remove')",
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("web remote queue capability logic missing %q", required)
		}
	}
}

func TestVolumeValidation(t *testing.T) {
	const errMessage = "v must be a finite number between 0 and 1"
	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantCall   bool
		wantValue  float64
	}{
		{name: "zero boundary", path: "/volume?v=0", wantStatus: http.StatusOK, wantCall: true, wantValue: 0},
		{name: "fraction", path: "/volume?v=0.5", wantStatus: http.StatusOK, wantCall: true, wantValue: 0.5},
		{name: "one boundary", path: "/volume?v=1", wantStatus: http.StatusOK, wantCall: true, wantValue: 1},
		{name: "missing", path: "/volume", wantStatus: http.StatusBadRequest},
		{name: "empty", path: "/volume?v=", wantStatus: http.StatusBadRequest},
		{name: "non numeric", path: "/volume?v=loud", wantStatus: http.StatusBadRequest},
		{name: "nan", path: "/volume?v=NaN", wantStatus: http.StatusBadRequest},
		{name: "positive infinity", path: "/volume?v=Inf", wantStatus: http.StatusBadRequest},
		{name: "negative infinity", path: "/volume?v=-Inf", wantStatus: http.StatusBadRequest},
		{name: "below range", path: "/volume?v=-0.0001", wantStatus: http.StatusBadRequest},
		{name: "above range", path: "/volume?v=1.0001", wantStatus: http.StatusBadRequest},
		{name: "duplicate", path: "/volume?v=0.2&v=0.3", wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			gotValue := 0.0
			s := newMutationTestServer(Commands{SetVolume: func(v float64) {
				calls++
				gotValue = v
			}})
			rr := httptest.NewRecorder()
			s.handleVolume(rr, httptest.NewRequest(http.MethodPost, tt.path, nil))
			wantError := ""
			if tt.wantStatus == http.StatusBadRequest {
				wantError = errMessage
			}
			assertMutationResponse(t, rr, tt.wantStatus, wantError)
			if tt.wantCall {
				if calls != 1 || gotValue != tt.wantValue {
					t.Fatalf("SetVolume calls/value = %d/%v, want 1/%v", calls, gotValue, tt.wantValue)
				}
			} else if calls != 0 {
				t.Fatalf("SetVolume called %d times for invalid input", calls)
			}
		})
	}
}

func TestSeekValidation(t *testing.T) {
	const errMessage = "ms must be a non-negative integer"
	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantCall   bool
		wantValue  int64
	}{
		{name: "zero boundary", path: "/seek?ms=0", wantStatus: http.StatusOK, wantCall: true, wantValue: 0},
		{name: "positive", path: "/seek?ms=12345", wantStatus: http.StatusOK, wantCall: true, wantValue: 12345},
		{name: "int64 boundary", path: "/seek?ms=9223372036854775807", wantStatus: http.StatusOK, wantCall: true, wantValue: 9223372036854775807},
		{name: "missing", path: "/seek", wantStatus: http.StatusBadRequest},
		{name: "empty", path: "/seek?ms=", wantStatus: http.StatusBadRequest},
		{name: "non numeric", path: "/seek?ms=soon", wantStatus: http.StatusBadRequest},
		{name: "negative", path: "/seek?ms=-1", wantStatus: http.StatusBadRequest},
		{name: "fraction", path: "/seek?ms=1.5", wantStatus: http.StatusBadRequest},
		{name: "overflow", path: "/seek?ms=9223372036854775808", wantStatus: http.StatusBadRequest},
		{name: "duplicate", path: "/seek?ms=1&ms=2", wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			var gotValue int64
			s := newMutationTestServer(Commands{Seek: func(ms int64) {
				calls++
				gotValue = ms
			}})
			rr := httptest.NewRecorder()
			s.handleSeek(rr, httptest.NewRequest(http.MethodPost, tt.path, nil))
			wantError := ""
			if tt.wantStatus == http.StatusBadRequest {
				wantError = errMessage
			}
			assertMutationResponse(t, rr, tt.wantStatus, wantError)
			if tt.wantCall {
				if calls != 1 || gotValue != tt.wantValue {
					t.Fatalf("Seek calls/value = %d/%d, want 1/%d", calls, gotValue, tt.wantValue)
				}
			} else if calls != 0 {
				t.Fatalf("Seek called %d times for invalid input", calls)
			}
		})
	}
}

func TestRepeatValidation(t *testing.T) {
	const errMessage = "mode must be one of off, all, one"
	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantMode   string
		wantCycle  bool
	}{
		{name: "legacy cycle", path: "/repeat", wantStatus: http.StatusOK, wantCycle: true},
		{name: "off", path: "/repeat?mode=off", wantStatus: http.StatusOK, wantMode: "off"},
		{name: "all", path: "/repeat?mode=all", wantStatus: http.StatusOK, wantMode: "all"},
		{name: "one", path: "/repeat?mode=one", wantStatus: http.StatusOK, wantMode: "one"},
		{name: "empty", path: "/repeat?mode=", wantStatus: http.StatusBadRequest},
		{name: "unknown", path: "/repeat?mode=track", wantStatus: http.StatusBadRequest},
		{name: "wrong case", path: "/repeat?mode=ALL", wantStatus: http.StatusBadRequest},
		{name: "whitespace", path: "/repeat?mode=%20all", wantStatus: http.StatusBadRequest},
		{name: "duplicate", path: "/repeat?mode=all&mode=one", wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var modes []string
			cycles := 0
			s := newMutationTestServer(Commands{
				SetRepeat:   func(mode string) { modes = append(modes, mode) },
				CycleRepeat: func() { cycles++ },
			})
			rr := httptest.NewRecorder()
			s.handleRepeat(rr, httptest.NewRequest(http.MethodPost, tt.path, nil))
			wantError := ""
			if tt.wantStatus == http.StatusBadRequest {
				wantError = errMessage
			}
			assertMutationResponse(t, rr, tt.wantStatus, wantError)
			if tt.wantMode != "" {
				if len(modes) != 1 || modes[0] != tt.wantMode || cycles != 0 {
					t.Fatalf("repeat callbacks modes/cycles = %#v/%d, want [%q]/0", modes, cycles, tt.wantMode)
				}
			} else if tt.wantCycle {
				if len(modes) != 0 || cycles != 1 {
					t.Fatalf("repeat callbacks modes/cycles = %#v/%d, want []/1", modes, cycles)
				}
			} else if len(modes) != 0 || cycles != 0 {
				t.Fatalf("repeat callback ran for invalid input: modes=%#v cycles=%d", modes, cycles)
			}
		})
	}
}

func TestShuffleValidation(t *testing.T) {
	const errMessage = "on must be one of true, false, 1, 0"
	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantSet    bool
		wantValue  bool
		wantToggle bool
	}{
		{name: "legacy toggle", path: "/shuffle", wantStatus: http.StatusOK, wantToggle: true},
		{name: "true", path: "/shuffle?on=true", wantStatus: http.StatusOK, wantSet: true, wantValue: true},
		{name: "one", path: "/shuffle?on=1", wantStatus: http.StatusOK, wantSet: true, wantValue: true},
		{name: "false", path: "/shuffle?on=false", wantStatus: http.StatusOK, wantSet: true, wantValue: false},
		{name: "zero", path: "/shuffle?on=0", wantStatus: http.StatusOK, wantSet: true, wantValue: false},
		{name: "empty", path: "/shuffle?on=", wantStatus: http.StatusBadRequest},
		{name: "wrong case", path: "/shuffle?on=TRUE", wantStatus: http.StatusBadRequest},
		{name: "garbage", path: "/shuffle?on=yes", wantStatus: http.StatusBadRequest},
		{name: "other number", path: "/shuffle?on=2", wantStatus: http.StatusBadRequest},
		{name: "duplicate", path: "/shuffle?on=true&on=false", wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var values []bool
			toggles := 0
			s := newMutationTestServer(Commands{
				SetShuffle:    func(on bool) { values = append(values, on) },
				ToggleShuffle: func() { toggles++ },
			})
			rr := httptest.NewRecorder()
			s.handleShuffle(rr, httptest.NewRequest(http.MethodPost, tt.path, nil))
			wantError := ""
			if tt.wantStatus == http.StatusBadRequest {
				wantError = errMessage
			}
			assertMutationResponse(t, rr, tt.wantStatus, wantError)
			if tt.wantSet {
				if len(values) != 1 || values[0] != tt.wantValue || toggles != 0 {
					t.Fatalf("shuffle callbacks values/toggles = %#v/%d, want [%v]/0", values, toggles, tt.wantValue)
				}
			} else if tt.wantToggle {
				if len(values) != 0 || toggles != 1 {
					t.Fatalf("shuffle callbacks values/toggles = %#v/%d, want []/1", values, toggles)
				}
			} else if len(values) != 0 || toggles != 0 {
				t.Fatalf("shuffle callback ran for invalid input: values=%#v toggles=%d", values, toggles)
			}
		})
	}
}

func TestSleepValidation(t *testing.T) {
	type sleepCall struct {
		minutes int
		eot     bool
	}
	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantSet    *sleepCall
		wantCancel bool
		wantError  string
	}{
		{name: "legacy no-parameter cancel", path: "/sleep", wantStatus: http.StatusOK, wantCancel: true},
		{name: "cancel one", path: "/sleep?cancel=1", wantStatus: http.StatusOK, wantCancel: true},
		{name: "cancel true", path: "/sleep?cancel=true", wantStatus: http.StatusOK, wantCancel: true},
		{name: "zero-minute cancel", path: "/sleep?minutes=0", wantStatus: http.StatusOK, wantCancel: true},
		{name: "one-minute boundary", path: "/sleep?minutes=1", wantStatus: http.StatusOK, wantSet: &sleepCall{minutes: 1}},
		{name: "maximum boundary", path: "/sleep?minutes=1440", wantStatus: http.StatusOK, wantSet: &sleepCall{minutes: 1440}},
		{name: "end of track one", path: "/sleep?eot=1", wantStatus: http.StatusOK, wantSet: &sleepCall{eot: true}},
		{name: "end of track true", path: "/sleep?eot=true&minutes=60", wantStatus: http.StatusOK, wantSet: &sleepCall{minutes: 60, eot: true}},
		{name: "false flags with duration", path: "/sleep?cancel=0&eot=false&minutes=15", wantStatus: http.StatusOK, wantSet: &sleepCall{minutes: 15}},
		{name: "empty minutes", path: "/sleep?minutes=", wantStatus: http.StatusBadRequest, wantError: "minutes must be an integer between 0 and 1440"},
		{name: "non numeric minutes", path: "/sleep?minutes=later", wantStatus: http.StatusBadRequest, wantError: "minutes must be an integer between 0 and 1440"},
		{name: "fractional minutes", path: "/sleep?minutes=1.5", wantStatus: http.StatusBadRequest, wantError: "minutes must be an integer between 0 and 1440"},
		{name: "negative minutes", path: "/sleep?minutes=-1", wantStatus: http.StatusBadRequest, wantError: "minutes must be an integer between 0 and 1440"},
		{name: "above maximum", path: "/sleep?minutes=1441", wantStatus: http.StatusBadRequest, wantError: "minutes must be an integer between 0 and 1440"},
		{name: "overflow", path: "/sleep?minutes=999999999999999999999", wantStatus: http.StatusBadRequest, wantError: "minutes must be an integer between 0 and 1440"},
		{name: "duplicate minutes", path: "/sleep?minutes=15&minutes=30", wantStatus: http.StatusBadRequest, wantError: "minutes must be an integer between 0 and 1440"},
		{name: "bad cancel", path: "/sleep?cancel=yes", wantStatus: http.StatusBadRequest, wantError: "cancel must be one of true, false, 1, 0"},
		{name: "empty cancel", path: "/sleep?cancel=", wantStatus: http.StatusBadRequest, wantError: "cancel must be one of true, false, 1, 0"},
		{name: "bad eot", path: "/sleep?eot=yes", wantStatus: http.StatusBadRequest, wantError: "eot must be one of true, false, 1, 0"},
		{name: "empty eot", path: "/sleep?eot=", wantStatus: http.StatusBadRequest, wantError: "eot must be one of true, false, 1, 0"},
		{name: "cancel still validates duration", path: "/sleep?cancel=1&minutes=bad", wantStatus: http.StatusBadRequest, wantError: "minutes must be an integer between 0 and 1440"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sets []sleepCall
			cancels := 0
			s := newMutationTestServer(Commands{
				SetSleepTimer: func(minutes int, eot bool) {
					sets = append(sets, sleepCall{minutes: minutes, eot: eot})
				},
				CancelSleepTimer: func() { cancels++ },
			})
			rr := httptest.NewRecorder()
			s.handleSleep(rr, httptest.NewRequest(http.MethodPost, tt.path, nil))
			assertMutationResponse(t, rr, tt.wantStatus, tt.wantError)
			if tt.wantSet != nil {
				if len(sets) != 1 || sets[0] != *tt.wantSet || cancels != 0 {
					t.Fatalf("sleep callbacks sets/cancels = %#v/%d, want [%+v]/0", sets, cancels, *tt.wantSet)
				}
			} else if tt.wantCancel {
				if len(sets) != 0 || cancels != 1 {
					t.Fatalf("sleep callbacks sets/cancels = %#v/%d, want []/1", sets, cancels)
				}
			} else if len(sets) != 0 || cancels != 0 {
				t.Fatalf("sleep callback ran for invalid input: sets=%#v cancels=%d", sets, cancels)
			}
		})
	}
}

func pairHandlerRequest(s *Server, remoteAddr, code string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"code": code})
	req := httptest.NewRequest(http.MethodPost, "/pair", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remoteAddr
	rr := httptest.NewRecorder()
	s.handlePair(rr, req)
	return rr
}

func TestPairingCodeGenerationFailureFailsClosed(t *testing.T) {
	t.Run("fresh server", func(t *testing.T) {
		s := New(Config{}, func() State { return State{} }, nil, Commands{}, nil)
		s.mintPairCode = func() (string, error) { return "", errors.New("entropy unavailable") }

		if code := s.EnablePairing(); code != "" {
			t.Fatalf("EnablePairing code = %q after entropy failure, want empty", code)
		}
		if s.PairingActive() || s.PairingCode() != "" || s.webRemoteEnabled() {
			t.Fatal("entropy failure left pairing or the web remote enabled")
		}
		rr := pairHandlerRequest(s, "192.0.2.10:1000", "")
		assertMutationResponse(t, rr, http.StatusInternalServerError, "internal error")
	})

	t.Run("invalidates active code", func(t *testing.T) {
		s := New(Config{WebRemote: true}, func() State { return State{} }, nil, Commands{}, nil)
		s.mintPairCode = func() (string, error) { return "123456", nil }
		if code := s.EnablePairing(); code != "123456" || !s.PairingActive() {
			t.Fatalf("initial EnablePairing code/active = %q/%v", code, s.PairingActive())
		}
		s.mintPairCode = func() (string, error) { return "", errors.New("entropy unavailable") }
		if code := s.EnablePairing(); code != "" {
			t.Fatalf("replacement code = %q after entropy failure, want empty", code)
		}
		if s.PairingActive() || s.PairingCode() != "" {
			t.Fatal("entropy failure left the previous pairing code active")
		}
		if !s.webRemoteEnabled() || !s.sessionAuthRequired() {
			t.Fatal("entropy failure weakened the configured web-remote auth mode")
		}
		rr := pairHandlerRequest(s, "192.0.2.10:1000", "123456")
		assertMutationResponse(t, rr, http.StatusInternalServerError, "internal error")
	})
}

func TestPairingRateLimitIsPerSourceIP(t *testing.T) {
	s := New(Config{}, func() State { return State{} }, nil, Commands{}, nil)
	s.mintPairCode = func() (string, error) { return "123456", nil }
	if code := s.EnablePairing(); code != "123456" {
		t.Fatalf("pairing code = %q, want deterministic code", code)
	}

	type result struct {
		source string
		status int
	}
	start := make(chan struct{})
	results := make(chan result, pairSourceAttemptLimit+1)
	var wg sync.WaitGroup
	for i := 0; i < pairSourceAttemptLimit; i++ {
		wg.Add(1)
		go func(port int) {
			defer wg.Done()
			<-start
			rr := pairHandlerRequest(s, "192.0.2.10:"+strconv.Itoa(1000+port), "654321")
			results <- result{source: "attacker", status: rr.Code}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		rr := pairHandlerRequest(s, "192.0.2.11:2000", "654321")
		results <- result{source: "legitimate", status: rr.Code}
	}()
	close(start)
	wg.Wait()
	close(results)

	attackerResponses := 0
	legitimateResponses := 0
	for got := range results {
		if got.status != http.StatusUnauthorized {
			t.Fatalf("concurrent %s response = %d, want 401", got.source, got.status)
		}
		if got.source == "attacker" {
			attackerResponses++
		} else {
			legitimateResponses++
		}
	}
	if attackerResponses != pairSourceAttemptLimit || legitimateResponses != 1 {
		t.Fatalf("concurrent response counts attacker/legitimate = %d/%d, want %d/1",
			attackerResponses, legitimateResponses, pairSourceAttemptLimit)
	}

	// A's ports varied, but all attempts share one IP bucket and lock only A.
	if got := pairHandlerRequest(s, "192.0.2.10:9999", "654321").Code; got != http.StatusTooManyRequests {
		t.Fatalf("locked attacker response = %d, want 429", got)
	}
	// B has one failure and can still submit the correct code successfully.
	rr := pairHandlerRequest(s, "192.0.2.11:2999", "123456")
	if rr.Code != http.StatusOK {
		t.Fatalf("other source correct-code response = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var paired map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&paired); err != nil {
		t.Fatalf("decode pair response: %v", err)
	}
	if paired["token"] == "" {
		t.Fatal("successful other-source pair response omitted token")
	}
}

func TestPairingGlobalBackstop(t *testing.T) {
	s := New(Config{}, func() State { return State{} }, nil, Commands{}, nil)
	s.mintPairCode = func() (string, error) { return "123456", nil }
	s.EnablePairing()

	for i := 1; i <= pairGlobalAttemptLimit; i++ {
		remote := "198.51.100." + strconv.Itoa(i) + ":1000"
		if got := pairHandlerRequest(s, remote, "654321").Code; got != http.StatusUnauthorized {
			t.Fatalf("distributed failure %d status = %d, want 401", i, got)
		}
	}
	if got := pairHandlerRequest(s, "198.51.100.101:1000", "654321").Code; got != http.StatusTooManyRequests {
		t.Fatalf("invalid code during global backstop = %d, want 429", got)
	}
	if got := pairHandlerRequest(s, "198.51.100.102:1000", "123456").Code; got != http.StatusTooManyRequests {
		t.Fatalf("correct code during global backstop = %d, want 429", got)
	}

	// Explicitly generating a fresh code is the local recovery path and clears
	// the conservative distributed-guess backstop.
	s.EnablePairing()
	if got := pairHandlerRequest(s, "198.51.100.102:1000", "123456").Code; got != http.StatusOK {
		t.Fatalf("correct code after fresh enable = %d, want 200", got)
	}
}

func TestWebRemoteStateConcurrentAccess(t *testing.T) {
	s := New(Config{}, func() State { return State{} }, nil, Commands{}, nil)
	s.mintPairCode = func() (string, error) { return "123456", nil }

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 200; i++ {
			s.EnablePairing()
			s.DisablePairing()
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 200; i++ {
			_ = s.webRemoteEnabled()
			_ = s.sessionAuthRequired()
			_ = s.authMode()
			rr := httptest.NewRecorder()
			s.handleRemote(rr, httptest.NewRequest(http.MethodGet, "/remote", nil))
		}
	}()
	close(start)
	wg.Wait()
}

func TestDisablePairingKeepsLANServerClosed(t *testing.T) {
	s := New(Config{Addr: "0.0.0.0:0", WebRemote: true},
		func() State { return State{} }, nil, Commands{}, nil)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	_, port, err := net.SplitHostPort(s.Addr())
	if err != nil {
		t.Fatalf("addr parse: %v", err)
	}
	s.DisablePairing()
	if code := getWith(t, "http://127.0.0.1:"+port+"/status", nil); code != http.StatusUnauthorized {
		t.Fatalf("no-session LAN status after DisablePairing = %d, want 401", code)
	}
}

// TestPairingFlow covers the complete web-remote pairing lifecycle.
func TestPairingFlow(t *testing.T) {
	var played int
	s := New(Config{Addr: "127.0.0.1:0", WebRemote: true},
		func() State { return State{State: "stopped"} },
		nil,
		Commands{PlayPause: func() { played++ }},
		nil)
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	base := "http://" + s.Addr()

	// 1. With WebRemote enabled at New, GET /remote serves the SPA even before
	// a pairing code is active (the SPA presents the pairing UI; /pair will
	// report "not active" until EnablePairing).
	resp, err := http.Get(base + "/remote")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/remote (web remote on, before enable) = %d, want 200", resp.StatusCode)
	}

	// 2. No session accepted when pairing is off.
	if code := getWith(t, base+"/status", map[string]string{"X-OpenDeezer-Session": "deadbeef"}); code != http.StatusUnauthorized {
		t.Fatalf("invalid session (pairing off) = %d, want 401", code)
	}

	// 3. Enable pairing: get a 6-digit code.
	pairCode := s.EnablePairing()
	if len(pairCode) != 6 {
		t.Fatalf("pair code len = %d, want 6", len(pairCode))
	}
	for _, ch := range pairCode {
		if ch < '0' || ch > '9' {
			t.Fatalf("pair code %q is not all digits", pairCode)
		}
	}

	// 4. /remote continues to serve the SPA.
	resp, err = http.Get(base + "/remote")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/remote (pairing on) = %d, want 200", resp.StatusCode)
	}

	// 5. Wrong code → 401.
	wrongCode := "000001"
	if pairCode == wrongCode {
		wrongCode = "000002"
	}
	req, _ := http.NewRequest(http.MethodPost, base+"/pair?code="+wrongCode, nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong code = %d, want 401", resp.StatusCode)
	}

	// 6. Correct code → 200 + token.
	body, _ := json.Marshal(map[string]string{"code": pairCode})
	req2, _ := http.NewRequest(http.MethodPost, base+"/pair", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("correct code = %d, want 200", resp2.StatusCode)
	}
	var pairResp map[string]string
	if err := json.NewDecoder(resp2.Body).Decode(&pairResp); err != nil {
		t.Fatal(err)
	}
	sessToken := pairResp["token"]
	if len(sessToken) == 0 {
		t.Fatal("pair response missing token")
	}

	// After successful pair the code is single-use (pairEnabled=false), but
	// /remote must still serve (gated on persistent webRemote) so the just-paired
	// SPA can reload and a second device can load the SPA.
	resp, err = http.Get(base + "/remote")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/remote after successful pair = %d, want 200 (SPA must remain available while web remote enabled)", resp.StatusCode)
	}

	// 7. Session token in X-OpenDeezer-Session authorizes GET /status.
	if code := getWith(t, base+"/status", map[string]string{"X-OpenDeezer-Session": sessToken}); code != http.StatusOK {
		t.Fatalf("valid session on /status = %d, want 200", code)
	}

	// 7b. No session, no Origin (e.g. a curl/script on the LAN) MUST be rejected.
	// Web-remote mode requires pairing; it must never fall through to open "none"
	// mode just because noBrowser lets a header-less request pass.
	if code := getWith(t, base+"/status", nil); code != http.StatusUnauthorized {
		t.Fatalf("no-session no-Origin /status = %d, want 401 (web remote must require pairing)", code)
	}
	if code := postWith(t, base+"/playpause", nil); code != http.StatusUnauthorized {
		t.Fatalf("no-session no-Origin /playpause = %d, want 401 (web remote must require pairing)", code)
	}

	// 8. Origin + valid session → POST mutation is allowed (SPA's CSRF-safe path).
	code8 := postWith(t, base+"/playpause", map[string]string{
		"Origin":               "http://192.168.1.42:7654",
		"X-OpenDeezer-Session": sessToken,
	})
	if code8 != http.StatusOK {
		t.Fatalf("Origin + valid session on /playpause = %d, want 200", code8)
	}
	if played != 1 {
		t.Fatalf("playpause ran %d times after Origin+session, want 1", played)
	}

	// 9. Origin without a session → 403 (CSRF blocked, as before).
	code9 := postWith(t, base+"/playpause", map[string]string{
		"Origin": "https://evil.example",
	})
	if code9 != http.StatusForbidden {
		t.Fatalf("Origin without session = %d, want 403", code9)
	}

	// 10. Origin + invalid session → 403 (from noBrowser before auth).
	code10 := postWith(t, base+"/playpause", map[string]string{
		"Origin":               "http://192.168.1.42:7654",
		"X-OpenDeezer-Session": "notavalidtoken",
	})
	if code10 != http.StatusForbidden {
		t.Fatalf("Origin + invalid session = %d, want 403", code10)
	}

	// 11. Expired session → 401.
	s.injectSession("expiredtok0123456789abcdef", time.Now().Add(-time.Hour))
	if code := getWith(t, base+"/status", map[string]string{"X-OpenDeezer-Session": "expiredtok0123456789abcdef"}); code != http.StatusUnauthorized {
		t.Fatalf("expired session = %d, want 401", code)
	}

	// 12. After DisablePairing (which clears webRemote), /remote returns 404 again;
	// existing sessions are now revoked.
	s.DisablePairing()
	resp3, err := http.Get(base + "/remote")
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Fatalf("/remote after DisablePairing = %d, want 404", resp3.StatusCode)
	}
	// Sessions are revoked on DisablePairing.
	if code := getWith(t, base+"/status", map[string]string{"X-OpenDeezer-Session": sessToken}); code != http.StatusUnauthorized {
		t.Fatalf("session after DisablePairing = %d, want 401 (sessions now revoked on disable)", code)
	}

	// 13. Rate limiting: re-enable and hammer with wrong codes.
	_ = s.EnablePairing()
	for i := 0; i < 5; i++ {
		req, _ := http.NewRequest(http.MethodPost, base+"/pair?code=000000", nil)
		resp, _ := http.DefaultClient.Do(req)
		resp.Body.Close()
	}
	req, _ = http.NewRequest(http.MethodPost, base+"/pair?code=000000", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("rate limit = %d, want 429", resp.StatusCode)
	}
}

// TestSameAccountAcceptsAccountIdOnNonLoopback (LAN Connect / wildcard case).
// Matching X-OpenDeezer-Account is accepted even on non-loopback bind (the
// LAN-trust tradeoff); wrong id is still rejected. Pairing for sessions remains
// available on top.
func TestSameAccountAcceptsAccountIdOnNonLoopback(t *testing.T) {
	acct := func() Account { return Account{UserID: "42", Name: "me"} }
	s := New(Config{Addr: "0.0.0.0:0", SameAccountOnly: true},
		func() State { return State{} },
		acct,
		Commands{}, nil)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	// Derive a connectable base (127.0.0.1 works for a wildcard bind).
	_, port, err := net.SplitHostPort(s.Addr())
	if err != nil {
		t.Fatalf("addr parse: %v", err)
	}
	base := "http://127.0.0.1:" + port

	// Correct account id is accepted on non-loopback (for LAN Connect peers).
	if code := getWith(t, base+"/status", map[string]string{"X-OpenDeezer-Account": "42"}); code != http.StatusOK {
		t.Fatalf("matching account id on non-loopback = %d, want 200", code)
	}
	// Wrong account id is rejected.
	if code := getWith(t, base+"/status", map[string]string{"X-OpenDeezer-Account": "999"}); code != http.StatusUnauthorized {
		t.Fatalf("wrong account id on non-loopback = %d, want 401", code)
	}

	// Pairing must be used to obtain session.
	pairCode := s.EnablePairing()
	body, _ := json.Marshal(map[string]string{"code": pairCode})
	req, _ := http.NewRequest(http.MethodPost, base+"/pair", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pair on nonloop sameacct = %d, want 200", resp.StatusCode)
	}
	var pr map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		t.Fatal(err)
	}
	sessTok := pr["token"]
	if sessTok == "" {
		t.Fatal("no token from pair")
	}

	// Valid session grants access (no need for account header).
	if code := getWith(t, base+"/status", map[string]string{"X-OpenDeezer-Session": sessTok}); code != http.StatusOK {
		t.Fatalf("session on nonloop sameacct = %d, want 200", code)
	}
}

// TestPairingCodeSingleUse verifies the code becomes invalid after one success.
func TestPairingCodeSingleUse(t *testing.T) {
	s := New(Config{Addr: "127.0.0.1:0", WebRemote: true},
		func() State { return State{} }, nil, Commands{}, nil)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	base := "http://" + s.Addr()

	pairCode := s.EnablePairing()
	if pairCode == "" {
		t.Fatal("no code")
	}

	// Successful pair.
	body, _ := json.Marshal(map[string]string{"code": pairCode})
	req, _ := http.NewRequest(http.MethodPost, base+"/pair", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first pair = %d", resp.StatusCode)
	}

	// Code should be cleared (single-use).
	if s.PairingCode() != "" {
		t.Fatalf("pairing code not cleared after use")
	}

	// Reuse of same code must fail.
	req2, _ := http.NewRequest(http.MethodPost, base+"/pair", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized && resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("reuse code = %d, want 401/404", resp2.StatusCode)
	}
}

// TestPairingLockoutAfterFailedAttempts verifies lockout after N fails.
func TestPairingLockoutAfterFailedAttempts(t *testing.T) {
	s := New(Config{Addr: "127.0.0.1:0", WebRemote: true},
		func() State { return State{} }, nil, Commands{}, nil)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	base := "http://" + s.Addr()

	_ = s.EnablePairing()
	for i := 0; i < 5; i++ {
		req, _ := http.NewRequest(http.MethodPost, base+"/pair", bytes.NewReader([]byte(`{"code":"000000"}`)))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := http.DefaultClient.Do(req)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("fail %d = %d, want 401", i, resp.StatusCode)
		}
	}
	// Next attempt (6th) must be locked out.
	req, _ := http.NewRequest(http.MethodPost, base+"/pair", bytes.NewReader([]byte(`{"code":"000000"}`)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("after 5 fails = %d, want 429", resp.StatusCode)
	}
}

// TestSessionRevocation covers per-device id revocation and account switch.
func TestSessionRevocation(t *testing.T) {
	uid := "42"
	acct := func() Account { return Account{UserID: uid} }
	s := New(Config{Addr: "127.0.0.1:0", SameAccountOnly: true},
		func() State { return State{} },
		acct, Commands{}, nil)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	base := "http://" + s.Addr()

	// Pair to get a session (pairing works in same-account too).
	pcode := s.EnablePairing()
	body, _ := json.Marshal(map[string]string{"code": pcode})
	req, _ := http.NewRequest(http.MethodPost, base+"/pair", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pair = %d", resp.StatusCode)
	}
	// Use inject + Revoke for the revocation tests below (pair body already consumed).
	// First test manual revoke using inject + Revoke by token (supported by RevokeSession).
	s.injectSession("revoketok1234567890abcdef", time.Now().Add(1*time.Hour))
	// manually set an id for it (tests can reach private via other? use Revoke by token too)
	s.RevokeSession("revoketok1234567890abcdef")
	if code := getWith(t, base+"/status", map[string]string{"X-OpenDeezer-Session": "revoketok1234567890abcdef"}); code != http.StatusUnauthorized {
		t.Fatalf("revoke by token = %d, want 401", code)
	}

	// Now test account switch revokes: first Enable to record pairedAccount, then inject.
	_ = s.EnablePairing() // records pairedAccount="42"
	s.injectSession("switchtok1234567890abcdef", time.Now().Add(1*time.Hour))
	if code := getWith(t, base+"/status", map[string]string{"X-OpenDeezer-Session": "switchtok1234567890abcdef"}); code != http.StatusOK {
		t.Fatalf("pre-switch session = %d, want 200", code)
	}
	uid = "99" // switch account
	if code := getWith(t, base+"/status", map[string]string{"X-OpenDeezer-Session": "switchtok1234567890abcdef"}); code != http.StatusUnauthorized {
		t.Fatalf("post-switch session = %d, want 401 (account switch revokes)", code)
	}
}
