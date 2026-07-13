// Package deezer is a Deezer client: ARL login, gw-light + public REST browse,
// and track -> CDN-url resolution. The ARL never leaves the machine beyond the
// requests it makes to Deezer.
package deezer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// ErrARLExpired is returned by Login when the ARL cookie is missing/expired or
// otherwise rejected, so callers can distinguish "re-auth needed" from a
// transient network failure and prompt the user accordingly.
var ErrARLExpired = errors.New("ARL expired or invalid — re-login required")

// ErrNoNetwork wraps any transport-level failure reaching Deezer (DNS failure,
// connection refused, host/network unreachable, TLS/dial timeout, context
// deadline). Callers use errors.Is(err, ErrNoNetwork) to show a "No Internet"
// screen and offer a retry, instead of mistaking an outage for an expired ARL
// (ErrARLExpired) and forcing the user to re-authenticate. The two are mutually
// exclusive: a reachable Deezer that rejects the ARL yields ErrARLExpired; an
// unreachable Deezer yields ErrNoNetwork.
var ErrNoNetwork = errors.New("no internet connection")

// ErrNoFullMedia signals that resolveMediaURL obtained no full-length source
// because the account is not entitled (Free tier, geo, licensing, etc.). It is
// distinct from transient transport, 5xx, or rate-limit errors. PrepareStream
// falls back to the 30 s preview ONLY on ErrNoFullMedia (wrapped); any other
// error from resolve (after retries) is returned as-is so callers see the real
// failure instead of a silent clip downgrade.
var ErrNoFullMedia = errors.New("no full-length media available for this account")

// classifyNet wraps err with ErrNoNetwork when it is a transport/reachability
// failure (so errors.Is(err, ErrNoNetwork) holds), and returns err unchanged
// otherwise. Every c.http.Do call site pipes its transport error through this so
// the online/offline distinction is made in exactly one place. HTTP-level
// rejections (non-2xx, Deezer error envelopes) are NOT network errors and are
// left to the existing per-call handling.
func classifyNet(err error) error {
	if err == nil {
		return nil
	}
	if isNetworkErr(err) {
		return fmt.Errorf("%w: %v", ErrNoNetwork, err)
	}
	return err
}

// isNetworkErr reports whether err is a transport-level connectivity failure.
// net/http wraps transport errors in *url.Error, whose Unwrap chain reaches the
// underlying *net.OpError / *net.DNSError / syscall.Errno / context deadline, so
// errors.As/Is see through it.
func isNetworkErr(err error) bool {
	if err == nil {
		return false
	}
	// Dial/read/write timeouts and the request context deadline (30s client
	// timeout fires as context.DeadlineExceeded).
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// DNS resolution failure (host not found / no route to resolver).
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	// Refused / unreachable / network-down at the syscall layer.
	for _, se := range []syscall.Errno{
		syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.EHOSTUNREACH,
		syscall.ENETUNREACH, syscall.ENETDOWN, syscall.ETIMEDOUT,
	} {
		if errors.Is(err, se) {
			return true
		}
	}
	// Any remaining net.Error that reports itself as a timeout, plus the generic
	// dial/OpError case (covers platforms/TLS handshakes not caught above).
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var opErr *net.OpError
	return errors.As(err, &opErr)
}

// IsNoNetwork reports whether err (or anything it wraps) is a transport-level
// connectivity failure. Exported for callers outside this package (the FFI
// bindings, the TUI) that classify a login/browse failure.
func IsNoNetwork(err error) bool { return errors.Is(err, ErrNoNetwork) }

// IsARLExpired reports whether err is an expired/invalid-ARL auth failure.
func IsARLExpired(err error) bool { return errors.Is(err, ErrARLExpired) }

// IsNoFullMedia reports whether err indicates an entitlement rejection
// (no full track source) rather than a transient problem.
func IsNoFullMedia(err error) bool { return errors.Is(err, ErrNoFullMedia) }

// isTransientMediaProblem reports true for transport, timeout, 5xx and rate
// limit cases that must be retried (inside resolve) and must NOT trigger a
// preview fallback in PrepareStream. We treat HTTP 5xx and 429 as transient;
// other HTTP 4xx and Deezer error payloads without those signals are treated
// as entitlement rejections (so preview is appropriate for free/geo cases).
func isTransientMediaProblem(err error, apiErr string) bool {
	if err != nil {
		if IsNoNetwork(err) || isNetworkErr(err) || errors.Is(err, context.DeadlineExceeded) {
			return true
		}
		low := strings.ToLower(err.Error())
		if strings.Contains(low, "timeout") || strings.Contains(low, "5") && strings.Contains(low, "http") {
			return true
		}
	}
	if apiErr != "" {
		if strings.HasPrefix(apiErr, "HTTP 5") {
			return true
		}
		low := strings.ToLower(apiErr)
		if strings.Contains(low, "429") || strings.Contains(low, "rate") || strings.Contains(low, "too many requests") {
			return true
		}
	}
	return false
}

const (
	gwURL    = "https://www.deezer.com/ajax/gw-light.php"
	mediaURL = "https://media.deezer.com/v1/get_url"
	restURL  = "https://api.deezer.com"

	userAgent = "Mozilla/5.0 OpenDeezer/0.1"
)

