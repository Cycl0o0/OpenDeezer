package control

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

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
	b, _ := io.ReadAll(resp.Body)
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

// Transport / mutation commands. Each returns the post-command status snapshot
// (which may lag the command by one tick on the server — see act() docs).
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

// Search returns the raw search-results JSON from the server.
func (c *Client) Search(q string) (json.RawMessage, error) {
	return c.raw(http.MethodGet, "/search?q="+url.QueryEscape(q))
}

// Playlists returns the raw playlists JSON from the server.
func (c *Client) Playlists() (json.RawMessage, error) {
	return c.raw(http.MethodGet, "/playlists")
}
