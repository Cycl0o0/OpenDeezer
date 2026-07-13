package audio

import (
	"testing"

	"github.com/Cycl0o0/OpenDeezer/v3/internal/deezer"
)

// A source parked after decoder EOF must wake and report a pending seek when one
// is requested (so seeking back into a fully-decoded / tail-of-track source still
// works) and must wake reporting "dead" when killed (so the decode goroutine
// exits instead of leaking).
func TestSourceWaitSeekWakesOnSeek(t *testing.T) {
	s := newSource(&deezer.StreamPlan{Format: "MP3"}, 1000)
	done := make(chan bool, 1)
	go func() { done <- s.waitSeekOrDead() }()
	s.requestSeek(4096)
	if !<-done {
		t.Fatal("waitSeekOrDead should return true (seek pending) after requestSeek")
	}
}

func TestSourceWaitSeekWakesOnKill(t *testing.T) {
	s := newSource(&deezer.StreamPlan{Format: "MP3"}, 1000)
	done := make(chan bool, 1)
	go func() { done <- s.waitSeekOrDead() }()
	s.kill()
	if <-done {
		t.Fatal("waitSeekOrDead should return false (dead) after kill")
	}
	if !s.eof.Load() {
		t.Fatal("kill() must set eof so manage() never waits on a killed source forever")
	}
}