// Client holds an authenticated Deezer session.
type Client struct {
	arl             string // immutable after New
	quality         int32  // 0=MP3_128, 1=MP3_320, 2=FLAC (lossless) — set atomically
	http            *http.Client
	restURLOverride string // override for testing REST API base URL

	// mu guards every session/identity field below: they are (re)written by
	// Login, which can now run concurrently with reads from the control-API HTTP
	// goroutine (browse) and Bubble Tea command goroutines. All writes happen in
	// one locked block at the end of Login; reads go through the locked accessors.
	mu           sync.RWMutex
	apiToken     string
	licenseToken string
	sid          string
	userID       string
	userName     string
	offerName    string // e.g. "Deezer Premium", "Deezer Free"
	canHiFi      bool   // account entitled to lossless
	canHQ        bool   // account entitled to MP3_320

	// Free-tier ads / listen logging (see ads.go). adMu guards the cadence state.
	adMu        sync.Mutex
	adOnce      sync.Once   // lazy one-shot deezer.adConfig fetch
	adCad       AdCadence   // audio-ad schedule (Enabled=false on paid accounts)
	adPlays     int         // tracks reported this session (drives the cadence)
	adsDisabled atomic.Bool // user opted out of play-reporting/ads (free, at own risk)
}

// Account summarizes the logged-in user's plan and entitlements.
type Account struct {
	UserID   string `json:"userId"`
	Name     string `json:"name"`
	Offer    string `json:"offer"`
	CanHQ    bool   `json:"canHq"`   // entitled to MP3 320
	CanHiFi  bool   `json:"canHifi"` // entitled to lossless FLAC
	Premium  bool   `json:"premium"` // a paid plan that can stream on-demand
	LoggedIn bool   `json:"loggedIn"`
}

// Quality levels.
const (
	QualityNormal   = 0 // MP3 128
	QualityHigh     = 1 // MP3 320
	QualityLossless = 2 // FLAC (HiFi) — falls back to MP3 if unavailable
)

// SetQuality selects the preferred stream quality (0..2). Deezer returns the
// first format the account+track is entitled to, so an unentitled FLAC/320
// automatically falls back to a lower MP3 format.
func (c *Client) SetQuality(q int) {
	if q < 0 {
		q = 0
	} else if q > 2 {
		q = 2
	}
	atomic.StoreInt32(&c.quality, int32(q))
}

// Quality returns the current preference (0..2).
func (c *Client) Quality() int { return int(atomic.LoadInt32(&c.quality)) }

// SetHighQuality keeps the old bool API: true => MP3_320, false => MP3_128.
func (c *Client) SetHighQuality(high bool) {
	if high {
		c.SetQuality(QualityHigh)
	} else {
		c.SetQuality(QualityNormal)
	}
}

// HighQuality reports whether the preference is at least MP3_320.
func (c *Client) HighQuality() bool { return c.Quality() >= QualityHigh }

// New builds a client for the given ARL (not yet logged in).
func New(arl string) *Client {
	return &Client{
		arl: strings.TrimSpace(arl),
		http: &http.Client{
			Timeout: 30 * time.Second,
			// We don't install a cookie jar (we set the Cookie header ourselves), so
			// the only reason to override CheckRedirect is to keep a sane hop cap: a
			// bare `return nil` would disable Go's default 10-redirect limit and let a
			// misbehaving host loop until the timeout.
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
	}
}

// LoggedIn reports whether Login succeeded.
func (c *Client) LoggedIn() bool { return c.apiTok() != "" }

// UserID returns the numeric Deezer user id (after Login).
func (c *Client) UserID() string { return c.uid() }

// Locked accessors for the session fields (safe under concurrent Login).
func (c *Client) apiTok() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.apiToken
}
func (c *Client) uid() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.userID
}
func (c *Client) licTok() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.licenseToken
}

func (c *Client) cookie() string {
	c.mu.RLock()
	sid := c.sid
	c.mu.RUnlock()
	ck := "arl=" + c.arl // arl is immutable after New
	if sid != "" {
		ck += "; sid=" + sid
	}
	return ck
}

