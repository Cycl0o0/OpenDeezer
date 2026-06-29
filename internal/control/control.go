// Package control exposes playback control + status over a small HTTP/JSON API.
// It is the shared foundation for remote control (one OpenDeezer client driving
// another) and the MCP server (an AI agent driving playback). A frontend wires
// it like the MPRIS bridge: provide a status snapshot func + a set of command
// callbacks, plus the Deezer client for read-only browse (search/playlists).
//
// Auth has three modes, picked by Config. Credentials are accepted via request
// HEADERS only (never the query string, which leaks into logs/history):
//   - Token: a bearer token in "X-OpenDeezer-Token". Strongest.
//   - Same-account: no token, but a controller must prove it is logged into the
//     SAME Deezer account by sending its OWN Deezer user id in
//     "X-OpenDeezer-Account". A controller learns that id from its own login, not
//     from this server — /whoami deliberately does NOT echo the user id, so a
//     bystander can't read the credential and replay it. Convenience auth for a
//     trusted LAN: the user copies no token; their own devices just connect.
//     The user id is only semi-private, so this is LAN-trust grade, not a secret.
//   - Session (web remote): a phone pairs with a 6-digit code minted at enable
//     time; on success it receives a short-lived session token sent as
//     X-OpenDeezer-Session. CSRF-safe because the token lives in localStorage (not
//     a cookie) and custom headers cannot be set cross-origin.
//   - None: open (only safe bound to localhost).
//
// Mutating endpoints require POST and reject requests carrying a browser Origin
// header, so a web page the user happens to visit can't drive playback (CSRF).
// The exception is requests that also carry a valid X-OpenDeezer-Session token:
// those come from our own SPA (same origin in the browser), so they are allowed.
// GET /whoami is unauthenticated so a controller can discover the account NAME
// (not id) and auth mode of a server before connecting.
package control

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Cycl0o0/OpenDeezer/internal/deezer"
)

// Track is a now-playing / queue entry in the API.
type Track struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	ArtistID   string `json:"artistId,omitempty"`
	Album      string `json:"album"`
	Explicit   bool   `json:"explicit"`
	DurationMS int64  `json:"durationMs"`
	ArtworkURL string `json:"artworkUrl,omitempty"`
}

// State is the playback snapshot returned by GET /status.
type State struct {
	State      string  `json:"state"` // playing | paused | stopped | loading | error
	Track      *Track  `json:"track,omitempty"`
	PositionMS int64   `json:"positionMs"`
	DurationMS int64   `json:"durationMs"`
	Volume     float64 `json:"volume"` // 0..1
	Repeat     string  `json:"repeat"` // off | all | one
	Shuffle    bool    `json:"shuffle"`
	Format     string  `json:"format,omitempty"`
	Queue      []Track `json:"queue,omitempty"`
}

// Commands are the mutating actions a controller exposes (each may be nil).
type Commands struct {
	PlayPause     func()
	Next          func()
	Prev          func()
	Stop          func()
	Restart       func() // seek to 0
	CycleRepeat   func()
	ToggleShuffle func()
	SetRepeat     func(mode string) // mode: "off"|"all"|"one" (SET variant)
	SetShuffle    func(on bool)     // on: true/false (SET variant)
	Seek          func(ms int64)
	SetVolume     func(v float64)
	PlayTrack     func(id string)
	PlayPlaylist  func(id string)
}

// Config configures the control server.
type Config struct {
	Addr            string // host:port ("127.0.0.1:7654" localhost, ":7654" LAN)
	Token           string // bearer token; "" disables token auth
	SameAccountOnly bool   // when Token=="", require a matching Deezer account id
	WebRemote       bool   // allow LAN bind with session (pairing) as the sole auth
}

// Account is the controlled client's Deezer identity, supplied by a snapshot
// provider so the HTTP goroutine never reads the deezer.Client's login fields
// directly (those are written by Login on another goroutine).
type Account struct {
	UserID string
	Name   string
	Offer  string
}

