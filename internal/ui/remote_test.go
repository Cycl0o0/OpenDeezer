package ui

import "testing"

func TestNormalizePeer(t *testing.T) {
	cases := []struct{ in, base, hostport string }{
		{"192.168.1.5", "http://192.168.1.5:7654", "192.168.1.5:7654"},
		{"192.168.1.5:9000", "http://192.168.1.5:9000", "192.168.1.5:9000"},
		{"http://host:7654/", "http://host:7654", "host:7654"},
		{"  host  ", "http://host:7654", "host:7654"},
		{"", "", ""},
	}
	for _, c := range cases {
		base, hp := normalizePeer(c.in)
		if base != c.base || hp != c.hostport {
			t.Errorf("normalizePeer(%q) = (%q,%q), want (%q,%q)", c.in, base, hp, c.base, c.hostport)
		}
	}
}
