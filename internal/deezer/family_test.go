package deezer

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// fakeFamily emulates deezer.com's Family flow: a fresh ARL login opens a sid on
// the default profile, user.loginMulti rebinds that sid to another member, and
// getUserData on the same sid then answers as that member.
type fakeFamily struct {
	sidUser   map[string]string // sid -> USER_ID
	nextSid   int
	members   map[string]bool // USER_IDs loginMulti accepts
	multiBody string          // last user.loginMulti body
	multiCk   string          // last user.loginMulti Cookie header
	ignore    bool            // loginMulti answers OK but doesn't rebind the sid
	onMulti   func()          // hook run while user.loginMulti is in flight
}

func (f *fakeFamily) client(t *testing.T) *Client {
	c := New("arl-x")
	c.http = &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}
		ck := r.Header.Get("Cookie")
		sid := ""
		for _, part := range strings.Split(ck, "; ") {
			if v, ok := strings.CutPrefix(part, "sid="); ok {
				sid = v
			}
		}
		q := r.URL.RawQuery
		switch {
		case strings.Contains(q, "method=deezer.getUserData"):
			resp := jsonResponse(nil)
			if sid == "" {
				f.nextSid++
				sid = fmt.Sprintf("s%d", f.nextSid)
				f.sidUser[sid] = "1"
				resp.Header.Add("Set-Cookie", "sid="+sid+"; Path=/")
			}
			uid := f.sidUser[sid]
			resp.Body = io.NopCloser(strings.NewReader(`{"error":{},"results":{"checkForm":"tok-` + uid +
				`","USER":{"USER_ID":"` + uid + `","BLOG_NAME":"u` + uid + `","OPTIONS":{"license_token":"lic"}}}}`))
			return resp, nil
		case strings.Contains(q, "method=user.loginMulti"):
			f.multiBody, f.multiCk = string(body), ck
			if f.onMulti != nil {
				f.onMulti()
			}
			if f.ignore {
				return jsonResponse([]byte(`{"error":[],"results":true}`)), nil
			}
			var id string
			fmt.Sscanf(string(body), `{"account_id":%s`, &id)
			id = strings.TrimSuffix(id, "}")
			if !f.members[id] || sid == "" {
				return jsonResponse([]byte(`{"error":{"PERMISSION":"denied"},"results":{}}`)), nil
			}
			f.sidUser[sid] = id
			return jsonResponse([]byte(`{"error":[],"results":true}`)), nil
		case strings.Contains(q, "method=deezer.getChildAccounts"):
			return jsonResponse([]byte(`{"error":[],"results":[` +
				`{"USER_ID":"1","BLOG_NAME":"Admin","USER_PICTURE":"aa","IS_KID":false,"PARENT_ID":null,"EXTRA_FAMILY":{"IS_LOGGABLE_AS":true}},` +
				`{"USER_ID":"2","BLOG_NAME":"Member","USER_PICTURE":"","IS_KID":false,"PARENT_ID":1,"EXTRA_FAMILY":{"IS_LOGGABLE_AS":true}},` +
				`{"USER_ID":"3","BLOG_NAME":"Other","IS_KID":true,"PARENT_ID":1,"EXTRA_FAMILY":{"IS_LOGGABLE_AS":false}}]}`)), nil
		}
		t.Fatalf("unexpected request: %s", r.URL)
		return nil, nil
	})}
	return c
}

func newFakeFamily() *fakeFamily {
	return &fakeFamily{sidUser: map[string]string{}, members: map[string]bool{"1": true, "2": true}}
}

func TestLogin_SwitchesToSelectedProfile(t *testing.T) {
	f := newFakeFamily()
	c := f.client(t)
	if err := c.SetProfileID("2"); err != nil {
		t.Fatal(err)
	}
	if err := c.Login(); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got := c.UserID(); got != "2" {
		t.Fatalf("UserID = %q, want 2", got)
	}
	if f.multiBody != `{"account_id":2}` {
		t.Errorf("loginMulti body = %s", f.multiBody)
	}
	if !strings.Contains(f.multiCk, "sid=s1") {
		t.Errorf("loginMulti must reuse the login sid, Cookie = %q", f.multiCk)
	}
	if c.apiTok() != "tok-2" {
		t.Errorf("api token not refreshed for the profile: %q", c.apiTok())
	}
}

