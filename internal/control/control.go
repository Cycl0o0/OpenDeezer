// Package control exposes playback control + status over a small HTTP/JSON API.
// It is the shared foundation for remote control (one OpenDeezer client driving
// another) and the MCP server (an AI agent driving playback). A frontend wires
// it like the MPRIS bridge: provide a status snapshot func + a set of command
// callbacks, plus the Deezer client for read-only browse (search/playlists).
//
// Auth has three modes, picked by Config. Credentials are accepted via request
// HEADERS only (never the query string, which leaks into logs/history), except
// that GET /events alone accepts ?session= for the browser EventSource API,
// which cannot set X-OpenDeezer-Session:
//   - Token: a bearer token in "X-OpenDeezer-Token". Strongest.
//   - Same-account: no token, but a controller must prove it is logged into the
//     SAME Deezer account by sending its OWN Deezer user id in
//     "X-OpenDeezer-Account". This is accepted directly from loopback clients
//     (backward compat) and from LAN peers (for token-less same-account Connect
//     and the LAN-trust tradeoff); the plaintext id is accepted on non-loopback
//     binds. Pairing via short-TTL single-use 6-digit code may still be used to
//     obtain a per-device session token (X-OpenDeezer-Session) instead.
//   - Session (web remote): a phone pairs with a 6-digit code minted at enable
//     time; on success it receives a short-lived session token sent as
//     X-OpenDeezer-Session. The code is now single-use + time-limited; failed
//     verifies have lockout. Per-device ids allow individual revocation.
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
	"math"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/deezer"
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

	// Sleep timer (0/false when not armed).
	SleepActive      bool  `json:"sleepActive,omitempty"`
	SleepEndOfTrack  bool  `json:"sleepEndOfTrack,omitempty"`
	SleepRemainingMS int64 `json:"sleepRemainingMs,omitempty"`
}

// Commands are the control callbacks a controller exposes (each may be nil).
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

	// Optional extended-control callbacks return an error for a well-formed
	// request that cannot be applied to the current queue/player state. The HTTP
	// handlers expose those failures as 409 Conflict; nil means unsupported.
	QueueAdd      func(id string, next bool) error
	QueueJump     func(index int) error
	QueueRemove   func(index int) error
	QueueMove     func(from, to int) error
	PlayAlbum     func(id string) error
	PlayMixTrack  func(id string) error
	PlayMixArtist func(id string) error
	HistoryRecent func(n int) (json.RawMessage, error)

	SetSleepTimer    func(minutes int, endOfTrack bool) // arm the sleep timer
	CancelSleepTimer func()                             // disarm it
}

// EQState is the equalizer snapshot returned by GET /eq (same wire shape as
// corelib's DZEQJSON / odmobile's EQJSON, so every controller renders one UI).
type EQState struct {
	Enabled  bool      `json:"enabled"`
	Mono     bool      `json:"mono"`
	PreampDB float64   `json:"preampDb"`
	GainsDB  []float64 `json:"gainsDb"`
	Preset   string    `json:"preset"`
	Bands    []float64 `json:"bands"`
	Presets  []string  `json:"presets"`
}

// EQ is the optional equalizer bridge; when nil the /eq endpoints 404. Hosts
// wire the funcs to the audio player (each may be nil individually).
type EQ struct {
	State      func() EQState
	SetEnabled func(on bool)
	SetMono    func(on bool)
	SetPreamp  func(db float64)
	SetPreset  func(name string) error
	SetBand    func(band int, db float64) error
}

// EQController is the player subset the /eq endpoint drives; *audio.Player
// satisfies it (declared here so this package needn't import the engine).
type EQController interface {
	EQEnabled() bool
	MonoDownmix() bool
	EQPreampDB() float64
	EQGains() []float64
	EQPreset() string
	EQBands() []float64
	SetEQEnabled(on bool)
	SetMonoDownmix(on bool)
	SetEQPreamp(db float64)
	SetEQPreset(name string) error
	SetEQGain(band int, db float64) error
}

