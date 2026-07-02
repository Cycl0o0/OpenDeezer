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

func TestLoadControlDisableValues(t *testing.T) {
	t.Setenv("OPENDEEZER_CONTROL_TOKEN", "")
	for _, v := range []string{"0", "off", "false", "no", "FALSE", "No"} {
		t.Setenv("OPENDEEZER_CONTROL", v)
		if c := LoadControl(); c.Enabled {
			t.Fatalf("OPENDEEZER_CONTROL=%q should disable, got %+v", v, c)
		}
	}
}

func TestNormalizePeer(t *testing.T) {
	cases := []struct {
		in, hostport string
	}{
		{"host", "host:7654"},
		{"host:9000", "host:9000"},
		{"http://host:9000", "host:9000"},
		{"192.168.1.5", "192.168.1.5:7654"},
		{"fd7a:115c:a1e0::42", "[fd7a:115c:a1e0::42]:7654"},
		{"[::1]", "[::1]:7654"},
		{"[::1]:7654", "[::1]:7654"},
		{"::1", "[::1]:7654"},
	}
	for _, c := range cases {
		base, hp := NormalizePeer(c.in)
		if hp != c.hostport || base != "http://"+c.hostport {
			t.Errorf("NormalizePeer(%q) = %q,%q want http://%s,%s", c.in, base, hp, c.hostport, c.hostport)
		}
	}
	if base, hp := NormalizePeer("  "); base != "" || hp != "" {
		t.Errorf("NormalizePeer(empty) = %q,%q want empty", base, hp)
	}
}

func TestLoadDiscordAppIDEnv(t *testing.T) {
	t.Setenv("OPENDEEZER_DISCORD_APP_ID", "12345")
	if LoadDiscordAppID() != "12345" {
		t.Fatal("env app id not read")
	}
}
