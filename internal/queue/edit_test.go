package queue

import (
	"testing"

	"github.com/Cycl0o0/OpenDeezer/v3/internal/deezer"
)

func ids(q *Queue) string {
	s := ""
	for _, t := range q.Tracks() {
		s += t.ID
	}
	return s
}

func TestVersionBumpsOnListMutationsOnly(t *testing.T) {
	q := New()
	if q.Version() != 0 {
		t.Fatalf("fresh queue version = %d, want 0", q.Version())
	}
	q.Set(tracks(3), 0)
	v := q.Version()
	if v == 0 {
		t.Fatal("Set should bump the version")
	}
	// Cursor moves must not bump: the list contents are unchanged.
	q.Next()
	q.Prev()
	q.SetIndex(2)
	q.CycleRepeat()
	q.ToggleShuffle()
	q.ToggleShuffle()
	if q.Version() != v {
		t.Fatalf("cursor/mode changes bumped version: %d -> %d", v, q.Version())
	}
	steps := []func(){
		func() { q.Append(deezer.Track{ID: "x"}) },
		func() { q.InsertAfterCurrent(deezer.Track{ID: "y"}) },
		func() { q.Move(0, 1) },
		func() { q.Remove(1) },
	}
	for i, step := range steps {
		before := q.Version()
		step()
		if q.Version() <= before {
			t.Fatalf("mutation %d did not bump version (%d -> %d)", i, before, q.Version())
		}
	}
	// No-op mutations don't bump.
	before := q.Version()
	q.Append()
	q.InsertAfterCurrent()
	q.Move(0, 0)
	q.Remove(99)
	if q.Version() != before {
		t.Fatalf("no-op mutations bumped version: %d -> %d", before, q.Version())
	}
}

func TestRemoveBeforeAfterAndAtCursor(t *testing.T) {
	q := New()
	q.Set(tracks(4), 2) // a b c d, cursor on c

	if !q.Remove(0) { // remove a: cursor must follow c
		t.Fatal("Remove(0) failed")
	}
	if ids(q) != "bcd" || curID(q) != "c" {
		t.Fatalf("after remove-before: %q cur=%q", ids(q), curID(q))
	}
	if !q.Remove(2) { // remove d (after cursor)
		t.Fatal("Remove(2) failed")
	}
	if ids(q) != "bc" || curID(q) != "c" {
		t.Fatalf("after remove-after: %q cur=%q", ids(q), curID(q))
	}
	if !q.Remove(1) { // remove the current track: cursor clamps to the new end
		t.Fatal("Remove(current) failed")
	}
	if ids(q) != "b" || curID(q) != "b" || q.Index() != 0 {
		t.Fatalf("after remove-current: %q cur=%q idx=%d", ids(q), curID(q), q.Index())
	}
	if !q.Remove(0) {
		t.Fatal("Remove(last) failed")
	}
	if q.Len() != 0 || q.Index() != -1 {
		t.Fatalf("emptied queue: len=%d idx=%d", q.Len(), q.Index())
	}
	if q.Remove(0) {
		t.Fatal("Remove on empty queue should report false")
	}
}

func TestRemoveRemapsHistory(t *testing.T) {
	q := New()
	q.Set(tracks(5), 0)
	q.Next() // a->b, history [0]
	q.Next() // b->c, history [0 1]
	if !q.Remove(0) {
		t.Fatal("Remove failed")
	}
	// History entry for the removed track is dropped; b's entry shifted to 0.
	if !q.Prev() || curID(q) != "b" {
		t.Fatalf("Prev after remove -> %q, want b", curID(q))
	}
	if q.Prev() { // the only remaining retrace target (a) was removed
		t.Fatalf("Prev should stop, cursor at %q", curID(q))
	}
}

func TestRemoveUnderShuffleKeepsCycle(t *testing.T) {
	q := New()
	q.Set(tracks(6), 0)
	q.SetShuffle(true)
	q.Next()
	cur := curID(q)
	rm := 5 // remove some other track — never the current row
	if q.Index() == rm {
		rm = 4
	}
	if !q.Remove(rm) {
		t.Fatal("Remove failed")
	}
	if curID(q) != cur {
		t.Fatalf("shuffle remove moved cursor: %q -> %q", cur, curID(q))
	}
	// The remaining cycle still visits every remaining track exactly once.
	seen := map[string]int{curID(q): 1}
	for q.Next() {
		seen[curID(q)]++
	}
	if len(seen) > 5 {
		t.Fatalf("cycle visited %d tracks, max 5", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("track %q visited %d times", id, n)
		}
	}
}