// Whoami is the unauthenticated identity returned by GET /whoami. It carries the
// account display NAME (for the controller to recognise its own device) but never
// the user id: in same-account mode that id IS the credential, so echoing it here
// would let any bystander read and replay it.
type Whoami struct {
	Name    string `json:"name"`
	Offer   string `json:"offer,omitempty"`
	Auth    string `json:"auth"`              // token | account | session | none
	Version string `json:"version,omitempty"` // OpenDeezer version
	Client  string `json:"client,omitempty"`  // client/platform id (tui, macos, gnome…)
	Device  string `json:"device,omitempty"`  // human device label ("OpenDeezer TUI")
}

// Server serves the control API.
type Server struct {
	status      func() State
	account     func() Account // identity snapshot (auth + /whoami)
	cmds        Commands
	client      *deezer.Client
	token       string
	sameAccount bool
	webRemote   bool // LAN-bind allowed; session token is the auth
	addr        string
	version     string
	clientID    string // client/platform id (tui, macos, …)
	device      string // human device label
	srv         *http.Server
	ln          net.Listener

	// pairing + session state (all guarded by pairMu)
	pairMu           sync.Mutex
	pairEnabled      bool
	pairCode         string
	sessions         map[string]time.Time // hex token → expiry
	pairAttempts     int
	pairAttemptReset time.Time
}

//go:embed webui/remote.html
var remoteHTML []byte

// New builds a control server from cfg. status + account are snapshot providers
// (called from the HTTP goroutine, so they must be race-free reads); client
// supplies the browse endpoints (search/playlists).
func New(cfg Config, status func() State, account func() Account, cmds Commands, client *deezer.Client) *Server {
	return &Server{
		status: status, account: account, cmds: cmds, client: client,
		token: cfg.Token, sameAccount: cfg.SameAccountOnly, webRemote: cfg.WebRemote,
		addr:     cfg.Addr,
		sessions: make(map[string]time.Time),
	}
}

// SetVersion records the app version reported by /whoami.
func (s *Server) SetVersion(v string) { s.version = v }

// SetClientInfo records the client/platform id + device label for /whoami.
func (s *Server) SetClientInfo(client, device string) { s.clientID, s.device = client, device }

// Addr returns the actual listen address (valid after Start).
func (s *Server) Addr() string {
	if s.ln != nil {
		return s.ln.Addr().String()
	}
	return s.addr
}

// EnablePairing mints a fresh 6-digit code, activates pairing, and returns the
// code. Safe to call multiple times; each call resets the code.
func (s *Server) EnablePairing() string {
	code, _ := mintCode()
	s.pairMu.Lock()
	s.pairEnabled = true
	s.pairCode = code
	s.pairAttempts = 0
	s.pairAttemptReset = time.Time{}
	s.pairMu.Unlock()
	return code
}

// DisablePairing clears the pairing code so no new phones can pair. Existing
// valid session tokens remain usable for their remaining TTL.
func (s *Server) DisablePairing() {
	s.pairMu.Lock()
	s.pairEnabled = false
	s.pairCode = ""
	s.pairMu.Unlock()
}

// PairingActive reports whether pairing is currently enabled.
func (s *Server) PairingActive() bool {
	s.pairMu.Lock()
	defer s.pairMu.Unlock()
	return s.pairEnabled
}

// PairingCode returns the current 6-digit code (empty when not active).
func (s *Server) PairingCode() string {
	s.pairMu.Lock()
	defer s.pairMu.Unlock()
	return s.pairCode
}

// Start binds the port and serves in a background goroutine.
func (s *Server) Start() error {
	// Fail closed: never serve unauthenticated ("none" mode) on a non-loopback
	// address — a config mistake (e.g. OPENDEEZER_CONTROL_SAMEACCOUNT=0 on a LAN
	// bind) must not silently expose playback + private playlists to the LAN.
	// Web-remote mode is exempt: the pairing code IS the auth for that path.
	if s.token == "" && !s.sameAccount && !s.webRemote && !isLoopbackAddr(s.addr) {
		return errors.New("control: refusing to serve unauthenticated on a non-loopback address; " +
			"set OPENDEEZER_CONTROL_TOKEN or keep same-account auth enabled")
	}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.ln = ln
	mux := http.NewServeMux()
	s.routes(mux)
	// Conservative timeouts + a small header cap: this can be LAN-exposed, so
	// bound every phase of a request to resist slowloris / resource exhaustion.
	s.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10, // 16 KiB
	}
	go func() { _ = s.srv.Serve(ln) }()
	return nil
}

