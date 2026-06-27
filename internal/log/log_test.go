package log

import (
	"bytes"
	"strings"
	"testing"
)

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	SetLevel(LevelWarn)
	t.Cleanup(func() { SetOutput(discardReset()); SetLevel(LevelInfo) })

	Debug("d")
	Info("i")
	Warn("w-%d", 1)
	Error("e")

	out := buf.String()
	if strings.Contains(out, "d") && strings.Contains(out, "[DEBUG]") {
		t.Error("debug should be filtered at LevelWarn")
	}
	if strings.Contains(out, "[INFO]") {
		t.Error("info should be filtered at LevelWarn")
	}
	if !strings.Contains(out, "[WARN] w-1") {
		t.Errorf("warn missing: %q", out)
	}
	if !strings.Contains(out, "[ERROR] e") {
		t.Errorf("error missing: %q", out)
	}
}

func TestParseLevel(t *testing.T) {
	cases := map[string]Level{
		"debug": LevelDebug, "INFO": LevelInfo, "warn": LevelWarn,
		"error": LevelError, "off": LevelOff, "bogus": LevelInfo,
	}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestLevelOffSilencesAll(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	SetLevel(LevelOff)
	t.Cleanup(func() { SetOutput(discardReset()); SetLevel(LevelInfo) })
	Error("should-not-appear")
	if buf.Len() != 0 {
		t.Errorf("LevelOff should silence everything, got %q", buf.String())
	}
}

// discardReset returns a throwaway writer to detach the test buffer.
func discardReset() *bytes.Buffer { return &bytes.Buffer{} }