// PlayerEQ builds the /eq bridge from a live player getter (get may return nil
// while the engine is starting) and the core's preset-name list.
func PlayerEQ(get func() EQController, presets []string) *EQ {
	return &EQ{
		State: func() EQState {
			p := get()
			if p == nil {
				return EQState{Preset: "flat", Presets: presets}
			}
			return EQState{
				Enabled: p.EQEnabled(), Mono: p.MonoDownmix(), PreampDB: p.EQPreampDB(),
				GainsDB: p.EQGains(), Preset: p.EQPreset(), Bands: p.EQBands(),
				Presets: presets,
			}
		},
		SetEnabled: func(on bool) {
			if p := get(); p != nil {
				p.SetEQEnabled(on)
			}
		},
		SetMono: func(on bool) {
			if p := get(); p != nil {
				p.SetMonoDownmix(on)
			}
		},
		SetPreamp: func(db float64) {
			if p := get(); p != nil {
				p.SetEQPreamp(db)
			}
		},
		SetPreset: func(name string) error {
			if p := get(); p != nil {
				return p.SetEQPreset(name)
			}
			return errors.New("player not ready")
		},
		SetBand: func(band int, db float64) error {
			if p := get(); p != nil {
				return p.SetEQGain(band, db)
			}
			return errors.New("player not ready")
		},
	}
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

// session holds metadata for a per-device authenticated session token.
type session struct {
	expiry time.Time
	id     string // public identifier for this session (for individual revocation)
}

type pairAttemptState struct {
	attempts     int
	lockoutUntil time.Time
}

const (
	maxSleepMinutes      = 24 * 60
	defaultHistoryRecent = 50
	maxHistoryRecent     = 500

	eventFallbackInterval  = time.Second
	eventKeepaliveInterval = 25 * time.Second
	eventWriteTimeout      = 5 * time.Second
	eventSubscriberBuffer  = 1

	pairSourceAttemptLimit = 5
	pairSourceLockout      = 15 * time.Minute
	pairGlobalAttemptLimit = 100
	pairGlobalWindow       = time.Minute
	pairGlobalLockout      = time.Minute
)

// Server serves the control API.
type Server struct {
	status      func() State
	account     func() Account // identity snapshot (auth + /whoami)
	cmds        Commands
	eq          *EQ // optional equalizer bridge (nil → /eq is 404)
	client      *deezer.Client
	token       string
	sameAccount bool
	addr        string
	version     string
	clientID    string // client/platform id (tui, macos, …)
	device      string // human device label
	srv         *http.Server
	ln          net.Listener

	// State-event subscribers are independent of pairing state. The single
	// background loop serializes non-blocking change prompts; each handler takes
	// and diffs its own fresh snapshots so subscribers cannot observe stale data.
	eventsMu         sync.Mutex
	eventSubscribers map[chan struct{}]struct{}
	eventNotify      chan struct{}
	eventStop        chan struct{}
	eventDone        chan struct{}
	eventLoopStarted bool

	// Pairing, web-remote and session state (all guarded by pairMu).
	pairMu                   sync.Mutex
	webRemote                bool // serve the embedded web remote SPA
	sessionOnly              bool // require a paired session when no stronger auth is configured
	pairEnabled              bool
	pairCode                 string
	pairCodeExpiry           time.Time
	pairCodeGenerationFailed bool
	sessions                 map[string]session // token → {expiry, per-device id}
	pairAttemptsByIP         map[string]pairAttemptState
	pairGlobalAttempts       int
	pairGlobalReset          time.Time
	pairGlobalLockoutUntil   time.Time
	pairedAccount            string // account UserID sessions were created for; switch revokes all

	// Test seam for the otherwise non-deterministic crypto/rand failure path.
	// Production code leaves this set to mintCode after construction.
	mintPairCode func() (string, error)
}

//go:embed webui/remote.html
var remoteHTML []byte

// New builds a control server from cfg. status + account are snapshot providers
// (called from the HTTP goroutine, so they must be race-free reads); client
// supplies the browse endpoints (search/playlists).
func New(cfg Config, status func() State, account func() Account, cmds Commands, client *deezer.Client) *Server {
	return &Server{
		status: status, account: account, cmds: cmds, client: client,
		token: cfg.Token, sameAccount: cfg.SameAccountOnly,
		addr:             cfg.Addr,
		webRemote:        cfg.WebRemote,
		sessionOnly:      cfg.WebRemote,
		sessions:         make(map[string]session),
		pairAttemptsByIP: make(map[string]pairAttemptState),
		mintPairCode:     mintCode,
		eventSubscribers: make(map[chan struct{}]struct{}),
		eventNotify:      make(chan struct{}, 1),
	}
}

// SetVersion records the app version reported by /whoami.
func (s *Server) SetVersion(v string) { s.version = v }

// SetClientInfo records the client/platform id + device label for /whoami.
func (s *Server) SetClientInfo(client, device string) { s.clientID, s.device = client, device }

// SetEQ wires the equalizer bridge (call before Start).
func (s *Server) SetEQ(eq *EQ) { s.eq = eq }

// Addr returns the actual listen address (valid after Start).
func (s *Server) Addr() string {
	if s.ln != nil {
		return s.ln.Addr().String()
	}
	return s.addr
}

// EnablePairing mints a fresh 6-digit code, activates pairing, and returns the
// code. Safe to call multiple times; each call resets the code. The code is
// single-use and short-TTL (5 minutes).
func (s *Server) EnablePairing() string {
	code, err := s.mintPairCode()
	s.pairMu.Lock()
	defer s.pairMu.Unlock()
	s.resetPairRateLimitsLocked()
	if err != nil || code == "" {
		// An empty generated code would compare equal to a missing request code.
		// Invalidate any previous code and remember the entropy failure so /pair
		// can report 500 instead of turning it into an authentication bypass.
		s.pairEnabled = false
		s.pairCode = ""
		s.pairCodeExpiry = time.Time{}
		s.pairCodeGenerationFailed = true
		return ""
	}
	curr := s.accountID()
	if s.pairedAccount != "" && curr != "" && s.pairedAccount != curr {
		s.sessions = make(map[string]session)
		s.pairedAccount = ""
	}
	s.webRemote = true
	s.sessionOnly = true
	s.pairEnabled = true
	s.pairCode = code
	s.pairCodeExpiry = time.Now().Add(5 * time.Minute)
	s.pairCodeGenerationFailed = false
	return code
}

// DisablePairing clears the pairing code so no new phones can pair and revokes
// all existing session tokens (individual sessions can be revoked via their
// per-device id). It also disables the web remote SPA.
func (s *Server) DisablePairing() {
	s.pairMu.Lock()
	s.webRemote = false
	// A server already listening without token/account auth must never become
	// unauthenticated merely because its LAN web remote was disabled. Preserve
	// the historical open-loopback behaviour, but fail closed off-loopback.
	s.sessionOnly = s.token == "" && !s.sameAccount && !isLoopbackAddr(s.addr)
	s.pairEnabled = false
	s.pairCode = ""
	s.pairCodeExpiry = time.Time{}
	s.pairCodeGenerationFailed = false
	s.resetPairRateLimitsLocked()
	s.sessions = make(map[string]session)
	s.pairedAccount = ""
	s.pairMu.Unlock()
}

func (s *Server) webRemoteEnabled() bool {
	s.pairMu.Lock()
	defer s.pairMu.Unlock()
	return s.webRemote
}

func (s *Server) sessionAuthRequired() bool {
	s.pairMu.Lock()
	defer s.pairMu.Unlock()
	return s.sessionOnly
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

// RevokeSession revokes the session by its public identifier (returned as "id"
// from successful /pair) or by its token. Used for per-device revocation and
// by account-switch / DisablePairing logic.
func (s *Server) RevokeSession(idOrToken string) {
	if idOrToken == "" {
		return
	}
	s.pairMu.Lock()
	defer s.pairMu.Unlock()
	for tok, se := range s.sessions {
		if se.id == idOrToken || tok == idOrToken {
			delete(s.sessions, tok)
			return
		}
	}
}

// Start binds the port and serves in a background goroutine.
func (s *Server) Start() error {
	// Fail closed: never serve unauthenticated ("none" mode) on a non-loopback
	// address — a config mistake (e.g. OPENDEEZER_CONTROL_SAMEACCOUNT=0 on a LAN
	// bind) must not silently expose playback + private playlists to the LAN.
	// Web-remote mode is exempt: the pairing code IS the auth for that path.
	if s.token == "" && !s.sameAccount && !s.sessionAuthRequired() && !isLoopbackAddr(s.addr) {
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
		Handler:           s.checkHost(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10, // 16 KiB
	}
	s.startEventLoop()
	go func() { _ = s.srv.Serve(ln) }()
	return nil
}

// Close stops the server.
func (s *Server) Close() {
	if s.srv != nil {
		_ = s.srv.Close()
	}
	s.stopEventLoop()
}

func (s *Server) routes(mux *http.ServeMux) {
	// Web remote: serve the SPA (no auth; gated by webRemote) and the pair endpoint.
	mux.HandleFunc("/remote", s.handleRemote)
	mux.HandleFunc("/pair", s.requireMethod(http.MethodPost, s.handlePair))
	// GET, unauthenticated: identity/discovery (name + auth mode only).
	mux.HandleFunc("/whoami", s.get(s.handleWhoami, false))
	// GET, authenticated: reads.
	mux.HandleFunc("/status", s.get(s.handleStatus, true))
	// EventSource cannot set X-OpenDeezer-Session. This route alone accepts the
	// same session credential as ?session= and then uses the normal auth path.
	mux.HandleFunc("/events", s.requireMethod(http.MethodGet, s.eventSourceSession(s.auth(s.handleEvents))))
	mux.HandleFunc("/playlists", s.get(s.handlePlaylists, true))
	mux.HandleFunc("/search", s.get(s.handleSearch, true))
	mux.HandleFunc("/history/recent", s.get(s.handleHistoryRecent, true))
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
	mux.HandleFunc("/queue/add", s.post(s.handleQueueAdd))
	mux.HandleFunc("/queue/jump", s.post(s.handleQueueJump))
	mux.HandleFunc("/queue/remove", s.post(s.handleQueueRemove))
	mux.HandleFunc("/queue/move", s.post(s.handleQueueMove))
	mux.HandleFunc("/play/track", s.post(s.handlePlayTrack))
	mux.HandleFunc("/play/playlist", s.post(s.handlePlayPlaylist))
	mux.HandleFunc("/play/album", s.post(s.handlePlayAlbum))
	mux.HandleFunc("/play/mix/track", s.post(s.handlePlayMixTrack))
	mux.HandleFunc("/play/mix/artist", s.post(s.handlePlayMixArtist))
	mux.HandleFunc("/sleep", s.post(s.handleSleep))
	if s.eq != nil {
		mux.HandleFunc("/eq", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				s.get(s.handleEQGet, true)(w, r)
				return
			}
			s.post(s.handleEQSet)(w, r)
		})
	}
}

func call(fn func()) {
	if fn != nil {
		fn()
	}
}

// eventSourceSession adapts the browser EventSource API to the existing session
// authentication path. EventSource cannot set request headers, so /events alone
// accepts one non-empty ?session= value and presents it to auth as the normal
// X-OpenDeezer-Session header. Token and account credentials remain header-only.
func (s *Server) eventSourceSession(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-OpenDeezer-Session") == "" {
			token, present, valid := singleQueryValue(r.URL.Query(), "session")
			if present && valid {
				r = r.Clone(r.Context())
				r.Header = r.Header.Clone()
				r.Header.Set("X-OpenDeezer-Session", token)
			}
		}
		h(w, r)
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

// NotifyStateChanged asks the event loop to publish a fresh snapshot. Calls are
// deliberately coalesced: the payload is state, not an edge-triggered event, so
// one pending notification is enough to deliver the newest snapshot.
func (s *Server) NotifyStateChanged() {
	select {
	case s.eventNotify <- struct{}{}:
	default:
	}
}

func (s *Server) startEventLoop() {
	s.eventsMu.Lock()
	if s.eventLoopStarted {
		s.eventsMu.Unlock()
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	s.eventStop = stop
	s.eventDone = done
	s.eventLoopStarted = true
	s.eventsMu.Unlock()

	go s.runEventLoop(stop, done)
}

func (s *Server) stopEventLoop() {
	s.eventsMu.Lock()
	if !s.eventLoopStarted {
		s.eventsMu.Unlock()
		return
	}
	stop := s.eventStop
	done := s.eventDone
	s.eventLoopStarted = false
	close(stop)
	s.eventsMu.Unlock()
	<-done
}

func (s *Server) runEventLoop(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(eventFallbackInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.eventNotify:
			s.signalEventSubscribers()
		case <-ticker.C:
			// Every handler captures and diffs its own current snapshot. Sending a
			// prompt on every tick avoids suppressing A→B→A transitions for a
			// subscriber that connected while the shared state happened to be B.
			s.signalEventSubscribers()
		case <-stop:
			return
		}
	}
}

func (s *Server) stateEventPayload() (string, error) {
	b, err := json.Marshal(s.status())
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (s *Server) signalEventSubscribers() {
	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()
	for subscriber := range s.eventSubscribers {
		select {
		case subscriber <- struct{}{}:
		default:
		}
	}
}

func (s *Server) subscribeEvents() chan struct{} {
	subscriber := make(chan struct{}, eventSubscriberBuffer)
	s.eventsMu.Lock()
	s.eventSubscribers[subscriber] = struct{}{}
	s.eventsMu.Unlock()
	return subscriber
}

func (s *Server) unsubscribeEvents(subscriber chan struct{}) {
	s.eventsMu.Lock()
	delete(s.eventSubscribers, subscriber)
	s.eventsMu.Unlock()
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
			// Accept the X-OpenDeezer-Account header for same-account auth from
			// both loopback and LAN clients. We preserve account auth for
			// non-loopback LAN peers (the documented LAN-trust tradeoff): the
			// built-in control client only ever sends the account id (no
			// session-token path), and /whoami advertises "account".
			want := s.accountID()
			got := r.Header.Get("X-OpenDeezer-Account")
			// Constant-time compare (defense-in-depth; the id is only semi-secret).
			if want == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
				http.Error(w, `{"error":"account mismatch"}`, http.StatusUnauthorized)
				return
			}
		case s.sessionAuthRequired():
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
// An account switch (detected via the snapshot provider) revokes all sessions.
func (s *Server) hasValidSession(tok string) bool {
	if tok == "" {
		return false
	}
	s.pairMu.Lock()
	defer s.pairMu.Unlock()
	now := time.Now()
	// Account switch revokes sessions for previous account.
	currAcct := s.accountID()
	if s.pairedAccount != "" && currAcct != "" && s.pairedAccount != currAcct {
		s.sessions = make(map[string]session)
		s.pairedAccount = ""
		return false
	}
	found := false
	for stored, se := range s.sessions {
		// Constant-time compare even if the token isn't in the map (timing leak
		// on map presence is negligible vs 64-char entropy, but we do it right).
		if subtle.ConstantTimeCompare([]byte(tok), []byte(stored)) == 1 && now.Before(se.expiry) {
			found = true
		}
	}
	return found
}

// reapSessions removes expired session tokens. Must be called with pairMu held.
func (s *Server) reapSessions() {
	now := time.Now()
	for tok, se := range s.sessions {
		if now.After(se.expiry) {
			delete(s.sessions, tok)
		}
	}
}

// injectSession plants a session with the given expiry. Used in tests.
func (s *Server) injectSession(tok string, exp time.Time) {
	s.pairMu.Lock()
	s.sessions[tok] = session{expiry: exp, id: "test-" + tok}
	s.pairMu.Unlock()
}

// handleRemote serves the embedded web remote SPA. Only active when web remote
// is enabled (persistent flag); returns 404 otherwise. The pairing code itself
// remains single-use. No auth — the SPA itself enforces pairing.
func (s *Server) handleRemote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if !s.webRemoteEnabled() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(remoteHTML)
}

// handlePair handles POST /pair: validates the 6-digit code and, on success,
// issues a per-device session token (and id). The code is single-use and
// time-limited (5min TTL). Failed attempts are rate-limited with lockout.
func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	// Read the code from the JSON body only. The pairing code is a credential that
	// mints a 12h session token, so it must never travel in the query string (which
	// leaks into proxy/access logs, browser history and Referer) — matching this
	// package's header/body-only credential policy. The bundled SPA already POSTs it
	// in the body. Read outside the lock to avoid holding it across I/O.
	var body struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body)
	code := body.Code

	sourceIP := pairSourceIP(r.RemoteAddr)
	s.pairMu.Lock()
	defer s.pairMu.Unlock()

	if s.pairCodeGenerationFailed {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !s.pairEnabled {
		writeJSONError(w, http.StatusNotFound, "pairing not active")
		return
	}

	now := time.Now()
	if !s.pairCodeExpiry.IsZero() && now.After(s.pairCodeExpiry) {
		s.pairEnabled = false
		s.pairCode = ""
		s.pairCodeExpiry = time.Time{}
		writeJSONError(w, http.StatusUnauthorized, "pairing code expired")
		return
	}

	// A source that exhausts its own attempts cannot lock out another LAN peer.
	// A much higher global backstop still bounds distributed brute-force traffic.
	if s.pairSourceRateLimitedLocked(sourceIP, now) || s.pairGlobalRateLimitedLocked(now) {
		writeJSONError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}

	// Constant-time compare to prevent timing oracle on the 6-digit code.
	if subtle.ConstantTimeCompare([]byte(code), []byte(s.pairCode)) != 1 {
		s.recordPairFailureLocked(sourceIP, now)
		writeJSONError(w, http.StatusUnauthorized, "invalid code")
		return
	}

	// Success: single-use (invalidate code now), mint per-device session (token + id),
	// reap, set paired account, reset counters.
	tok, err := mintToken()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	sid, err := mintSessionID()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.reapSessions()
	exp := now.Add(12 * time.Hour)
	s.sessions[tok] = session{expiry: exp, id: sid}
	s.pairedAccount = s.accountID()
	s.resetPairRateLimitsLocked()
	s.pairEnabled = false
	s.pairCode = ""
	s.pairCodeExpiry = time.Time{}

	writeJSON(w, map[string]string{"token": tok, "id": sid})
}

func pairSourceIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	host = strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(host, "]"), "["))
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	if host == "" {
		return "unknown"
	}
	return host
}

// pairSourceRateLimitedLocked reports whether sourceIP is currently locked.
// pairMu must be held.
func (s *Server) pairSourceRateLimitedLocked(sourceIP string, now time.Time) bool {
	state := s.pairAttemptsByIP[sourceIP]
	if !state.lockoutUntil.IsZero() {
		if now.Before(state.lockoutUntil) {
			return true
		}
		delete(s.pairAttemptsByIP, sourceIP)
	}
	return false
}

// pairGlobalRateLimitedLocked reports whether the distributed-guess backstop
// is active. pairMu must be held.
func (s *Server) pairGlobalRateLimitedLocked(now time.Time) bool {
	if !s.pairGlobalLockoutUntil.IsZero() {
		if now.Before(s.pairGlobalLockoutUntil) {
			return true
		}
		s.pairGlobalLockoutUntil = time.Time{}
		s.pairGlobalAttempts = 0
		s.pairGlobalReset = time.Time{}
	}
	if !s.pairGlobalReset.IsZero() && !now.Before(s.pairGlobalReset) {
		s.pairGlobalAttempts = 0
		s.pairGlobalReset = time.Time{}
	}
	return false
}

