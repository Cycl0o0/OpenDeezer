package control

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxControlResponseBytes = 8 << 20

// Client talks to a control Server over HTTP. It is the shared driver for the
// MCP server and the TUI's remote-play feature: both point it at another
// OpenDeezer client's control API and issue the same commands a local user would.
type Client struct {
	base    string // e.g. http://127.0.0.1:7654
	token   string // X-OpenDeezer-Token (optional)
	account string // X-OpenDeezer-Account (same-account auth, optional)
	http    *http.Client
}

// NewClient builds a control client. base is the server's URL; token/account are
// the credentials (send whichever the server requires; empty ones are omitted).
func NewClient(base, token, account string) *Client {
	return &Client{
		base: base, token: token, account: account,
		http: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) req(method, path string) (*http.Request, error) {
	r, err := http.NewRequest(method, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		r.Header.Set("X-OpenDeezer-Token", c.token)
	}
	if c.account != "" {
		r.Header.Set("X-OpenDeezer-Account", c.account)
	}
	return r, nil
}

// raw issues a request and returns the response body, erroring on non-2xx.
func (c *Client) raw(method, path string) ([]byte, error) {
	req, err := c.req(method, path)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Cap the response so a malicious/compromised peer can't exhaust memory.
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxControlResponseBytes))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("control %s %s: %s: %s", method, path, resp.Status, string(b))
	}
	return b, nil
}

// state issues a request whose response is a State (status + all command endpoints).
func (c *Client) state(method, path string) (State, error) {
	b, err := c.raw(method, path)
	if err != nil {
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return State{}, err
	}
	return st, nil
}

// Whoami fetches the server's identity (name + auth mode).
func (c *Client) Whoami() (Whoami, error) {
	b, err := c.raw(http.MethodGet, "/whoami")
	if err != nil {
		return Whoami{}, err
	}
	var w Whoami
	return w, json.Unmarshal(b, &w)
}

// Status returns the current playback snapshot.
func (c *Client) Status() (State, error) { return c.state(http.MethodGet, "/status") }

// Events subscribes to playback state changes from the peer. The returned
// channel is closed when ctx is cancelled or the event stream disconnects.
// State snapshots are buffered; if a caller falls behind, stale snapshots may
// be discarded in favour of the newest one.
func (c *Client) Events(ctx context.Context) (<-chan State, error) {
	req, err := c.req(http.MethodGet, "/events")
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	req.Header.Set("Accept", "text/event-stream")

	// NewClient's regular HTTP client has a whole-request timeout. Streaming
	// responses must instead live until ctx is cancelled, while retaining the
	// configured transport, redirects and cookie jar.
	streamClient := &http.Client{
		Transport:     c.http.Transport,
		CheckRedirect: c.http.CheckRedirect,
		Jar:           c.http.Jar,
	}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxControlResponseBytes))
		return nil, fmt.Errorf("control %s %s: %s: %s", http.MethodGet, "/events", resp.Status, string(b))
	}

	states := make(chan State, 16)
	go readStateEvents(ctx, resp.Body, states)
	return states, nil
}

func readStateEvents(ctx context.Context, body io.ReadCloser, states chan State) {
	defer close(states)
	defer body.Close()

	// Closing the body unblocks Scanner even for a stream that has gone quiet.
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			_ = body.Close()
		case <-finished:
		}
	}()

	scanner := bufio.NewScanner(body)
	// State includes the queue, so permit the same maximum event size as raw's
	// bounded response reader rather than Scanner's small default token limit.
	scanner.Buffer(make([]byte, 64<<10), maxControlResponseBytes)

	event := ""
	var data strings.Builder
	hasData := false
	discardEvent := false
	dispatch := func() bool {
		defer func() {
			event = ""
			data.Reset()
			hasData = false
			discardEvent = false
		}()
		if discardEvent || event != "state" || !hasData {
			return true
		}

		var state State
		if err := json.Unmarshal([]byte(data.String()), &state); err != nil {
			return true
		}
		return offerLatestState(ctx, states, state)
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if !dispatch() {
				return
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if discardEvent {
			continue
		}

		field, value, ok := strings.Cut(line, ":")
		if !ok {
			value = ""
		} else {
			value = strings.TrimPrefix(value, " ")
		}
		switch field {
		case "event":
			event = value
		case "data":
			additional := len(value)
			if hasData {
				additional++ // newline inserted between consecutive data fields
			}
			if additional > maxControlResponseBytes-data.Len() {
				discardEvent = true
				continue
			}
			if hasData {
				data.WriteByte('\n')
			}
			data.WriteString(value)
			hasData = true
		}
	}

	// Be liberal toward peers that close immediately after the final event
	// without writing its terminating blank line.
	if hasData {
		_ = dispatch()
	}
}

func offerLatestState(ctx context.Context, states chan State, state State) bool {
	select {
	case states <- state:
		return true
	case <-ctx.Done():
		return false
	default:
	}

	// The channel is full. State events are complete snapshots, so replace the
	// oldest queued snapshot rather than allowing a slow reader to wedge stream
	// teardown indefinitely.
	select {
	case <-states:
	default:
	}
	select {
	case states <- state:
	case <-ctx.Done():
		return false
	default:
	}
	return true
}

// The transport / mutation commands below each return the post-command status
// snapshot (which may lag the command by one tick on the server — see act docs).

