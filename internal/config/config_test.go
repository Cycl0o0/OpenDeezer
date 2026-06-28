package config

import "testing"

func TestIsLoopbackAddr(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:7654", true},
		{"localhost:7654", true},
		{"[::1]:7654", true},
		{"192.168.1.5:7654", false},
		{":7654", false},
		{"0.0.0.0:7654", false},
	}
	for _, c := range cases {
		if got := isLoopbackAddr(c.addr); got != c.want {
			t.Errorf("isLoopbackAddr(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}

func TestLoadControlEnv(t *testing.T) {
	t.Setenv("OPENDEEZER_CONTROL", ":7654")
	t.Setenv("OPENDEEZER_CONTROL_TOKEN", "")
	c := LoadControl()
	if !c.Enabled || c.Addr != ":7654" {
		t.Fatalf("LoadControl = %+v", c)
	}
	if !c.SameAccount {
		t.Fatal("LAN bind without token should default to same-account auth")
	}

	t.Setenv("OPENDEEZER_CONTROL", "1")
	c = LoadControl()
	if !c.Enabled || c.Addr != "127.0.0.1:7654" || c.SameAccount {
		t.Fatalf("localhost LoadControl = %+v", c)
	}
}

func TestLoadDiscordAppIDEnv(t *testing.T) {
	t.Setenv("OPENDEEZER_DISCORD_APP_ID", "12345")
	if LoadDiscordAppID() != "12345" {
		t.Fatal("env app id not read")
	}
}