// recordPairFailureLocked consumes one eligible attempt for sourceIP and the
// global backstop. Requests from an already-locked source never reach here, so
// one noisy host cannot trip the global limit by itself. pairMu must be held.
func (s *Server) recordPairFailureLocked(sourceIP string, now time.Time) {
	state := s.pairAttemptsByIP[sourceIP]
	state.attempts++
	if state.attempts >= pairSourceAttemptLimit {
		state.attempts = 0
		state.lockoutUntil = now.Add(pairSourceLockout)
	}
	s.pairAttemptsByIP[sourceIP] = state

	if s.pairGlobalReset.IsZero() {
		s.pairGlobalReset = now.Add(pairGlobalWindow)
	}
	s.pairGlobalAttempts++
	if s.pairGlobalAttempts >= pairGlobalAttemptLimit {
		s.pairGlobalLockoutUntil = now.Add(pairGlobalLockout)
	}
}

// resetPairRateLimitsLocked clears the rate state when an explicit new code is
// generated, pairing is disabled, or a pair succeeds. pairMu must be held.
func (s *Server) resetPairRateLimitsLocked() {
	s.pairAttemptsByIP = make(map[string]pairAttemptState)
	s.pairGlobalAttempts = 0
	s.pairGlobalReset = time.Time{}
	s.pairGlobalLockoutUntil = time.Time{}
}