// PlayPause toggles play/pause on the peer.
func (c *Client) PlayPause() (State, error) { return c.state(http.MethodPost, "/playpause") }
func (c *Client) Next() (State, error)      { return c.state(http.MethodPost, "/next") }
func (c *Client) Prev() (State, error)      { return c.state(http.MethodPost, "/prev") }
func (c *Client) Stop() (State, error)      { return c.state(http.MethodPost, "/stop") }
func (c *Client) Restart() (State, error)   { return c.state(http.MethodPost, "/restart") }
func (c *Client) CycleRepeat() (State, error) {
	return c.state(http.MethodPost, "/repeat")
}
func (c *Client) ToggleShuffle() (State, error) {
	return c.state(http.MethodPost, "/shuffle")
}

// SetRepeat sets the repeat mode on the peer to mode ("off", "all", or "one").
func (c *Client) SetRepeat(mode string) (State, error) {
	return c.state(http.MethodPost, "/repeat?mode="+url.QueryEscape(mode))
}

// SetShuffle sets shuffle on (true) or off (false) on the peer.
func (c *Client) SetShuffle(on bool) (State, error) {
	v := "false"
	if on {
		v = "true"
	}
	return c.state(http.MethodPost, "/shuffle?on="+v)
}
func (c *Client) Seek(ms int64) (State, error) {
	return c.state(http.MethodPost, "/seek?ms="+strconv.FormatInt(ms, 10))
}
func (c *Client) SetVolume(v float64) (State, error) {
	return c.state(http.MethodPost, "/volume?v="+strconv.FormatFloat(v, 'f', -1, 64))
}
func (c *Client) PlayTrack(id string) (State, error) {
	return c.state(http.MethodPost, "/play/track?id="+url.QueryEscape(id))
}
func (c *Client) PlayPlaylist(id string) (State, error) {
	return c.state(http.MethodPost, "/play/playlist?id="+url.QueryEscape(id))
}
func (c *Client) PlayAlbum(id string) (State, error) {
	return c.state(http.MethodPost, "/play/album?id="+url.QueryEscape(id))
}
func (c *Client) PlayMixTrack(id string) (State, error) {
	return c.state(http.MethodPost, "/play/mix/track?id="+url.QueryEscape(id))
}
func (c *Client) PlayMixArtist(id string) (State, error) {
	return c.state(http.MethodPost, "/play/mix/artist?id="+url.QueryEscape(id))
}

// QueueAdd appends a track to the peer's queue, or inserts it immediately
// after the current track when next is true.
func (c *Client) QueueAdd(id string, next bool) (State, error) {
	n := "0"
	if next {
		n = "1"
	}
	return c.state(http.MethodPost, "/queue/add?id="+url.QueryEscape(id)+"&next="+n)
}

func (c *Client) QueueJump(index int) (State, error) {
	return c.state(http.MethodPost, "/queue/jump?i="+strconv.Itoa(index))
}

func (c *Client) QueueRemove(index int) (State, error) {
	return c.state(http.MethodPost, "/queue/remove?i="+strconv.Itoa(index))
}

func (c *Client) QueueMove(from, to int) (State, error) {
	return c.state(http.MethodPost, "/queue/move?from="+strconv.Itoa(from)+"&to="+strconv.Itoa(to))
}

// SetSleepTimer arms the peer's sleep timer: pause after `minutes` (with a
// fade-out), or when the current track ends if endOfTrack is true.
func (c *Client) SetSleepTimer(minutes int, endOfTrack bool) (State, error) {
	q := "/sleep?minutes=" + strconv.Itoa(minutes)
	if endOfTrack {
		q += "&eot=1"
	}
	return c.state(http.MethodPost, q)
}

// CancelSleepTimer disarms the peer's sleep timer.
func (c *Client) CancelSleepTimer() (State, error) {
	return c.state(http.MethodPost, "/sleep?cancel=1")
}

// eqState issues a request whose response is an EQState (GET /eq and POST /eq
// both return the post-command equalizer snapshot).
func (c *Client) eqState(method, path string) (EQState, error) {
	b, err := c.raw(method, path)
	if err != nil {
		return EQState{}, err
	}
	var st EQState
	if err := json.Unmarshal(b, &st); err != nil {
		return EQState{}, err
	}
	return st, nil
}

// EQ returns the peer's equalizer snapshot (enabled, mono downmix, preamp,
// per-band gains, active preset and the preset/band lists).
func (c *Client) EQ() (EQState, error) { return c.eqState(http.MethodGet, "/eq") }

// SetEQ applies a partial equalizer update. params carries any of the POST /eq
// query params — on=0|1, mono=0|1, preset=name, band=N&db=X, preamp=dB — and
// the server applies whichever are present (at least one is required).
func (c *Client) SetEQ(params url.Values) (EQState, error) {
	return c.eqState(http.MethodPost, "/eq?"+params.Encode())
}

// Search returns the raw search-results JSON from the server.
func (c *Client) Search(q string) (json.RawMessage, error) {
	return c.raw(http.MethodGet, "/search?q="+url.QueryEscape(q))
}

// Playlists returns the raw playlists JSON from the server.
func (c *Client) Playlists() (json.RawMessage, error) {
	return c.raw(http.MethodGet, "/playlists")
}

// HistoryRecent returns the peer's recent-listening history as raw JSON.
func (c *Client) HistoryRecent(n int) (json.RawMessage, error) {
	return c.raw(http.MethodGet, "/history/recent?n="+strconv.Itoa(n))
}
