package audio

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/deezer"
)

type mockOutput struct {
	lostHandler func(string)
}

func (m *mockOutput) start(read func(out []byte) int) error { return nil }
func (m *mockOutput) devices() ([]Device, error)            { return nil, nil }
func (m *mockOutput) setDevice(id string) error             { return nil }
func (m *mockOutput) currentDevice() string                 { return "" }
func (m *mockOutput) suspend(on bool) error                 { return nil }
func (m *mockOutput) setLostHandler(fn func(string))        { m.lostHandler = fn }
func (m *mockOutput) close()                                {}
func (m *mockOutput) deviceDown() bool                      { return false }
func (m *mockOutput) latencyFrames() int                    { return 0 }

func TestB4ErroredFinish(t *testing.T) {
	out := &mockOutput{}
	p := &Player{out: out, stopMgr: make(chan struct{})}
	p.state.Store(int32(Stopped))
	p.lastErr.Store("")
	p.format.Store("")
	p.gainFac.Store(math.Float64bits(1))
	p.sleepGain.Store(math.Float64bits(1))
	p.gapless.Store(true)
	p.setVolume(1.0)
	p.out.setLostHandler(p.onDeviceLost)
	go p.manage()
	defer p.Close()

	src := newSource(&deezer.StreamPlan{Format: "MP3"}, 1000)
	src.setErr(fmt.Errorf("test decode error"))
	src.eof.Store(true)

	p.cur.Store(src)
	p.state.Store(int32(Playing))

	finishedCalled := make(chan bool, 1)
	p.SetOnFinish(func() {
		finishedCalled <- true
	})

	select {
	case <-finishedCalled:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("onFinish was not called on errored finish")
	}

	if p.State() != Errored {
		t.Errorf("player state = %v, want Errored", p.State())
	}
	if p.LastError() != "test decode error" {
		t.Errorf("player lastErr = %q, want %q", p.LastError(), "test decode error")
	}
}

func TestB16SetOnFinishRace(t *testing.T) {
	out := &mockOutput{}
	p := &Player{out: out, stopMgr: make(chan struct{})}
	p.state.Store(int32(Stopped))
	p.lastErr.Store("")
	p.format.Store("")
	p.gainFac.Store(math.Float64bits(1))
	p.sleepGain.Store(math.Float64bits(1))
	p.gapless.Store(true)
	p.setVolume(1.0)
	p.out.setLostHandler(p.onDeviceLost)
	go p.manage()
	defer p.Close()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			p.SetOnFinish(func() {})
			p.SetOnFinish(nil)
		}
		close(done)
	}()

	src := newSource(&deezer.StreamPlan{Format: "MP3"}, 1000)
	p.cur.Store(src)
	p.state.Store(int32(Playing))

	<-done
}