// Close stops the server.
func (s *Server) Close() {
	if s.srv != nil {
		_ = s.srv.Close()
	}
}

func (s *Server) routes(mux *http.ServeMux) {
	// Web remote: serve the SPA (no auth; gated by pairEnabled) and the pair endpoint.
	mux.HandleFunc("/remote", s.handleRemote)
	mux.HandleFunc("/pair", s.requireMethod(http.MethodPost, s.handlePair))
	// GET, unauthenticated: identity/discovery (name + auth mode only).
	mux.HandleFunc("/whoami", s.get(s.handleWhoami, false))
	// GET, authenticated: reads.
	mux.HandleFunc("/status", s.get(s.handleStatus, true))
	mux.HandleFunc("/playlists", s.get(s.handlePlaylists, true))
	mux.HandleFunc("/search", s.get(s.handleSearch, true))
	// POST, authenticated, CSRF-guarded: mutations.
	mux.HandleFunc("/playpause", s.post(s.act(func() { call(s.cmds.PlayPause) })))
	mux.HandleFunc("/next", s.post(s.act(func() { call(s.cmds.Next) })))
	mux.HandleFunc("/prev", s.post(s.act(func() { call(s.cmds.Prev) })))
	mux.HandleFunc("/stop", s.post(s.act(func() { call(s.cmds.Stop) })))
	mux.HandleFunc("/restart", s.post(s.act(func() { call(s.cmds.Restart) })))
	mux.HandleFunc("/repeat", s.post(s.handleRepeat))
	mux.HandleFunc("/shuffle", s.post(s.handleShuffle))
	mux.HandleFunc("/seek", s.post(s.handleSeek))
	mux.HandleFunc("/volume", s.post(s.handleVolume))
	mux.HandleFunc("/play/track", s.post(s.handlePlayTrack))
	mux.HandleFunc("/play/playlist", s.post(s.handlePlayPlaylist))
}

func call(fn func()) {
	if fn != nil {
		fn()
	}
}

// get wraps a read handler: GET only, optionally authenticated.
func (s *Server) get(h http.HandlerFunc, authed bool) http.HandlerFunc {
	if authed {
		h = s.auth(h)
	}
	return s.requireMethod(http.MethodGet, h)
}

// post wraps a mutating handler: POST only, CSRF-guarded, authenticated.
func (s *Server) post(h http.HandlerFunc) http.HandlerFunc {
	return s.requireMethod(http.MethodPost, s.noBrowser(s.auth(h)))
}

func (s *Server) requireMethod(method string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
}

// noBrowser rejects requests carrying a browser Origin header. A native
// controller / MCP client never sends one, but a web page does — this blocks
// drive-by CSRF that would otherwise reach the no-auth localhost mode via a
// simple cross-origin POST.
//
// Exception: a request that also carries a valid X-OpenDeezer-Session token is
// allowed through. The session token lives in the phone's localStorage; cross-
// site JS cannot read another origin's localStorage nor set custom headers
// cross-origin (CORS blocks that), so a valid session header is proof the
// request originates from our own SPA on the same device.
func (s *Server) noBrowser(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			sessTok := r.Header.Get("X-OpenDeezer-Session")
			if !s.hasValidSession(sessTok) {
				http.Error(w, `{"error":"cross-origin requests are not allowed"}`, http.StatusForbidden)
				return
			}
		}
		h(w, r)
	}
}

