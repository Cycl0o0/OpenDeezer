package deezer

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Profile is one member of a Deezer Family plan (deezer.getChildAccounts).
type Profile struct {
	UserID  string `json:"userId"`
	Name    string `json:"name"`
	Picture string `json:"picture"` // Deezer image md5 ("" = none); see PictureURL
	IsKid   bool   `json:"isKid"`
	IsAdmin bool   `json:"isAdmin"` // the plan owner (no PARENT_ID)
	Current bool   `json:"current"` // the profile this session is logged in as
}

// PictureURL returns a square avatar URL for the profile, or "" if it has none.
func (p Profile) PictureURL(size int) string {
	if p.Picture == "" {
		return ""
	}
	return fmt.Sprintf("https://e-cdns-images.dzcdn.net/images/user/%s/%dx%d-000000-80-0-0.jpg", p.Picture, size, size)
}

// validProfileID reports whether id is a plain numeric Deezer user id, so it can
// be embedded in a gw JSON body as a number.
func validProfileID(id string) bool {
	_, err := strconv.ParseUint(id, 10, 64)
	return err == nil
}

// SetProfileID selects the Family profile Login switches to ("" = the account's
// default profile). It does not log in; use SwitchProfile for a live switch.
func (c *Client) SetProfileID(id string) error {
	id = strings.TrimSpace(id)
	if id != "" && !validProfileID(id) {
		return fmt.Errorf("invalid profile id %q", id)
	}
	c.mu.Lock()
	c.profileID = id
	c.mu.Unlock()
	return nil
}

// ProfileID returns the selected Family profile ("" = default).
func (c *Client) ProfileID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profileID
}

// Profiles lists the profiles of the account's Family plan that this session can
// switch to. A non-Family account returns an empty list.
func (c *Client) Profiles() ([]Profile, error) {
	body, err := c.gw("deezer.getChildAccounts", "{}")
	if err != nil {
		return nil, err
	}
	var env struct {
		Results []struct {
			UserID      json.Number `json:"USER_ID"`
			BlogName    string      `json:"BLOG_NAME"`
			Picture     string      `json:"USER_PICTURE"`
			IsKid       bool        `json:"IS_KID"`
			ParentID    json.Number `json:"PARENT_ID"`
			ExtraFamily struct {
				IsLoggableAs bool `json:"IS_LOGGABLE_AS"`
			} `json:"EXTRA_FAMILY"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("parse getChildAccounts: %w", err)
	}
	cur := c.uid()
	out := make([]Profile, 0, len(env.Results))
	for _, r := range env.Results {
		id := r.UserID.String()
		if !r.ExtraFamily.IsLoggableAs || !validProfileID(id) {
			continue
		}
		pid := r.ParentID.String()
		out = append(out, Profile{
			UserID:  id,
			Name:    r.BlogName,
			Picture: r.Picture,
			IsKid:   r.IsKid,
			IsAdmin: pid == "" || pid == "0",
			Current: id == cur,
		})
	}
	return out, nil
}

// SwitchProfile switches the live session to the given Family profile and
// remembers it for future re-logins. On failure the previous profile is
// restored (best effort) and the error returned.
func (c *Client) SwitchProfile(id string) error {
	prev := c.ProfileID()
	if err := c.SetProfileID(id); err != nil {
		return err
	}
	if err := c.Login(); err != nil {
		_ = c.SetProfileID(prev)
		_ = c.Login()
		return err
	}
	return nil
}