// Login authenticates and fetches api_token + license_token + sid + user id.
func (c *Client) Login() error {
	u := gwURL + "?method=deezer.getUserData&input=3&api_version=1.0&api_token="
	req, err := http.NewRequest(http.MethodPost, u, strings.NewReader("{}"))
	if err != nil {
		return err
	}
	req.Header.Set("Cookie", "arl="+c.arl)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return classifyNet(err)
	}
	defer resp.Body.Close()

	// Pull sid from Set-Cookie.
	var sid string
	for _, ck := range resp.Cookies() {
		if strings.EqualFold(ck.Name, "sid") {
			sid = ck.Value
		}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var parsed struct {
		Results struct {
			CheckForm string `json:"checkForm"`
			User      struct {
				UserID    json.Number `json:"USER_ID"`
				BlogName  string      `json:"BLOG_NAME"`
				Firstname string      `json:"FIRSTNAME"`
				Options   struct {
					LicenseToken   string `json:"license_token"`
					WebHQ          bool   `json:"web_hq"`
					WebLossless    bool   `json:"web_lossless"`
					MobileHQ       bool   `json:"mobile_hq"`
					MobileLossless bool   `json:"mobile_lossless"`
				} `json:"OPTIONS"`
			} `json:"USER"`
			Offers []struct {
				Title string `json:"title"`
			} `json:"OFFERS"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("parse getUserData: %w", err)
	}
	// A populated error envelope with empty results is a gateway/quota failure,
	// not an auth one — don't misreport it as an expired ARL (unless it actually
	// asks for auth).
	if gwErr := gwError(body); gwErr != "" && !strings.Contains(gwErr, "AUTH") {
		return fmt.Errorf("deezer gw deezer.getUserData: %s", gwErr)
	}
	apiToken := parsed.Results.CheckForm
	userID := parsed.Results.User.UserID.String()
	licenseToken := parsed.Results.User.Options.LicenseToken
	if apiToken == "" || userID == "" || userID == "0" {
		// A blank checkForm / user 0 is exactly what Deezer returns for an
		// anonymous (= expired/invalid ARL) session.
		return ErrARLExpired
	}
	userName := parsed.Results.User.BlogName
	if userName == "" {
		userName = parsed.Results.User.Firstname
	}
	opt := parsed.Results.User.Options
	canHQ := opt.WebHQ || opt.MobileHQ
	canHiFi := opt.WebLossless || opt.MobileLossless
	offerName := ""
	if len(parsed.Results.Offers) > 0 {
		offerName = parsed.Results.Offers[0].Title
	}
	if offerName == "" {
		switch {
		case canHiFi:
			offerName = "Deezer (HiFi)"
		case canHQ:
			offerName = "Deezer Premium"
		default:
			offerName = "Deezer Free"
		}
	}

	// Commit all session fields atomically so concurrent readers never observe a
	// half-updated session (and concurrent re-logins don't tear each other's writes).
	c.mu.Lock()
	c.sid = sid
	c.apiToken = apiToken
	c.userID = userID
	c.licenseToken = licenseToken
	c.userName = userName
	c.canHQ = canHQ
	c.canHiFi = canHiFi
	c.offerName = offerName
	c.mu.Unlock()
	return nil
}

// Account returns the logged-in user's plan + entitlement summary.
func (c *Client) Account() Account {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return Account{
		UserID:  c.userID,
		Name:    c.userName,
		Offer:   c.offerName,
		CanHQ:   c.canHQ,
		CanHiFi: c.canHiFi,
		// Premium = entitled to on-demand HQ/lossless streaming. Deezer Free has
		// neither, so it can't actually stream tracks in this client.
		Premium:  c.canHQ || c.canHiFi,
		LoggedIn: c.apiToken != "",
	}
}

// gwRaw performs one gw-light call and returns the raw response body.
func (c *Client) gwRaw(method, jsonBody string) ([]byte, error) {
	apiToken := c.apiTok()
	if apiToken == "" {
		return nil, fmt.Errorf("not logged in")
	}
	u := gwURL + "?method=" + method + "&input=3&api_version=1.0&api_token=" + url.QueryEscape(apiToken)
	req, err := http.NewRequest(http.MethodPost, u, strings.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", c.cookie())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, classifyNet(err)
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// gwError extracts a gw error message from the response envelope. gw returns
// "error":{} / "error":[] when OK, and a populated object/array otherwise
// (e.g. {"VALID_TOKEN_REQUIRED":"..."}).
func gwError(body []byte) string {
	var env struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &env) != nil {
		return ""
	}
	s := strings.TrimSpace(string(env.Error))
	if s == "" || s == "[]" || s == "{}" || s == "null" {
		return ""
	}
	return s
}

// jsonEsc renders s as a JSON string literal so caller-supplied ids can never
// break out of a gw body built with Sprintf (quotes/backslashes get escaped).
func jsonEsc(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// gw calls a gw method, transparently re-logging in once if the API token has
// expired (Deezer rotates it), and returns an error on a non-empty envelope.
func (c *Client) gw(method, jsonBody string) ([]byte, error) {
	body, err := c.gwRaw(method, jsonBody)
	if err != nil {
		return nil, err
	}
	gwErr := gwError(body)
	if gwErr != "" && strings.Contains(gwErr, "TOKEN") {
		// Stale token: re-login once and retry.
		if err := c.Login(); err != nil {
			return nil, fmt.Errorf("re-login: %w", err)
		}
		body, err = c.gwRaw(method, jsonBody)
		if err != nil {
			return nil, err
		}
		gwErr = gwError(body)
	}
	if gwErr != "" {
		return nil, fmt.Errorf("deezer gw %s: %s", method, gwErr)
	}
	return body, nil
}

func (c *Client) getRestURL() string {
	if c.restURLOverride != "" {
		return c.restURLOverride
	}
	return restURL
}

// restGet calls the public REST API (no auth needed). REST reports failures as
// an HTTP-200 body with an "error" envelope ({"error":{"type":...,"code":...}}),
// so that is surfaced as a Go error rather than decoding to empty results.
func (c *Client) restGet(path string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, c.getRestURL()+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, classifyNet(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("deezer rest %s: HTTP %d", path, resp.StatusCode)
	}
	var env struct {
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &env) == nil && env.Error != nil &&
		(env.Error.Type != "" || env.Error.Message != "" || env.Error.Code != 0) {
		msg := env.Error.Message
		if msg == "" {
			msg = env.Error.Type
		}
		return nil, fmt.Errorf("deezer rest %s: %s (code %d)", path, msg, env.Error.Code)
	}
	return b, nil
}

// gwCover builds a 250x250 cover URL from an md5 image hash.
func gwCover(md5 string) string {
	if md5 == "" {
		return ""
	}
	return "https://e-cdns-images.dzcdn.net/images/cover/" + md5 + "/250x250-000000-80-0-0.jpg"
}

// ---- REST DTOs ----

type restArtist struct {
	ID   json.Number `json:"id"`
	Name string      `json:"name"`
}
type restTrackDTO struct {
	ID            json.Number `json:"id"`
	Title         string      `json:"title"`
	Duration      json.Number `json:"duration"`
	ExplicitLyric bool        `json:"explicit_lyrics"`
	// TrackPosition is the album track number, present on album-tracks listings.
	TrackPosition json.Number `json:"track_position"`
	// ReleaseDate is an ISO date ("2021-06-15"); present on track/album objects.
	ReleaseDate string     `json:"release_date"`
	Artist      restArtist `json:"artist"`
	Album       struct {
		Title       string `json:"title"`
		CoverMedium string `json:"cover_medium"`
	} `json:"album"`
}

func (r restTrackDTO) toTrack() Track {
	durSec, _ := r.Duration.Int64()
	pos, _ := r.TrackPosition.Int64()
	return Track{
		ID:          r.ID.String(),
		Name:        r.Title,
		DurationMS:  durSec * 1000,
		Artists:     []Artist{{ID: r.Artist.ID.String(), Name: r.Artist.Name}},
		AlbumName:   r.Album.Title,
		ArtworkURL:  r.Album.CoverMedium,
		Explicit:    r.ExplicitLyric,
		TrackNumber: int(pos),
		Year:        yearFromDate(r.ReleaseDate),
	}
}

// yearFromDate extracts a 4-digit release year from a Deezer ISO date
// ("2021-06-15" -> "2021"). It returns "" when the date is absent or does not
// begin with four digits, so the tagger leaves the year field unset.
func yearFromDate(date string) string {
	if len(date) < 4 {
		return ""
	}
	y := date[:4]
	for i := 0; i < 4; i++ {
		if y[i] < '0' || y[i] > '9' {
			return ""
		}
	}
	return y
}

// ---- gw DTOs (mixed string/number ids; ALL-CAPS keys) ----

type gwTrackDTO struct {
	SngID      json.Number `json:"SNG_ID"`
	SngTitle   string      `json:"SNG_TITLE"`
	Duration   json.Number `json:"DURATION"`
	ArtID      json.Number `json:"ART_ID"`
	ArtName    string      `json:"ART_NAME"`
	AlbTitle   string      `json:"ALB_TITLE"`
	AlbPicture string      `json:"ALB_PICTURE"`
	Explicit   json.Number `json:"EXPLICIT_LYRICS"` // "0"/"1" (or absent)
}

func (g gwTrackDTO) toTrack() Track {
	durSec, _ := g.Duration.Int64()
	exp, _ := g.Explicit.Int64()
	return Track{
		ID:         g.SngID.String(),
		Name:       g.SngTitle,
		DurationMS: durSec * 1000,
		Artists:    []Artist{{ID: g.ArtID.String(), Name: g.ArtName}},
		AlbumName:  g.AlbTitle,
		ArtworkURL: gwCover(g.AlbPicture),
		Explicit:   exp != 0,
	}
}

// Search queries tracks, albums, artists, and playlists concurrently. A failing
// sub-search (due to network or JSON parsing issues) is tolerated as long as at
// least one category succeeds. If all four categories fail, the underlying errors
// are joined and returned, so callers can tell "no matches" from "all requests failed".
func (c *Client) Search(query string) (*SearchResults, error) {
	enc := url.QueryEscape(query)

	var (
		wg sync.WaitGroup

		errTracks    error
		tracksResult []Track

		errAlbums    error
		albumsResult []Album

		errArtists    error
		artistsResult []ArtistInfo

		errPlaylists    error
		playlistsResult []Playlist
	)

	wg.Add(4)

	// 1. Tracks sub-search
	go func() {
		defer wg.Done()
		b, err := c.restGet("/search?q=" + enc + "&limit=40")
		if err != nil {
			errTracks = err
			return
		}
		var r struct {
			Data []restTrackDTO `json:"data"`
		}
		if err = json.Unmarshal(b, &r); err != nil {
			errTracks = fmt.Errorf("json unmarshal: %w", err)
			return
		}
		tracksResult = make([]Track, 0, len(r.Data))
		for _, t := range r.Data {
			tracksResult = append(tracksResult, t.toTrack())
		}
	}()

	// 2. Albums sub-search
	go func() {
		defer wg.Done()
		b, err := c.restGet("/search/album?q=" + enc + "&limit=20")
		if err != nil {
			errAlbums = err
			return
		}
		var r struct {
			Data []struct {
				ID          json.Number `json:"id"`
				Title       string      `json:"title"`
				Artist      restArtist  `json:"artist"`
				CoverMedium string      `json:"cover_medium"`
			} `json:"data"`
		}
		if err = json.Unmarshal(b, &r); err != nil {
			errAlbums = fmt.Errorf("json unmarshal: %w", err)
			return
		}
		albumsResult = make([]Album, 0, len(r.Data))
		for _, a := range r.Data {
			albumsResult = append(albumsResult, Album{
				ID:         a.ID.String(),
				Name:       a.Title,
				Artists:    []Artist{{ID: a.Artist.ID.String(), Name: a.Artist.Name}},
				ArtworkURL: a.CoverMedium,
			})
		}
	}()

	// 3. Artists sub-search
	go func() {
		defer wg.Done()
		b, err := c.restGet("/search/artist?q=" + enc + "&limit=20")
		if err != nil {
			errArtists = err
			return
		}
		var r struct {
			Data []struct {
				ID            json.Number `json:"id"`
				Name          string      `json:"name"`
				PictureMedium string      `json:"picture_medium"`
				NbFan         int         `json:"nb_fan"`
			} `json:"data"`
		}
		if err = json.Unmarshal(b, &r); err != nil {
			errArtists = fmt.Errorf("json unmarshal: %w", err)
			return
		}
		artistsResult = make([]ArtistInfo, 0, len(r.Data))
		for _, a := range r.Data {
			artistsResult = append(artistsResult, ArtistInfo{
				ID:         a.ID.String(),
				Name:       a.Name,
				ArtworkURL: a.PictureMedium,
				NbFans:     a.NbFan,
			})
		}
	}()

	// 4. Playlists sub-search
	go func() {
		defer wg.Done()
		b, err := c.restGet("/search/playlist?q=" + enc + "&limit=20")
		if err != nil {
			errPlaylists = err
			return
		}
		var r struct {
			Data []struct {
				ID            json.Number           `json:"id"`
				Title         string                `json:"title"`
				User          struct{ Name string } `json:"user"`
				NbTracks      int                   `json:"nb_tracks"`
				PictureMedium string                `json:"picture_medium"`
			} `json:"data"`
		}
		if err = json.Unmarshal(b, &r); err != nil {
			errPlaylists = fmt.Errorf("json unmarshal: %w", err)
			return
		}
		playlistsResult = make([]Playlist, 0, len(r.Data))
		for _, p := range r.Data {
			playlistsResult = append(playlistsResult, Playlist{
				ID:         p.ID.String(),
				Name:       p.Title,
				Owner:      p.User.Name,
				TrackCount: p.NbTracks,
				ArtworkURL: p.PictureMedium,
			})
		}
	}()

	wg.Wait()

	var errs []error
	if errTracks != nil {
		errs = append(errs, fmt.Errorf("tracks: %w", errTracks))
	}
	if errAlbums != nil {
		errs = append(errs, fmt.Errorf("albums: %w", errAlbums))
	}
	if errArtists != nil {
		errs = append(errs, fmt.Errorf("artists: %w", errArtists))
	}
	if errPlaylists != nil {
		errs = append(errs, fmt.Errorf("playlists: %w", errPlaylists))
	}

	if len(errs) == 4 {
		return nil, errors.Join(errs...)
	}

	sr := &SearchResults{
		Tracks:    tracksResult,
		Albums:    albumsResult,
		Artists:   artistsResult,
		Playlists: playlistsResult,
	}
	return sr, nil
}

// albumPageSize is the per-request page for the REST album tracklist.
const albumPageSize = 100

// AlbumTracks lists an album's tracks via the public REST API, paging until
// the album is exhausted. A single page caps at 100 tracks, and albums
// (compilations, box sets) can exceed that; without pagination they would be
// silently truncated. Any page error fails the whole listing (SaveAlbum
// depends on a complete tracklist). Bounded by maxTracks like gwTrackAll.
func (c *Client) AlbumTracks(id string) ([]Track, error) {
	var out []Track
	for index := 0; index < maxTracks; index += albumPageSize {
		b, err := c.restGet(fmt.Sprintf("/album/%s/tracks?limit=%d&index=%d", id, albumPageSize, index))
		if err != nil {
			return nil, err
		}
		var r struct {
			Data []restTrackDTO `json:"data"`
			Next string         `json:"next"`
		}
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		for _, t := range r.Data {
			out = append(out, t.toTrack())
		}
		// A short/empty page or a response without a "next" link means we've
		// reached the end.
		if len(r.Data) < albumPageSize || r.Next == "" {
			break
		}
	}
	return out, nil
}

// pageSize is the per-request page for paginated gw lists.
const pageSize = 200

// maxTracks caps a paginated fetch so a huge library can't run away.
const maxTracks = 5000

// gwTrackPage fetches one page of gw tracks for a method whose body is
// "<extra>,\"nb\":<n>,\"start\":<s>".
func (c *Client) gwTrackPage(method, extra string, start, nb int) ([]Track, error) {
	body := fmt.Sprintf(`{%s,"nb":%d,"start":%d}`, extra, nb, start)
	b, err := c.gw(method, body)
	if err != nil {
		return nil, err
	}
	var r struct {
		Results struct {
			Data []gwTrackDTO `json:"data"`
		} `json:"results"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	out := make([]Track, 0, len(r.Results.Data))
	for _, t := range r.Results.Data {
		out = append(out, t.toTrack())
	}
	return out, nil
}

// gwTrackAll pages through a gw track list until it's exhausted.
func (c *Client) gwTrackAll(method, extra string) ([]Track, error) {
	var all []Track
	for start := 0; start < maxTracks; start += pageSize {
		page, err := c.gwTrackPage(method, extra, start, pageSize)
		if err != nil {
			if len(all) > 0 {
				return all, nil // keep what we have on a mid-fetch error
			}
			return nil, err
		}
		all = append(all, page...)
		if len(page) < pageSize {
			break
		}
	}
	return all, nil
}

// PlaylistTracks lists a playlist's tracks (gw, works for private playlists).
// A mid-fetch page error is tolerated (partial list, nil error) — acceptable
// for browse/UI. Batch downloads must use playlistTracksStrict instead.
func (c *Client) PlaylistTracks(id string) ([]Track, error) {
	return c.gwTrackAll("playlist.getSongs", fmt.Sprintf(`"playlist_id":%s`, jsonEsc(id)))
}

// playlistTracksStrict is PlaylistTracks without gwTrackAll's mid-fetch
// tolerance: any page error fails the whole listing. SavePlaylist uses it so
// a truncated playlist is never downloaded and reported as success.
func (c *Client) playlistTracksStrict(id string) ([]Track, error) {
	extra := fmt.Sprintf(`"playlist_id":%s`, jsonEsc(id))
	return collectTrackPagesStrict(func(start, nb int) ([]Track, error) {
		return c.gwTrackPage("playlist.getSongs", extra, start, nb)
	})
}

// collectTrackPagesStrict pages through a track list via fetch(start, nb),
// returning nil and the error if ANY page fails (no partial tolerance).
// Bounded by maxTracks like gwTrackAll.
func collectTrackPagesStrict(fetch func(start, nb int) ([]Track, error)) ([]Track, error) {
	var all []Track
	for start := 0; start < maxTracks; start += pageSize {
		page, err := fetch(start, pageSize)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < pageSize {
			break
		}
	}
	return all, nil
}

// Track fetches a single track's metadata by id (gw song.getData). Used by the
// control API / MCP to "play track <id>".
func (c *Client) Track(id string) (Track, error) {
	b, err := c.gw("song.getData", fmt.Sprintf(`{"sng_id":%s}`, jsonEsc(id)))
	if err != nil {
		return Track{}, err
	}
	var r struct {
		Results gwTrackDTO `json:"results"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return Track{}, err
	}
	t := r.Results.toTrack()
	if t.ID == "" {
		return Track{}, fmt.Errorf("track %s: not found", id)
	}
	return t, nil
}

// Favorites lists the user's liked songs (gw).
func (c *Client) Favorites() ([]Track, error) {
	return c.gwTrackAll("favorite_song.getList", fmt.Sprintf(`"user_id":%s`, jsonEsc(c.uid())))
}

// Playlists lists the user's own playlists (gw pageProfile). It paginates
// using index/nb (mirrors the style of gw track pagination and AlbumTracks'
// index/limit) until a short page. The previous single-request nb=100 silently
// dropped playlists beyond the first page.
func (c *Client) Playlists() ([]Playlist, error) {
	var out []Playlist
	const plPage = 100
	for index := 0; index < maxTracks; index += plPage {
		body := fmt.Sprintf(`{"user_id":%s,"tab":"playlists","nb":%d,"index":%d}`, jsonEsc(c.uid()), plPage, index)
		b, err := c.gw("deezer.pageProfile", body)
		if err != nil {
			return nil, err
		}
		var r struct {
			Results struct {
				Tab struct {
					Playlists struct {
						Data []struct {
							PlaylistID      json.Number `json:"PLAYLIST_ID"`
							Title           string      `json:"TITLE"`
							NbSong          json.Number `json:"NB_SONG"`
							PlaylistPicture string      `json:"PLAYLIST_PICTURE"`
						} `json:"data"`
					} `json:"playlists"`
				} `json:"TAB"`
			} `json:"results"`
		}
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		page := r.Results.Tab.Playlists.Data
		for _, p := range page {
			n, _ := p.NbSong.Int64()
			out = append(out, Playlist{
				ID:         p.PlaylistID.String(),
				Name:       p.Title,
				TrackCount: int(n),
				ArtworkURL: gwCover(p.PlaylistPicture),
			})
		}
		if len(page) < plPage {
			break
		}
	}
	return out, nil
}

// trackToken fetches the per-track token needed for media URL resolution, plus
// the track's ReplayGain (dB) so playback can be loudness-normalized. GAIN is
// already present in the song.getData payload, so this costs no extra request.
func (c *Client) trackToken(trackID string) (token string, gainDB float64, err error) {
	b, err := c.gw("song.getData", fmt.Sprintf(`{"sng_id":%s}`, jsonEsc(trackID)))
	if err != nil {
		return "", 0, err
	}
	var r struct {
		Results struct {
			TrackToken string      `json:"TRACK_TOKEN"`
			Gain       json.Number `json:"GAIN"`
		} `json:"results"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return "", 0, err
	}
	g, _ := strconv.ParseFloat(r.Results.Gain.String(), 64)
	return r.Results.TrackToken, g, nil
}

// StreamPlan is the resolved CDN URL + track id for decryption.
type StreamPlan struct {
	CDNURL    string
	TrackID   string
	Format    string
	GainDB    float64 // track ReplayGain in dB (0 if unknown)
	Encrypted bool    // false for plain CDN streams (e.g. podcast episodes)
	Preview   bool    // true when this is Deezer's public 30-second preview

	// Refresh re-resolves this plan's CDN URL when the current one expires
	// mid-stream (the CDN answers 403/410 because the signed media URL's token
	// rotated). Optional; populated by Client.PrepareStream so the audio engine
	// can recover a long download without depending on a *deezer.Client. Returns
	// a fresh plan for the same track. nil when re-resolution isn't wired up
	// (podcasts, SDK callers that build a StreamPlan directly). Excluded from
	// JSON since a func can't be serialized.
	Refresh func() (*StreamPlan, error) `json:"-"`
}

// resolveMediaURL turns a track token into an encrypted CDN URL. Transport,
// 5xx and rate-limit problems are retried a couple of times with short backoff
// before returning the error (no silent preview downgrade). Entitlement
// rejections ("no media", not-entitled payloads, or the explicit no-source
// case) are surfaced as ErrNoFullMedia so PrepareStream can choose the 30 s
// preview fallback only for those. Re-login is attempted only for non-transient
// rejections (license token rotation etc).
func (c *Client) resolveMediaURL(trackToken string) (urlStr, format string, err error) {
	licenseToken := c.licTok()
	if licenseToken == "" || trackToken == "" {
		return "", "", fmt.Errorf("missing license or track token")
	}
	// Format order is preference order — Deezer serves the first format the
	// account+track is entitled to. We never request a tier the account isn't
	// entitled to: Deezer's web player omits 320/FLAC for a Deezer Free account
	// (which streams full tracks at 128 kbps, ad-supported) and requests
	// MP3_128/MP3_64/MP3_MISC. Requesting only entitled formats guarantees a free
	// account still resolves the *full* track (not just a preview).
	const (
		f128  = `{"cipher":"BF_CBC_STRIPE","format":"MP3_128"}`
		f64   = `{"cipher":"BF_CBC_STRIPE","format":"MP3_64"}`
		fmisc = `{"cipher":"BF_CBC_STRIPE","format":"MP3_MISC"}`
		f320  = `{"cipher":"BF_CBC_STRIPE","format":"MP3_320"}`
		fflac = `{"cipher":"BF_CBC_STRIPE","format":"FLAC"}`
	)
	c.mu.RLock()
	canHQ, canHiFi := c.canHQ, c.canHiFi
	c.mu.RUnlock()
	var order []string
	if atomic.LoadInt32(&c.quality) == QualityLossless && canHiFi {
		order = append(order, fflac)
	}
	if atomic.LoadInt32(&c.quality) >= QualityHigh && canHQ {
		order = append(order, f320)
	}
	// Always-entitled full-track fallbacks (what a free account is served).
	order = append(order, f128, f64, fmisc)
	formats := strings.Join(order, ",")

	const maxAttempts = 3 // initial + a couple of retries for transients
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		var apiErr string
		urlStr, format, apiErr, err = c.getMediaURL(licenseToken, trackToken, formats)
		if err != nil {
			lastErr = err
			if attempt < maxAttempts-1 && isTransientMediaProblem(err, "") {
				time.Sleep(time.Duration(50*(attempt+1)) * time.Millisecond)
				continue
			}
			return "", "", err
		}
		if urlStr == "" && apiErr != "" {
			lastErr = fmt.Errorf("get_url: %s", apiErr)
			if isTransientMediaProblem(nil, apiErr) {
				if attempt < maxAttempts-1 {
					time.Sleep(time.Duration(50*(attempt+1)) * time.Millisecond)
					continue
				}
				return "", "", lastErr
			}
			// non-transient rejection: try one re-login (license token rotate case)
			if err := c.Login(); err != nil {
				return "", "", fmt.Errorf("get_url: %s (re-login: %w)", apiErr, err)
			}
			urlStr, format, apiErr, err = c.getMediaURL(c.licTok(), trackToken, formats)
			if err != nil {
				return "", "", err
			}
			if urlStr == "" && apiErr != "" {
				// after re-login still rejected: surface as entitlement
				return "", "", fmt.Errorf("%s: %w", apiErr, ErrNoFullMedia)
			}
		}
		if urlStr == "" {
			if apiErr != "" {
				if isTransientMediaProblem(nil, apiErr) {
					return "", "", fmt.Errorf("get_url: %s", apiErr)
				}
				return "", "", fmt.Errorf("%s: %w", apiErr, ErrNoFullMedia)
			}
			return "", "", ErrNoFullMedia
		}
		return urlStr, format, nil
	}
	if lastErr != nil {
		return "", "", lastErr
	}
	return "", "", ErrNoFullMedia
}

// getMediaURL performs one media get_url call. apiErr carries Deezer's own
// rejection (data[0].errors or a non-2xx status); err is transport/decode only.
func (c *Client) getMediaURL(licenseToken, trackToken, formats string) (urlStr, format, apiErr string, err error) {
	body := fmt.Sprintf(`{"license_token":"%s","media":[{"type":"FULL","formats":[%s]}],`+
		`"track_tokens":["%s"]}`, licenseToken, formats, trackToken)

	req, err := http.NewRequest(http.MethodPost, mediaURL, bytes.NewReader([]byte(body)))
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", "", "", classifyNet(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", "", fmt.Sprintf("HTTP %d", resp.StatusCode), nil
	}
	var r struct {
		Data []struct {
			Errors []struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"errors"`
			Media []struct {
				Format  string `json:"format"`
				Sources []struct {
					URL string `json:"url"`
				} `json:"sources"`
			} `json:"media"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return "", "", "", err
	}
	if len(r.Data) > 0 && len(r.Data[0].Media) > 0 && len(r.Data[0].Media[0].Sources) > 0 {
		m := r.Data[0].Media[0]
		return m.Sources[0].URL, m.Format, "", nil
	}
	if len(r.Data) > 0 && len(r.Data[0].Errors) > 0 {
		e := r.Data[0].Errors[0]
		return "", "", fmt.Sprintf("%s (code %d)", e.Message, e.Code), nil
	}
	return "", "", "", nil
}

// trackPreview fetches a track's public 30-second preview URL via the REST API.
// The preview is a plain (unencrypted) MP3 on Deezer's CDN, available without a
// subscription — it is the sanctioned free-tier / anonymous audio source.
func (c *Client) trackPreview(trackID string) (string, error) {
	b, err := c.restGet("/track/" + url.PathEscape(trackID))
	if err != nil {
		return "", err
	}
	var r struct {
		Preview string `json:"preview"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return "", err
	}
	return r.Preview, nil
}

// PrepareStream resolves a track id to a playable stream. It first tries the
// full, entitled track (encrypted CDN URL). When full-track resolution is
// refused with an entitlement rejection (ErrNoFullMedia) it falls back to
// Deezer's public 30-second preview (plain MP3, .Preview==true) so free
// accounts can still play. Transient errors (5xx, transport, rate limits) from
// resolveMediaURL are retried inside the resolver and, if still failing,
// returned as-is — a premium user never gets a 30 s clip during a CDN hiccup.
func (c *Client) PrepareStream(trackID string) (*StreamPlan, error) {
	if !c.LoggedIn() {
		return nil, fmt.Errorf("not logged in")
	}
	tok, gain, err := c.trackToken(trackID)
	if err == nil && tok != "" {
		u, format, mErr := c.resolveMediaURL(tok)
		if mErr == nil {
			// Refresh re-resolves a fresh signed URL for the same track when the
			// CDN token expires mid-download; PrepareStream already retries the
			// media call (re-login on rejection), so re-invoking it is the
			// natural re-resolution path.
			return &StreamPlan{
				CDNURL: u, TrackID: trackID, Format: format, GainDB: gain, Encrypted: true,
				Refresh: func() (*StreamPlan, error) { return c.PrepareStream(trackID) },
			}, nil
		}
		if !errors.Is(mErr, ErrNoFullMedia) {
			// Transient (post-retry) or other non-entitlement error: surface it
			// instead of falling back to a preview clip.
			return nil, mErr
		}
		err = mErr // entitlement rejection: allow preview fallback below
	}
	// Fallback: the public 30-second preview (unencrypted). Encrypted == false
	// so the player and downloader stream it straight through, no Blowfish.
	if prev, pErr := c.trackPreview(trackID); pErr == nil && prev != "" {
		return &StreamPlan{CDNURL: prev, TrackID: trackID, Format: "MP3_128", GainDB: gain, Encrypted: false, Preview: true}, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("no media source (track unavailable for this account)")
}

// TrackIDOf extracts a numeric id from "deezer:track:123", a URL, or "123".
// Returns "" when no valid id is present.
func TrackIDOf(uri string) string {
	uri = strings.TrimSpace(uri)
	// Drop query/fragment first: share URLs append params whose digits would
	// otherwise pollute the id (e.g. ?utm_content=track-<id>, ?host=0).
	if i := strings.IndexAny(uri, "?#"); i >= 0 {
		uri = uri[:i]
	}
	uri = strings.TrimSuffix(uri, "/")
	if i := strings.LastIndexAny(uri, ":/"); i >= 0 {
		uri = uri[i+1:]
	}
	// Valid ids are all digits, optionally with a leading '-' (user uploads).
	digits := strings.TrimPrefix(uri, "-")
	if digits == "" {
		return ""
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return uri
}