// checkHost wraps the mux with a DNS-rebinding guard. A browser tricked into
// re-resolving attacker.com to a loopback/LAN address still sends
// Host: attacker.com, which we reject — closing the read-exfiltration path in
// the open "none" mode. It fronts every route so the web-remote SPA and /pair
// flow are covered too (they are reached by localhost or LAN IP, which pass).
func (s *Server) checkHost(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.allowedHost(r.Host) {
			http.Error(w, `{"error":"invalid host"}`, http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// allowedHost reports whether the request Host header is safe to serve. Accepted:
// an empty Host (non-browser HTTP/1.0 clients omit it), localhost, any literal IP
// (LAN clients connect by IP, and an IP literal cannot be DNS-rebound), and the
// configured bind host. Any other DNS name is rejected as a rebinding attempt.
func (s *Server) allowedHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	} else {
		// Bare "[::1]" (no port) leaves brackets that SplitHostPort won't strip.
		host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	}
	if host == "" || host == "localhost" {
		return true
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if bindHost, _, err := net.SplitHostPort(s.addr); err == nil && bindHost != "" && host == bindHost {
		return true
	}
	return false
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
	case s.sessionAuthRequired():
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

// handleEvents streams State snapshots as named SSE events. Every connection
// receives an initial snapshot, then only wire-visible changes. Subscriber
// notifications are buffered and coalesced non-blockingly, so a slow
// network client can only stall its own handler goroutine.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	subscriber := s.subscribeEvents()
	defer s.unsubscribeEvents(subscriber)

	initial, err := s.stateEventPayload()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	// The server's normal 15-second WriteTimeout is intentionally shorter than
	// the 25-second keepalive interval. Clear it for this streaming response;
	// writeSSEChunk installs a bounded deadline around each actual write.
	_ = controller.SetWriteDeadline(time.Time{})
	if err := writeSSEChunk(controller, w, "event: state\ndata: "+initial+"\n\n"); err != nil {
		return
	}
	lastSent := initial

	keepalive := time.NewTicker(eventKeepaliveInterval)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-subscriber:
			payload, err := s.stateEventPayload()
			if err != nil {
				continue
			}
			if payload == lastSent {
				continue
			}
			if err := writeSSEChunk(controller, w, "event: state\ndata: "+payload+"\n\n"); err != nil {
				return
			}
			lastSent = payload
		case <-keepalive.C:
			if err := writeSSEChunk(controller, w, ": keepalive\n\n"); err != nil {
				return
			}
		}
	}
}