func TestMoveRemapsCursorAndHistory(t *testing.T) {
	q := New()
	q.Set(tracks(5), 0) // a b c d e
	q.Next()            // -> b, history [0]
	if !q.Move(1, 3) {  // move the playing track down: a c d b e
		t.Fatal("Move failed")
	}
	if ids(q) != "acdbe" || curID(q) != "b" || q.Index() != 3 {
		t.Fatalf("after move: %q cur=%q idx=%d", ids(q), curID(q), q.Index())
	}
	if !q.Move(0, 4) { // move a to the end: c d b e a
		t.Fatal("Move failed")
	}
	if ids(q) != "cdbea" || curID(q) != "b" {
		t.Fatalf("after move2: %q cur=%q", ids(q), curID(q))
	}
	// History still retraces to a wherever it lives now.
	if !q.Prev() || curID(q) != "a" {
		t.Fatalf("Prev after moves -> %q, want a", curID(q))
	}
	// Bounds / no-ops.
	if q.Move(0, 0) || q.Move(-1, 2) || q.Move(2, 9) {
		t.Fatal("invalid Move should report false")
	}
}

func TestMoveUnderShuffleKeepsCurrent(t *testing.T) {
	q := New()
	q.Set(tracks(5), 2)
	q.SetShuffle(true)
	cur := curID(q)
	if !q.Move(2, 0) {
		t.Fatal("Move failed")
	}
	if curID(q) != cur || q.Index() != 0 {
		t.Fatalf("shuffle move lost cursor: cur=%q idx=%d", curID(q), q.Index())
	}
	seen := map[string]int{curID(q): 1}
	for q.Next() {
		seen[curID(q)]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("track %q visited %d times after move", id, n)
		}
	}
}

func TestInsertAfterCurrent(t *testing.T) {
	q := New()
	q.Set(tracks(3), 1) // a b c, cursor on b
	at := q.InsertAfterCurrent(deezer.Track{ID: "x"}, deezer.Track{ID: "y"})
	if at != 2 || ids(q) != "abxyc" || curID(q) != "b" {
		t.Fatalf("insert: at=%d %q cur=%q", at, ids(q), curID(q))
	}
	if n, ok := q.PeekNext(); !ok || n.ID != "x" {
		t.Fatalf("PeekNext after insert -> %q ok=%v, want x", n.ID, ok)
	}
	if !q.Next() || curID(q) != "x" {
		t.Fatalf("Next after insert -> %q, want x", curID(q))
	}
	// History (b) still retraces correctly.
	if !q.Prev() || curID(q) != "b" {
		t.Fatalf("Prev -> %q, want b", curID(q))
	}
}

func TestInsertAfterCurrentEmptyQueue(t *testing.T) {
	q := New()
	if at := q.InsertAfterCurrent(deezer.Track{ID: "x"}); at != 0 {
		t.Fatalf("insert into empty at %d, want 0", at)
	}
	if q.Len() != 1 || q.Index() != -1 {
		t.Fatalf("empty-queue insert: len=%d idx=%d", q.Len(), q.Index())
	}
	q.SetIndex(0)
	if curID(q) != "x" {
		t.Fatalf("cur = %q, want x", curID(q))
	}
	if q.InsertAfterCurrent() != -1 {
		t.Fatal("inserting nothing should report -1")
	}
}

func TestInsertAfterCurrentHistoryRemap(t *testing.T) {
	q := New()
	q.Set(tracks(4), 0) // a b c d
	q.Next()            // -> b
	q.Next()            // -> c, history [0 1]
	q.InsertAfterCurrent(deezer.Track{ID: "x"})
	if ids(q) != "abcxd" {
		t.Fatalf("tracks = %q", ids(q))
	}
	if !q.Prev() || curID(q) != "b" {
		t.Fatalf("Prev1 -> %q, want b", curID(q))
	}
	if !q.Prev() || curID(q) != "a" {
		t.Fatalf("Prev2 -> %q, want a", curID(q))
	}
}

func TestInsertAfterCurrentUnderShufflePlaysNext(t *testing.T) {
	q := New()
	q.Set(tracks(5), 0)
	q.SetShuffle(true)
	q.Next()
	q.InsertAfterCurrent(deezer.Track{ID: "x"})
	if !q.Next() || curID(q) != "x" {
		t.Fatalf("shuffle Next after insert -> %q, want x", curID(q))
	}
	// The rest of the cycle still visits every track at most once.
	seen := map[string]int{}
	for q.Next() {
		seen[curID(q)]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("track %q visited %d times", id, n)
		}
	}
}
