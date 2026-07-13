package queue

import (
	"testing"

	"github.com/Cycl0o0/OpenDeezer/v3/internal/deezer"
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

func TestShuffleRepeatOffPlaysEachOnceThenStops(t *testing.T) {
	q := New()
	q.Set(tracks(10), 0)
	q.SetShuffle(true)
	seen := map[string]int{curID(q): 1}
	for q.Next() {
		seen[curID(q)]++
	}
	if len(seen) != 10 {
		t.Fatalf("shuffle RepeatOff should visit all 10 tracks, got %d", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("track %q played %d times, want exactly 1 (no replacement)", id, n)
		}
	}
}

func TestShuffleRepeatAllReshufflesAndContinues(t *testing.T) {
	q := New()
	q.Set(tracks(5), 0)
	q.SetShuffle(true)
	q.SetRepeat(RepeatAll)
	// Advancing far past one cycle must never stop, and must not repeat the
	// just-finished track across a wrap.
	prev := curID(q)
	for i := 0; i < 100; i++ {
		if !q.Next() {
			t.Fatalf("shuffle RepeatAll should never stop (advance %d)", i)
		}
		if curID(q) == prev {
			t.Fatalf("advance %d repeated %q back-to-back", i, prev)
		}
		prev = curID(q)
	}
}

func TestSetIndexRecordsHistory(t *testing.T) {
	q := New()
	q.Set(tracks(10), 0)
	q.Next() // 0->1
	q.Next() // 1->2, now at index 2
	q.SetIndex(7)
	if q.Index() != 7 {
		t.Fatalf("SetIndex -> %d want 7", q.Index())
	}
	if !q.Prev() || q.Index() != 2 {
		t.Fatalf("Prev after pick should retrace to the playing track (2), got %d", q.Index())
	}
}

// TestAdvanceAutoWalksEveryTrack is the contract the engine-side auto-advance
// relies on: repeatedly calling AdvanceAuto on a linear RepeatOff queue visits
// every remaining track exactly once, in order, then stops at the end.
func TestAdvanceAutoWalksEveryTrack(t *testing.T) {
	q := New()
	q.Set(tracks(4), 0) // a,b,c,d starting on a
	var seq []string
	for q.AdvanceAuto() {
		seq = append(seq, curID(q))
	}
	want := []string{"b", "c", "d"}
	if len(seq) != len(want) {
		t.Fatalf("AdvanceAuto walk = %v, want %v", seq, want)
	}
	for i := range want {
		if seq[i] != want[i] {
			t.Fatalf("AdvanceAuto walk[%d]=%q want %q (%v)", i, seq[i], want[i], seq)
		}
	}
	if curID(q) != "d" {
		t.Fatalf("cursor should rest at the last track, got %q", curID(q))
	}
}

func TestCycleRepeat(t *testing.T) {
	q := New()
	if q.CycleRepeat() != RepeatAll || q.CycleRepeat() != RepeatOne || q.CycleRepeat() != RepeatOff {
		t.Fatal("repeat cycle order wrong")
	}
}

// TestAlignIndexIsHistoryFree covers the remote/GUI queue sync pattern:
// Set(tracks, 0) then AlignIndex(N) aligns the cursor to the row that is already
// playing. That alignment must NOT record the synthetic row 0 in history —
// otherwise the first remote Prev jumps to a never-played track.
func TestAlignIndexIsHistoryFree(t *testing.T) {
	q := New()
	q.Set(tracks(10), 0)
	q.AlignIndex(5) // cursor alignment, not a user pick
	if q.Index() != 5 {
		t.Fatalf("AlignIndex -> %d, want 5", q.Index())
	}
	if !q.Prev() {
		t.Fatal("Prev failed")
	}
	if q.Index() == 0 {
		t.Fatal("Prev after alignment visited the never-played row 0")
	}
	if q.Index() != 4 {
		t.Fatalf("Prev with no history should step back linearly to 4, got %d", q.Index())
	}
}

// TestNextAfterSetStillRecordsHistory verifies genuine navigation records
// history: a really-played row 0 followed by a real Next still retraces to 0.
func TestNextAfterSetStillRecordsHistory(t *testing.T) {
	q := New()
	q.Set(tracks(5), 0)
	if !q.Next() || q.Index() != 1 {
		t.Fatalf("Next -> %d, want 1", q.Index())
	}
	if !q.Prev() || q.Index() != 0 {
		t.Fatalf("Prev after a real Next must return to row 0, got %d", q.Index())
	}
}

// TestSetIndexAlwaysRecordsHistory verifies SetIndex (a genuine user pick, e.g.
// the TUI queue view or a remote QueueJump) records history even as the first
// cursor move after a Set — unlike AlignIndex, which is history-free.
func TestSetIndexAlwaysRecordsHistory(t *testing.T) {
	q := New()
	q.Set(tracks(10), 0)
	q.SetIndex(5) // genuine pick: records the row that was playing (0)
	if !q.Prev() || q.Index() != 0 {
		t.Fatalf("Prev after a genuine pick should retrace to 0, got %d", q.Index())
	}

	q2 := New()
	q2.Set(tracks(10), 0)
	q2.SetIndex(4) // records 0
	q2.SetIndex(8) // records 4
	if !q2.Prev() || q2.Index() != 4 {
		t.Fatalf("Prev after the second pick should retrace to 4, got %d", q2.Index())
	}
}

// TestSetIndexAfterAdvanceRecordsHistory verifies a SetIndex following an
// AdvanceLinear/AdvanceAuto records history normally.
func TestSetIndexAfterAdvanceRecordsHistory(t *testing.T) {
	q := New()
	q.Set(tracks(6), 0)
	if !q.AdvanceLinear() || q.Index() != 1 {
		t.Fatalf("AdvanceLinear -> %d, want 1", q.Index())
	}
	q.SetIndex(4)
	if !q.Prev() || q.Index() != 1 {
		t.Fatalf("Prev after AdvanceLinear+pick should retrace to 1, got %d", q.Index())
	}

	q2 := New()
	q2.Set(tracks(6), 0)
	if !q2.AdvanceAuto() || q2.Index() != 1 {
		t.Fatalf("AdvanceAuto -> %d, want 1", q2.Index())
	}
	q2.SetIndex(4)
	if !q2.Prev() || q2.Index() != 1 {
		t.Fatalf("Prev after AdvanceAuto+pick should retrace to 1, got %d", q2.Index())
	}
}
