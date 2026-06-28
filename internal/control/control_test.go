package control

import (
	"encoding/json"
	"net/http"
	"testing"
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