// auth enforces the configured auth mode. Credentials come from headers only.
// A valid X-OpenDeezer-Session token (web remote) is checked first and always
// grants access regardless of the server's base auth mode.
func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Session token (web remote): checked before other auth modes so a paired
		// phone can reach the API even when the server uses token or account auth.
		if sessTok := r.Header.Get("X-OpenDeezer-Session"); sessTok != "" {
			if s.hasValidSession(sessTok) {
				h(w, r)
				return
			}
			// Session header present but invalid/expired → reject; do not fall
			// through to other auth modes (the phone should re-pair).
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		switch {
		case s.token != "":
			tok := r.Header.Get("X-OpenDeezer-Token")
			// Constant-time compare: the token is a real secret.
			if subtle.ConstantTimeCompare([]byte(tok), []byte(s.token)) != 1 {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
		case s.sameAccount:
			want := s.accountID()
			got := r.Header.Get("X-OpenDeezer-Account")
			// Constant-time compare (defense-in-depth; the id is only semi-secret).
			if want == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
				http.Error(w, `{"error":"account mismatch"}`, http.StatusUnauthorized)
				return
			}
		case s.webRemote:
			// Web-remote mode: a valid session token is mandatory. A valid session
			// already returned above; reaching here means no/invalid session, so
			// reject — never fall through to open "none" mode. Without this a
			// non-browser LAN client (no Origin header, so noBrowser lets it pass)
			// could drive playback and read private playlists without ever pairing.
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

// hasValidSession reports whether tok is a currently-valid session token.
// All comparisons are constant-time to prevent timing oracle on token values.
func (s *Server) hasValidSession(tok string) bool {
	if tok == "" {
		return false
	}
	s.pairMu.Lock()
	defer s.pairMu.Unlock()
	now := time.Now()
	found := false
	for stored, exp := range s.sessions {
		// Constant-time compare even if the token isn't in the map (timing leak
		// on map presence is negligible vs 64-char entropy, but we do it right).
		if subtle.ConstantTimeCompare([]byte(tok), []byte(stored)) == 1 && now.Before(exp) {
			found = true
		}
	}
	return found
}

// reapSessions removes expired session tokens. Must be called with pairMu held.
func (s *Server) reapSessions() {
	now := time.Now()
	for tok, exp := range s.sessions {
		if now.After(exp) {
			delete(s.sessions, tok)
		}
	}
}

// injectSession plants a session with the given expiry. Used in tests.
func (s *Server) injectSession(tok string, exp time.Time) {
	s.pairMu.Lock()
	s.sessions[tok] = exp
	s.pairMu.Unlock()
}

// handleRemote serves the embedded web remote SPA. Only active when pairing is
// enabled; returns 404 otherwise. No auth — the SPA itself enforces pairing.
func (s *Server) handleRemote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	s.pairMu.Lock()
	enabled := s.pairEnabled
	s.pairMu.Unlock()
	if !enabled {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(remoteHTML)
}

// handlePair handles POST /pair: validates the 6-digit code and, on success,
// issues a session token. Rate-limited to ~5 attempts per minute.
func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	// Read code from query string first, then JSON body. Do this outside the lock
	// to avoid holding it across I/O.
	code := r.URL.Query().Get("code")
	if code == "" {
		var body struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body)
		code = body.Code
	}

	s.pairMu.Lock()
	defer s.pairMu.Unlock()

	if !s.pairEnabled {
		http.Error(w, `{"error":"pairing not active"}`, http.StatusNotFound)
		return
	}

	// Rate limiting: allow ~5 wrong attempts per minute, then cool down.
	now := time.Now()
	if now.After(s.pairAttemptReset) {
		s.pairAttempts = 0
		s.pairAttemptReset = now.Add(time.Minute)
	}
	if s.pairAttempts >= 5 {
		http.Error(w, `{"error":"too many attempts, try again later"}`, http.StatusTooManyRequests)
		return
	}

	// Constant-time compare to prevent timing oracle on the 6-digit code.
	if subtle.ConstantTimeCompare([]byte(code), []byte(s.pairCode)) != 1 {
		s.pairAttempts++
		http.Error(w, `{"error":"invalid code"}`, http.StatusUnauthorized)
		return
	}

	// Success: mint a session token, reap old ones, reset attempt counter.
	tok, err := mintToken()
	if err != nil {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	s.reapSessions()
	s.sessions[tok] = now.Add(12 * time.Hour)
	s.pairAttempts = 0

	writeJSON(w, map[string]string{"token": tok})
}

// isLoopbackAddr reports whether a host:port binds only the loopback interface.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	switch host {
	case "", "0.0.0.0", "::":
		return false // wildcard = all interfaces
	case "localhost":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// accountID is our logged-in Deezer user id ("" if unknown / not logged in).
func (s *Server) accountID() string {
	if s.account == nil {
		return ""
	}
	return s.account().UserID
}

// authMode reports the active auth mode for /whoami.
func (s *Server) authMode() string {
	switch {
	case s.token != "":
		return "token"
	case s.sameAccount:
		return "account"
	case s.webRemote:
		return "session"
	default:
		return "none"
	}
}

func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	who := Whoami{Auth: s.authMode(), Version: s.version, Client: s.clientID, Device: s.device}
	if s.account != nil {
		a := s.account()
		who.Name, who.Offer = a.Name, a.Offer // never the user id (it's the credential)
	}
	writeJSON(w, who)
}