func TestLogin_DefaultProfileSkipsLoginMulti(t *testing.T) {
	f := newFakeFamily()
	c := f.client(t)
	if err := c.Login(); err != nil {
		t.Fatal(err)
	}
	if c.UserID() != "1" || f.multiBody != "" {
		t.Fatalf("UserID=%q multiBody=%q", c.UserID(), f.multiBody)
	}
}

func TestLogin_UnavailableProfileFallsBackToDefault(t *testing.T) {
	f := newFakeFamily()
	c := f.client(t)
	_ = c.SetProfileID("99")
	err := c.Login()
	if !errors.Is(err, ErrProfileUnavailable) {
		t.Fatalf("err = %v, want ErrProfileUnavailable", err)
	}
	if !c.LoggedIn() || c.UserID() != "1" {
		t.Fatalf("should stay logged in as default: loggedIn=%v uid=%q", c.LoggedIn(), c.UserID())
	}
	if c.ProfileID() != "" {
		t.Errorf("stale selection not cleared: %q", c.ProfileID())
	}
}

func TestLogin_NeverPublishesDefaultProfileMidSwitch(t *testing.T) {
	f := newFakeFamily()
	c := f.client(t)
	_ = c.SetProfileID("2")
	f.onMulti = func() {
		// A concurrent reader (browse, like…) during the switch must not see the
		// default profile's identity or token.
		if uid, tok := c.UserID(), c.apiTok(); uid == "1" || tok == "tok-1" {
			t.Errorf("default profile published mid-switch: uid=%q tok=%q", uid, tok)
		}
	}
	if err := c.Login(); err != nil {
		t.Fatal(err)
	}
	if c.UserID() != "2" {
		t.Fatalf("UserID = %q, want 2", c.UserID())
	}
}

func TestLogin_IgnoredSwitchIsProfileUnavailable(t *testing.T) {
	f := newFakeFamily()
	f.ignore = true
	c := f.client(t)
	_ = c.SetProfileID("2")
	if err := c.Login(); !errors.Is(err, ErrProfileUnavailable) {
		t.Fatalf("err = %v, want ErrProfileUnavailable", err)
	}
	if c.UserID() != "1" || c.ProfileID() != "" {
		t.Fatalf("uid=%q profile=%q, want default session + cleared selection", c.UserID(), c.ProfileID())
	}
}

func TestSwitchProfile_AndProfiles(t *testing.T) {
	f := newFakeFamily()
	c := f.client(t)
	if err := c.Login(); err != nil {
		t.Fatal(err)
	}
	if err := c.SwitchProfile("2"); err != nil {
		t.Fatalf("SwitchProfile: %v", err)
	}
	ps, err := c.Profiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("want 2 loggable profiles, got %+v", ps)
	}
	if !ps[0].IsAdmin || ps[1].IsAdmin {
		t.Errorf("admin detection wrong: %+v", ps)
	}
	if ps[0].Current || !ps[1].Current {
		t.Errorf("current flag wrong: %+v", ps)
	}
	if ps[0].PictureURL(120) == "" || ps[1].PictureURL(120) != "" {
		t.Errorf("picture urls wrong: %q %q", ps[0].PictureURL(120), ps[1].PictureURL(120))
	}
}

func TestSetProfileID_RejectsNonNumeric(t *testing.T) {
	c := New("x")
	if err := c.SetProfileID(`1,"x":2`); err == nil {
		t.Fatal("expected error for non-numeric id")
	}
	if err := c.SetProfileID(" 42 "); err != nil || c.ProfileID() != "42" {
		t.Fatalf("err=%v id=%q", err, c.ProfileID())
	}
}
