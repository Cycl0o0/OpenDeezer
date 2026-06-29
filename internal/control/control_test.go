package control

import (
	"bytes"
	"encoding/json"
	"net/http"
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

	// 1. Pairing not yet enabled → GET /remote returns 404.
	resp, err := http.Get(base + "/remote")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("/remote (pairing off) = %d, want 404", resp.StatusCode)
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

	// 4. /remote now serves the SPA.
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
		"Origin":                 "http://192.168.1.42:7654",
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
		"Origin":                 "http://192.168.1.42:7654",
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

	// 12. After DisablePairing, /remote returns 404 again; valid sessions still work.
	s.DisablePairing()
	resp3, err := http.Get(base + "/remote")
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Fatalf("/remote after DisablePairing = %d, want 404", resp3.StatusCode)
	}
	// The session minted before disabling is still valid.
	if code := getWith(t, base+"/status", map[string]string{"X-OpenDeezer-Session": sessToken}); code != http.StatusOK {
		t.Fatalf("session after DisablePairing = %d, want 200 (sessions survive disable)", code)
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