// act returns a handler that runs fn then replies with the status snapshot.
//
// NOTE: commands are dispatched asynchronously onto the frontend's update loop,
// so the snapshot returned here reflects state as of the request — it may not yet
// show the just-issued change (it lands within one tick). Clients that need the
// post-command state should poll GET /status.
func (s *Server) act(fn func()) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fn()
		writeJSON(w, s.status())
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.status()) }

func (s *Server) handleSeek(w http.ResponseWriter, r *http.Request) {
	ms, err := strconv.ParseInt(r.URL.Query().Get("ms"), 10, 64)
	if err != nil {
		http.Error(w, `{"error":"ms required"}`, http.StatusBadRequest)
		return
	}
	if s.cmds.Seek != nil {
		s.cmds.Seek(ms)
	}
	writeJSON(w, s.status())
}

func (s *Server) handleVolume(w http.ResponseWriter, r *http.Request) {
	v, err := strconv.ParseFloat(r.URL.Query().Get("v"), 64)
	if err != nil {
		http.Error(w, `{"error":"v (0..1) required"}`, http.StatusBadRequest)
		return
	}
	if s.cmds.SetVolume != nil {
		s.cmds.SetVolume(v)
	}
	writeJSON(w, s.status())
}

func (s *Server) handlePlaylists(w http.ResponseWriter, r *http.Request) {
	if s.client == nil {
		http.Error(w, `{"error":"not available"}`, http.StatusServiceUnavailable)
		return
	}
	ps, err := s.client.Playlists()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"playlists": ps})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if s.client == nil {
		http.Error(w, `{"error":"not available"}`, http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query().Get("q")
	if q == "" {
		http.Error(w, `{"error":"q required"}`, http.StatusBadRequest)
		return
	}
	res, err := s.client.Search(q)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, res)
}

func (s *Server) handlePlayTrack(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
		return
	}
	if s.cmds.PlayTrack != nil {
		s.cmds.PlayTrack(id)
	}
	writeJSON(w, s.status())
}

func (s *Server) handlePlayPlaylist(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
		return
	}
	if s.cmds.PlayPlaylist != nil {
		s.cmds.PlayPlaylist(id)
	}
	writeJSON(w, s.status())
}

// handleRepeat handles POST /repeat. With ?mode=off|all|one it SETS repeat
// (via SetRepeat); with no param it cycles via CycleRepeat (legacy behaviour).
func (s *Server) handleRepeat(w http.ResponseWriter, r *http.Request) {
	if mode := r.URL.Query().Get("mode"); mode != "" {
		if s.cmds.SetRepeat != nil {
			s.cmds.SetRepeat(mode)
		}
	} else {
		call(s.cmds.CycleRepeat)
	}
	writeJSON(w, s.status())
}

// handleShuffle handles POST /shuffle. With ?on=true|false it SETS shuffle
// (via SetShuffle); with no param it toggles via ToggleShuffle (legacy behaviour).
func (s *Server) handleShuffle(w http.ResponseWriter, r *http.Request) {
	if on := r.URL.Query().Get("on"); on != "" {
		if s.cmds.SetShuffle != nil {
			s.cmds.SetShuffle(on == "true")
		}
	} else {
		call(s.cmds.ToggleShuffle)
	}
	writeJSON(w, s.status())
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// mintCode returns a cryptographically random 6-digit numeric code (zero-padded).
func mintCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// mintToken returns a cryptographically random 32-byte session token as hex.
func mintToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
