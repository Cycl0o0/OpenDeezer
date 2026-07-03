package deezer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"syscall"
	"testing"
)

// TestClassifyNetTagsTransportErrors verifies that the transport failures the
// http.Client actually surfaces (all wrapped in *url.Error by net/http) are
// tagged ErrNoNetwork, while application-level errors are left untouched.
func TestClassifyNetTagsTransportErrors(t *testing.T) {
	// How net/http wraps things: dial/read errors come back as *url.Error whose
	// chain reaches *net.OpError -> syscall.Errno / *net.DNSError, and the client
	// timeout surfaces as context.DeadlineExceeded.
	network := []struct {
		name string
		err  error
	}{
		{"conn refused", &url.Error{Op: "Post", URL: gwURL,
			Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}},
		{"host unreachable", &url.Error{Op: "Get", URL: restURL,
			Err: &net.OpError{Op: "dial", Err: syscall.EHOSTUNREACH}}},
		{"dns failure", &url.Error{Op: "Post", URL: gwURL,
			Err: &net.OpError{Op: "dial", Err: &net.DNSError{IsNotFound: true}}}},
		{"bare dns error", &net.DNSError{IsNotFound: true}},
		{"client timeout", &url.Error{Op: "Post", URL: gwURL, Err: context.DeadlineExceeded}},
		{"deadline", context.DeadlineExceeded},
	}
	for _, tc := range network {
		got := classifyNet(tc.err)
		if !errors.Is(got, ErrNoNetwork) {
			t.Errorf("%s: classifyNet did not tag ErrNoNetwork: %v", tc.name, got)
		}
		if IsARLExpired(got) {
			t.Errorf("%s: network error must never look like an expired ARL", tc.name)
		}
	}

	// Application-level failures are NOT connectivity problems and must pass
	// through unchanged so the caller's own handling still applies.
	app := []error{
		ErrARLExpired,
		fmt.Errorf("deezer gw deezer.getUserData: %s", "QUOTA"),
		fmt.Errorf("deezer rest /user/me: HTTP 403"),
		errors.New("not logged in"),
	}
	for _, e := range app {
		if got := classifyNet(e); IsNoNetwork(got) {
			t.Errorf("app error wrongly tagged as no-network: %v", e)
		}
	}

	if classifyNet(nil) != nil {
		t.Error("classifyNet(nil) must stay nil")
	}
	// The two sentinels are mutually exclusive by construction.
	if IsNoNetwork(ErrARLExpired) || IsARLExpired(ErrNoNetwork) {
		t.Error("ErrNoNetwork and ErrARLExpired must not alias each other")
	}
}