func writeSSEChunk(controller *http.ResponseController, w http.ResponseWriter, chunk string) error {
	_ = controller.SetWriteDeadline(time.Now().Add(eventWriteTimeout))
	_, writeErr := io.WriteString(w, chunk)
	if writeErr == nil {
		writeErr = controller.Flush()
	}
	// Clear the short deadline after a successful flush. Leaving it armed would
	// make the next 25-second keepalive fail against an already-expired deadline.
	_ = controller.SetWriteDeadline(time.Time{})
	return writeErr
}

// handleEQGet handles GET /eq.
func (s *Server) handleEQGet(w http.ResponseWriter, r *http.Request) {
	if s.eq.State == nil {
		http.Error(w, `{"error":"not available"}`, http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, s.eq.State())
}

// handleEQSet handles POST /eq. Query params (each optional, applied in this
// order): preset=name, band=N&db=X, on=0|1, mono=0|1, preamp=dB.
func (s *Server) handleEQSet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	touched := false
	if v := q.Get("preset"); v != "" {
		touched = true
		if s.eq.SetPreset == nil {
			http.Error(w, `{"error":"not available"}`, http.StatusServiceUnavailable)
			return
		}
		if err := s.eq.SetPreset(v); err != nil {
			writeErr(w, err)
			return
		}
	}
	if v := q.Get("band"); v != "" {
		touched = true
		band, err1 := strconv.Atoi(v)
		db, err2 := strconv.ParseFloat(q.Get("db"), 64)
		if err1 != nil || err2 != nil {
			http.Error(w, `{"error":"band (int) and db (number) required"}`, http.StatusBadRequest)
			return
		}
		if s.eq.SetBand == nil {
			http.Error(w, `{"error":"not available"}`, http.StatusServiceUnavailable)
			return
		}
		if err := s.eq.SetBand(band, db); err != nil {
			writeErr(w, err)
			return
		}
	}
	if v := q.Get("on"); v != "" {
		touched = true
		if s.eq.SetEnabled != nil {
			s.eq.SetEnabled(v == "1" || strings.EqualFold(v, "true"))
		}
	}
	if v := q.Get("mono"); v != "" {
		touched = true
		if s.eq.SetMono != nil {
			s.eq.SetMono(v == "1" || strings.EqualFold(v, "true"))
		}
	}
	if v := q.Get("preamp"); v != "" {
		touched = true
		db, err := strconv.ParseFloat(v, 64)
		if err != nil {
			http.Error(w, `{"error":"preamp must be a number"}`, http.StatusBadRequest)
			return
		}
		if s.eq.SetPreamp != nil {
			s.eq.SetPreamp(db)
		}
	}
	if !touched {
		http.Error(w, `{"error":"no eq parameter given"}`, http.StatusBadRequest)
		return
	}
	if s.eq.State != nil {
		writeJSON(w, s.eq.State())
		return
	}
	writeJSON(w, s.status())
}

