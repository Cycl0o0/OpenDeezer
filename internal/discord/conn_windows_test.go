//go:build windows

package discord

import (
	"errors"
	"testing"
)

func TestDialIPCWindowsNotRunning(t *testing.T) {
	_, err := dialIPC()
	if err == nil {
		t.Fatal("expected dialIPC to fail when Discord is not running, but it succeeded")
	}
	if !errors.Is(err, errNoIPC) {
		t.Errorf("expected error %v, got %v", errNoIPC, err)
	}
}
