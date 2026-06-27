package queue

import (
	"testing"

	"github.com/Cycl0o0/OpenDeezer/internal/deezer"
)

func tracks(n int) []deezer.Track {
	ts := make([]deezer.Track, n)
	for i := range ts {
		ts[i] = deezer.Track{ID: string(rune('a' + i)), Name: string(rune('a' + i))}
	}
	return ts
}

func curID(q *Queue) string {
	t, ok := q.Current()
	if !ok {
		return ""
	}
	return t.ID
}

func TestEmptyQueue(t *testing.T) {
	q := New()
	if _, ok := q.Current(); ok {
		t.Fatal("empty queue should have no current")
	}
	if q.Next() || q.Prev() || q.AdvanceAuto() {
		t.Fatal("ops on empty queue should report false")
	}
}

func TestSetClampsStart(t *testing.T) {
	q := New()
	q.Set(tracks(3), 99)
	if q.Index() != 2 {
		t.Fatalf("start clamp: got %d want 2", q.Index())
	}
	q.Set(tracks(3), -5)
	if q.Index() != 0 {
		t.Fatalf("start clamp low: got %d want 0", q.Index())
	}
}

func TestLinearNextStopsAtEnd(t *testing.T) {
	q := New()
	q.Set(tracks(3), 0)
	if !q.Next() || curID(q) != "b" {
		t.Fatalf("next1 -> %q", curID(q))
	}
	if !q.Next() || curID(q) != "c" {
		t.Fatalf("next2 -> %q", curID(q))
	}
	if q.Next() {
		t.Fatal("next past end should return false (RepeatOff)")
	}
	if curID(q) != "c" {
		t.Fatalf("cursor should stay at end, got %q", curID(q))
	}
}

func TestRepeatAllWraps(t *testing.T) {
	q := New()
	q.Set(tracks(2), 1)
	q.SetRepeat(RepeatAll)
	if !q.Next() || curID(q) != "a" {
		t.Fatalf("repeat-all wrap -> %q", curID(q))
	}
}

func TestRepeatOneAutoReplays(t *testing.T) {
	q := New()
	q.Set(tracks(3), 1)
	q.SetRepeat(RepeatOne)
	if !q.AdvanceAuto() || curID(q) != "b" {
		t.Fatalf("repeat-one should replay current, got %q", curID(q))
	}
}

func TestPrevUsesHistory(t *testing.T) {
	q := New()
	q.Set(tracks(5), 0)
	q.Next() // a->b
	q.Next() // b->c
	if !q.Prev() || curID(q) != "b" {
		t.Fatalf("prev1 -> %q", curID(q))
	}
	if !q.Prev() || curID(q) != "a" {
		t.Fatalf("prev2 -> %q", curID(q))
	}
}

func TestShuffleNeverRepeatsCurrentAndRetraces(t *testing.T) {
	q := New()
	q.Set(tracks(4), 0)
	q.SetShuffle(true)
	// Deterministic rng: always returns 2.
	q.intn = func(n int) int { return 2 }
	prev := q.Index()
	if !q.Next() {
		t.Fatal("shuffle next failed")
	}
	if q.Index() == prev {
		t.Fatal("shuffle must not pick the current index")
	}
	// History retrace returns to the start index.
	if !q.Prev() || q.Index() != prev {
		t.Fatalf("shuffle prev should retrace to %d, got %d", prev, q.Index())
	}
}

func TestPeekNext(t *testing.T) {
	q := New()
	q.Set(tracks(3), 0)
	if n, ok := q.PeekNext(); !ok || n.ID != "b" {
		t.Fatalf("peek linear -> %q ok=%v", n.ID, ok)
	}
	q.SetIndex(2) // last, RepeatOff
	if _, ok := q.PeekNext(); ok {
		t.Fatal("peek at end (RepeatOff) should be !ok")
	}
	q.SetRepeat(RepeatAll)
	if n, ok := q.PeekNext(); !ok || n.ID != "a" {
		t.Fatalf("peek wrap -> %q ok=%v", n.ID, ok)
	}
	q.SetRepeat(RepeatOff)
	q.SetShuffle(true)
	q.SetIndex(0)
	if _, ok := q.PeekNext(); ok {
		t.Fatal("peek under shuffle should be !ok")
	}
	// PeekNext must not mutate the cursor.
	q.SetShuffle(false)
	q.SetIndex(0)
	_, _ = q.PeekNext()
	if q.Index() != 0 {
		t.Fatalf("PeekNext mutated cursor: %d", q.Index())
	}
}

func TestCycleRepeat(t *testing.T) {
	q := New()
	if q.CycleRepeat() != RepeatAll || q.CycleRepeat() != RepeatOne || q.CycleRepeat() != RepeatOff {
		t.Fatal("repeat cycle order wrong")
	}
}