func (s *Server) handleSeek(w http.ResponseWriter, r *http.Request) {
	raw, present, valid := singleQueryValue(r.URL.Query(), "ms")
	ms, err := strconv.ParseInt(raw, 10, 64)
	if !present || !valid || err != nil || ms < 0 {
		writeJSONError(w, http.StatusBadRequest, "ms must be a non-negative integer")
		return
	}
	if s.cmds.Seek != nil {
		s.cmds.Seek(ms)
	}
	writeJSON(w, s.status())
}

func (s *Server) handleVolume(w http.ResponseWriter, r *http.Request) {
	raw, present, valid := singleQueryValue(r.URL.Query(), "v")
	v, err := strconv.ParseFloat(raw, 64)
	if !present || !valid || err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
		writeJSONError(w, http.StatusBadRequest, "v must be a finite number between 0 and 1")
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

func (s *Server) handleHistoryRecent(w http.ResponseWriter, r *http.Request) {
	n := defaultHistoryRecent
	if _, present := r.URL.Query()["n"]; present {
		var valid bool
		n, valid = strictNonNegativeInt(r.URL.Query(), "n")
		if !valid || n == 0 || n > maxHistoryRecent {
			writeJSONError(w, http.StatusBadRequest, "n must be an integer between 1 and 500")
			return
		}
	}
	if s.cmds.HistoryRecent == nil {
		writeJSONError(w, http.StatusNotFound, "not available")
		return
	}
	history, err := s.cmds.HistoryRecent(n)
	if err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	if len(history) == 0 {
		history = json.RawMessage(`[]`)
	} else if !json.Valid(history) {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, history)
}

func (s *Server) handleQueueAdd(w http.ResponseWriter, r *http.Request) {
	id, valid := strictPositiveID(r.URL.Query(), "id")
	if !valid {
		writeJSONError(w, http.StatusBadRequest, "id must be a positive decimal integer")
		return
	}
	rawNext, present, valid := singleQueryValue(r.URL.Query(), "next")
	if !present || !valid || (rawNext != "0" && rawNext != "1") {
		writeJSONError(w, http.StatusBadRequest, "next must be 0 or 1")
		return
	}
	if s.cmds.QueueAdd == nil {
		writeJSONError(w, http.StatusNotImplemented, "not available")
		return
	}
	if err := s.cmds.QueueAdd(id, rawNext == "1"); err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	s.NotifyStateChanged()
	writeJSON(w, s.status())
}

func (s *Server) handleQueueJump(w http.ResponseWriter, r *http.Request) {
	index, valid := strictNonNegativeInt(r.URL.Query(), "i")
	if !valid {
		writeJSONError(w, http.StatusBadRequest, "i must be a non-negative integer")
		return
	}
	if s.cmds.QueueJump == nil {
		writeJSONError(w, http.StatusNotImplemented, "not available")
		return
	}
	if err := s.cmds.QueueJump(index); err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	s.NotifyStateChanged()
	writeJSON(w, s.status())
}

func (s *Server) handleQueueRemove(w http.ResponseWriter, r *http.Request) {
	index, valid := strictNonNegativeInt(r.URL.Query(), "i")
	if !valid {
		writeJSONError(w, http.StatusBadRequest, "i must be a non-negative integer")
		return
	}
	if s.cmds.QueueRemove == nil {
		writeJSONError(w, http.StatusNotImplemented, "not available")
		return
	}
	if err := s.cmds.QueueRemove(index); err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	s.NotifyStateChanged()
	writeJSON(w, s.status())
}

func (s *Server) handleQueueMove(w http.ResponseWriter, r *http.Request) {
	from, fromValid := strictNonNegativeInt(r.URL.Query(), "from")
	to, toValid := strictNonNegativeInt(r.URL.Query(), "to")
	if !fromValid || !toValid {
		writeJSONError(w, http.StatusBadRequest, "from and to must be non-negative integers")
		return
	}
	if s.cmds.QueueMove == nil {
		writeJSONError(w, http.StatusNotImplemented, "not available")
		return
	}
	if err := s.cmds.QueueMove(from, to); err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	s.NotifyStateChanged()
	writeJSON(w, s.status())
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

func (s *Server) handlePlayAlbum(w http.ResponseWriter, r *http.Request) {
	s.handleOptionalIDCommand(w, r, s.cmds.PlayAlbum)
}

func (s *Server) handlePlayMixTrack(w http.ResponseWriter, r *http.Request) {
	s.handleOptionalIDCommand(w, r, s.cmds.PlayMixTrack)
}

func (s *Server) handlePlayMixArtist(w http.ResponseWriter, r *http.Request) {
	s.handleOptionalIDCommand(w, r, s.cmds.PlayMixArtist)
}

func (s *Server) handleOptionalIDCommand(w http.ResponseWriter, r *http.Request, command func(string) error) {
	id, valid := strictPositiveID(r.URL.Query(), "id")
	if !valid {
		writeJSONError(w, http.StatusBadRequest, "id must be a positive decimal integer")
		return
	}
	if command == nil {
		writeJSONError(w, http.StatusNotImplemented, "not available")
		return
	}
	if err := command(id); err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	s.NotifyStateChanged()
	writeJSON(w, s.status())
}

// handleSleep handles POST /sleep. ?minutes=N arms a duration timer; ?eot=1 arms
// an end-of-track timer; ?minutes=0 (or ?cancel=1) disarms it.
func (s *Server) handleSleep(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cancel, cancelPresent, err := optionalStrictBool(q, "cancel")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "cancel must be one of true, false, 1, 0")
		return
	}
	eot, _, err := optionalStrictBool(q, "eot")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "eot must be one of true, false, 1, 0")
		return
	}

	rawMinutes, minutesPresent, minutesValid := singleQueryValue(q, "minutes")
	minutes := 0
	if minutesPresent {
		if !minutesValid {
			writeJSONError(w, http.StatusBadRequest, "minutes must be an integer between 0 and 1440")
			return
		}
		minutes, err = strconv.Atoi(rawMinutes)
		if err != nil || minutes < 0 || minutes > maxSleepMinutes {
			writeJSONError(w, http.StatusBadRequest, "minutes must be an integer between 0 and 1440")
			return
		}
	}

	if cancelPresent && cancel {
		if s.cmds.CancelSleepTimer != nil {
			s.cmds.CancelSleepTimer()
		}
		writeJSON(w, s.status())
		return
	}
	// Preserve the documented legacy cancellation forms: no parameters and
	// minutes=0. Any non-zero duration has already been validated as 1..1440.
	if !eot && (!minutesPresent || minutes == 0) {
		if s.cmds.CancelSleepTimer != nil {
			s.cmds.CancelSleepTimer()
		}
		writeJSON(w, s.status())
		return
	}
	if s.cmds.SetSleepTimer != nil {
		s.cmds.SetSleepTimer(minutes, eot)
	}
	writeJSON(w, s.status())
}

