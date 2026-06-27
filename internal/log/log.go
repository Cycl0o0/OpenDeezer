// Package log is a tiny leveled logger for OpenDeezer. It writes to an
// io.Writer (a file in normal use) rather than stdout/stderr so it never
// corrupts the Bubble Tea TUI. Levels are filtered by SetLevel.
package log

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Level is a severity threshold.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
	LevelOff
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "OFF"
	}
}

// ParseLevel maps a name (case-insensitive) to a Level; unknown => LevelInfo.
func ParseLevel(s string) Level {
	switch s {
	case "debug", "DEBUG":
		return LevelDebug
	case "info", "INFO":
		return LevelInfo
	case "warn", "WARN", "warning":
		return LevelWarn
	case "error", "ERROR":
		return LevelError
	case "off", "OFF", "none":
		return LevelOff
	default:
		return LevelInfo
	}
}

var (
	mu    sync.Mutex
	out   io.Writer = io.Discard
	level           = LevelInfo
)

// SetOutput directs log lines to w.
func SetOutput(w io.Writer) {
	mu.Lock()
	out = w
	mu.Unlock()
}

// SetLevel sets the minimum level that is emitted.
func SetLevel(l Level) {
	mu.Lock()
	level = l
	mu.Unlock()
}

// OpenFile points logging at <dir>/opendeezer.log (appended, 0600), honoring the
// OPENDEEZER_LOG env var ("debug"/"info"/...) for the level. Returns the file so
// the caller can Close it; logging is a no-op (discard) on failure.
func OpenFile(dir string) (*os.File, error) {
	if v := os.Getenv("OPENDEEZER_LOG"); v != "" {
		SetLevel(ParseLevel(v))
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "opendeezer.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	SetOutput(f)
	return f, nil
}

func logf(l Level, format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if l < level || level == LevelOff {
		return
	}
	fmt.Fprintf(out, "[%s] %s\n", l, fmt.Sprintf(format, args...))
}

// Debug / Info / Warn / Error emit at their level.
func Debug(format string, args ...any) { logf(LevelDebug, format, args...) }
func Info(format string, args ...any)  { logf(LevelInfo, format, args...) }
func Warn(format string, args ...any)  { logf(LevelWarn, format, args...) }
func Error(format string, args ...any) { logf(LevelError, format, args...) }
