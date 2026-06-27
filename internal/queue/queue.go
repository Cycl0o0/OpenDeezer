// Package queue is the playback queue model shared by the TUI and the C API
// (via corelib), so shuffle/repeat/prev-history behaviour is defined once
// instead of being re-implemented per frontend.
package queue

import (
	"math/rand"

	"github.com/Cycl0o0/OpenDeezer/internal/deezer"
)

// Repeat is the loop mode.
type Repeat int

const (
	RepeatOff Repeat = iota
	RepeatAll
	RepeatOne
)

func (r Repeat) String() string {
	switch r {
	case RepeatAll:
		return "all"
	case RepeatOne:
		return "one"
	default:
		return "off"
	}
}

// Queue holds an ordered track list plus a cursor, shuffle/repeat state and a
// visited-index history (so Prev under shuffle retraces the real path). The zero
// value is a valid empty queue. Not safe for concurrent use; callers serialize.
type Queue struct {
	tracks  []deezer.Track
	index   int
	repeat  Repeat
	shuffle bool
	history []int

	// intn is rand.Intn by default; tests override for determinism.
	intn func(n int) int
}

// New returns an empty queue.
func New() *Queue { return &Queue{index: -1, intn: rand.Intn} }

func (q *Queue) rnd(n int) int {
	if q.intn == nil {
		q.intn = rand.Intn
	}
	return q.intn(n)
}

// Set replaces the queue contents and positions the cursor at start (clamped).
// History is cleared.
func (q *Queue) Set(tracks []deezer.Track, start int) {
	q.tracks = tracks
	q.history = nil
	if len(tracks) == 0 {
		q.index = -1
		return
	}
	if start < 0 {
		start = 0
	} else if start >= len(tracks) {
		start = len(tracks) - 1
	}
	q.index = start
}

// Append adds tracks to the end without moving the cursor.
func (q *Queue) Append(tracks ...deezer.Track) { q.tracks = append(q.tracks, tracks...) }

// Tracks returns the underlying slice (read-only; do not mutate).
func (q *Queue) Tracks() []deezer.Track { return q.tracks }

// Len reports the number of queued tracks.
func (q *Queue) Len() int { return len(q.tracks) }

// Index returns the current cursor (−1 if empty).
func (q *Queue) Index() int { return q.index }

// Current returns the current track and whether one exists.
func (q *Queue) Current() (deezer.Track, bool) {
	if q.index < 0 || q.index >= len(q.tracks) {
		return deezer.Track{}, false
	}
	return q.tracks[q.index], true
}

// SetIndex moves the cursor (clamped); use when the user picks a row directly.
func (q *Queue) SetIndex(i int) {
	if i < 0 || i >= len(q.tracks) {
		return
	}
	q.index = i
}

// Shuffle / Repeat accessors.
func (q *Queue) Shuffle() bool       { return q.shuffle }
func (q *Queue) SetShuffle(on bool)  { q.shuffle = on }
func (q *Queue) ToggleShuffle() bool { q.shuffle = !q.shuffle; return q.shuffle }
func (q *Queue) Repeat() Repeat      { return q.repeat }
func (q *Queue) SetRepeat(r Repeat)  { q.repeat = r }
func (q *Queue) CycleRepeat() Repeat { q.repeat = (q.repeat + 1) % 3; return q.repeat }

// Next advances the cursor following shuffle/repeat rules and reports whether it
// moved to a playable track. RepeatOne is treated as a normal advance here (the
// caller decides whether a natural finish should instead replay current — see
// AdvanceAuto).
func (q *Queue) Next() bool {
	if len(q.tracks) == 0 {
		return false
	}
	q.history = append(q.history, q.index)
	switch {
	case q.shuffle && len(q.tracks) > 1:
		next := q.index
		for next == q.index {
			next = q.rnd(len(q.tracks))
		}
		q.index = next
	case q.index+1 < len(q.tracks):
		q.index++
	case q.repeat == RepeatAll:
		q.index = 0
	default:
		q.history = q.history[:len(q.history)-1] // nothing to advance to
		return false
	}
	return true
}

// Prev steps back, retracing shuffle history when present.
func (q *Queue) Prev() bool {
	if len(q.tracks) == 0 {
		return false
	}
	if n := len(q.history); n > 0 {
		q.index = q.history[n-1]
		q.history = q.history[:n-1]
		return true
	}
	if q.index > 0 {
		q.index--
		return true
	}
	return false
}

// PeekNext returns the track Next would advance to, WITHOUT mutating the queue,
// for the deterministic cases only (linear order, with RepeatAll wrap). Under
// shuffle or RepeatOne it returns ok=false, since the next track isn't fixed —
// callers use this to decide whether a gapless preload is safe.
func (q *Queue) PeekNext() (deezer.Track, bool) {
	if len(q.tracks) == 0 || q.shuffle || q.repeat == RepeatOne {
		return deezer.Track{}, false
	}
	ni := -1
	if q.index+1 < len(q.tracks) {
		ni = q.index + 1
	} else if q.repeat == RepeatAll {
		ni = 0
	}
	if ni < 0 {
		return deezer.Track{}, false
	}
	return q.tracks[ni], true
}

// AdvanceAuto is called when a track ends naturally: RepeatOne replays the
// current track (reports true, cursor unchanged); otherwise it behaves like
// Next. Returns whether playback should continue.
func (q *Queue) AdvanceAuto() bool {
	if q.repeat == RepeatOne {
		_, ok := q.Current()
		return ok
	}
	return q.Next()
}