// handleRepeat handles POST /repeat. With ?mode=off|all|one it SETS repeat
// (via SetRepeat); with no param it cycles via CycleRepeat (legacy behaviour).
func (s *Server) handleRepeat(w http.ResponseWriter, r *http.Request) {
	mode, present, valid := singleQueryValue(r.URL.Query(), "mode")
	if present {
		if !valid || (mode != "off" && mode != "all" && mode != "one") {
			writeJSONError(w, http.StatusBadRequest, "mode must be one of off, all, one")
			return
		}
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
	raw, present, valid := singleQueryValue(r.URL.Query(), "on")
	if present {
		if !valid {
			writeJSONError(w, http.StatusBadRequest, "on must be one of true, false, 1, 0")
			return
		}
		on, ok := strictBool(raw)
		if !ok {
			writeJSONError(w, http.StatusBadRequest, "on must be one of true, false, 1, 0")
			return
		}
		if s.cmds.SetShuffle != nil {
			s.cmds.SetShuffle(on)
		}
	} else {
		call(s.cmds.ToggleShuffle)
	}
	writeJSON(w, s.status())
}

// singleQueryValue distinguishes an absent parameter from an explicitly empty
// or duplicated one. Strict mutation inputs accept exactly one non-empty value.
func singleQueryValue(q url.Values, name string) (value string, present, valid bool) {
	values, present := q[name]
	if !present {
		return "", false, false
	}
	if len(values) != 1 || values[0] == "" {
		return "", true, false
	}
	return values[0], true, true
}

func strictNonNegativeInt(q url.Values, name string) (int, bool) {
	raw, present, valid := singleQueryValue(q, name)
	if !present || !valid {
		return 0, false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, false
		}
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return value, true
}

func strictPositiveID(q url.Values, name string) (string, bool) {
	raw, present, valid := singleQueryValue(q, name)
	if !present || !valid {
		return "", false
	}
	nonZero := false
	for i := 0; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return "", false
		}
		if raw[i] != '0' {
			nonZero = true
		}
	}
	if !nonZero {
		return "", false
	}
	if _, err := strconv.ParseUint(raw, 10, 64); err != nil {
		return "", false
	}
	return raw, true
}

func strictBool(raw string) (bool, bool) {
	switch raw {
	case "true", "1":
		return true, true
	case "false", "0":
		return false, true
	default:
		return false, false
	}
}

func optionalStrictBool(q url.Values, name string) (value, present bool, err error) {
	raw, present, valid := singleQueryValue(q, name)
	if !present {
		return false, false, nil
	}
	if !valid {
		return false, true, errors.New("invalid boolean")
	}
	value, ok := strictBool(raw)
	if !ok {
		return false, true, errors.New("invalid boolean")
	}
	return value, true, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
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

// mintSessionID returns a random non-secret identifier for a session (used as
// the per-device id for revocation). It is returned to the pairing client.
func mintSessionID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
