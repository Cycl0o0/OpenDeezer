// Package history keeps a purely local listening history. Entries are appended
// to <config-dir>/history.jsonl (one JSON object per line) and never leave the
// machine: nothing in this package talks to the network.
//
// The store is safe for one writer (the player reporting plays) plus any
// number of concurrent readers (stats views, the control API). A small
// in-memory index is rebuilt lazily from the file on first use and kept in
// sync by Record.
package history

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/config"
)

// Kind values for Entry.Kind. A song carries "" (the zero value, which is also
// how legacy JSONL lines written before Kind existed decode) or KindTrack;
// KindEpisode marks a podcast episode so native history screens can route a
// replay to the episode player instead of the music-track resolver.
const (
	KindTrack   = "track"
	KindEpisode = "episode"
)

// Entry is one item from the local listening history — a played song or podcast
// episode. Kind marks which: "" (legacy/default) or KindTrack is a song,
// KindEpisode is a podcast episode. Old lines with no "kind" field decode as ""
// and are therefore treated as songs (backward compatible).
type Entry struct {
	TrackID           string `json:"trackId"`
	Title             string `json:"title"`
	Artist            string `json:"artist"`
	Album             string `json:"album,omitempty"`
	Kind              string `json:"kind,omitempty"`    // "" / "track" = song, "episode" = podcast
	StartedAt         int64  `json:"startedAt"`         // unix seconds
	DurationPlayedSec int64  `json:"durationPlayedSec"` // seconds actually listened
}

// isEpisode reports whether this entry is a podcast episode rather than a song.
// Music aggregates (TopTracks/TopArtists) exclude episodes so a binge-listened
// podcast can't masquerade as a top "track" or "artist".
func (e Entry) isEpisode() bool { return e.Kind == KindEpisode }

// TrackStat aggregates plays of one track.
type TrackStat struct {
	TrackID  string
	Title    string
	Artist   string
	Plays    int
	TotalSec int64
}

// ArtistStat aggregates plays of one artist.
type ArtistStat struct {
	Artist   string
	Plays    int
	TotalSec int64
}

// DefaultCap is how many entries the JSONL file is allowed to grow to before
// rotation trims it back to the newest DefaultCap entries.
const DefaultCap = 50000

// Store is a JSONL-backed listening history.
type Store struct {
	path string
	cap  int

	mu      sync.RWMutex
	loaded  bool
	entries []Entry // oldest -> newest
}

// New returns a store backed by the given JSONL file. The file (and its
// directory) is created on the first Record.
func New(path string) *Store {
	return &Store{path: path, cap: DefaultCap}
}

// Default returns the store at <config-dir>/history.jsonl (the same config
// dir resolution the rest of the app uses).
func Default() (*Store, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	return New(filepath.Join(dir, "history.jsonl")), nil
}

// Path returns the backing file path.
func (s *Store) Path() string { return s.path }

// Record appends one entry, durably (single write + fsync). When the file
// exceeds the size cap it is rotated in place: the newest cap entries are
// rewritten atomically (temp file + rename), matching the config package's
// crash-safe write pattern.
func (s *Store) Record(e Entry) error {
	if e.StartedAt == 0 {
		e.StartedAt = time.Now().Unix()
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoadedLocked(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	// One Write call for the whole line so concurrent external readers never
	// see a torn record; Sync so a crash right after Record can't lose it.
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	s.entries = append(s.entries, e)
	if len(s.entries) > s.cap {
		return s.rotateLocked()
	}
	return nil
}

// rotateLocked trims the in-memory list to the newest cap entries and
// atomically rewrites the file to match. Caller holds the write lock.
func (s *Store) rotateLocked() error {
	keep := s.entries[len(s.entries)-s.cap:]
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, filepath.Base(s.path)+".tmp*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after a successful rename
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	w := bufio.NewWriter(tmp)
	for _, e := range keep {
		line, err := json.Marshal(e)
		if err != nil {
			_ = tmp.Close()
			return err
		}
		if _, err := w.Write(append(line, '\n')); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return err
	}
	// Re-slice into a fresh array so the trimmed prefix can be collected.
	s.entries = append([]Entry(nil), keep...)
	return nil
}

// ensureLoadedLocked rebuilds the in-memory index from the file once. Caller
// holds the write lock. Unparseable lines (a torn write from a crash mid-line)
// are skipped rather than poisoning the whole history.
func (s *Store) ensureLoadedLocked() error {
	if s.loaded {
		return nil
	}
	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.loaded = true
			return nil
		}
		return err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var entries []Entry
	for sc.Scan() {
		b := sc.Bytes()
		if len(b) == 0 {
			continue
		}
		var e Entry
		if json.Unmarshal(b, &e) != nil {
			continue // skip corrupt line
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read %s: %w", s.path, err)
	}
	if len(entries) > s.cap {
		entries = append([]Entry(nil), entries[len(entries)-s.cap:]...)
	}
	s.entries = entries
	s.loaded = true
	return nil
}

// load makes sure the index is built, then downgrades to a read lock pattern
// for the query methods.
func (s *Store) load() error {
	s.mu.RLock()
	loaded := s.loaded
	s.mu.RUnlock()
	if loaded {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensureLoadedLocked()
}

// Recent returns the newest n entries, newest first. n <= 0 returns all.
func (s *Store) Recent(n int) ([]Entry, error) {
	if err := s.load(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := len(s.entries)
	if n <= 0 || n > total {
		n = total
	}
	out := make([]Entry, 0, n)
	for i := total - 1; i >= total-n; i-- {
		out = append(out, s.entries[i])
	}
	return out, nil
}

// TopTracks returns the n most-played tracks since the given time (zero time =
// all history), ordered by play count, then total listened time, then title.
// Podcast episodes are excluded — this is a music-only stat.
func (s *Store) TopTracks(since time.Time, n int) ([]TrackStat, error) {
	if err := s.load(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	cut := sinceUnix(since)
	byKey := map[string]*TrackStat{}
	for _, e := range s.entries {
		if e.StartedAt < cut || e.isEpisode() {
			continue
		}
		key := e.TrackID
		if key == "" {
			key = e.Artist + "\x00" + e.Title // tolerate entries without an id
		}
		st, ok := byKey[key]
		if !ok {
			st = &TrackStat{TrackID: e.TrackID, Title: e.Title, Artist: e.Artist}
			byKey[key] = st
		}
		st.Plays++
		st.TotalSec += e.DurationPlayedSec
	}
	s.mu.RUnlock()
	out := make([]TrackStat, 0, len(byKey))
	for _, st := range byKey {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Plays != out[j].Plays {
			return out[i].Plays > out[j].Plays
		}
		if out[i].TotalSec != out[j].TotalSec {
			return out[i].TotalSec > out[j].TotalSec
		}
		return out[i].Title < out[j].Title
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// TopArtists returns the n most-played artists since the given time (zero time
// = all history), ordered by play count, then total listened time, then name.
// Podcast episodes are excluded — this is a music-only stat (a show's name would
// otherwise land in the artist chart).
func (s *Store) TopArtists(since time.Time, n int) ([]ArtistStat, error) {
	if err := s.load(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	cut := sinceUnix(since)
	byArtist := map[string]*ArtistStat{}
	for _, e := range s.entries {
		if e.StartedAt < cut || e.Artist == "" || e.isEpisode() {
			continue
		}
		st, ok := byArtist[e.Artist]
		if !ok {
			st = &ArtistStat{Artist: e.Artist}
			byArtist[e.Artist] = st
		}
		st.Plays++
		st.TotalSec += e.DurationPlayedSec
	}
	s.mu.RUnlock()
	out := make([]ArtistStat, 0, len(byArtist))
	for _, st := range byArtist {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Plays != out[j].Plays {
			return out[i].Plays > out[j].Plays
		}
		if out[i].TotalSec != out[j].TotalSec {
			return out[i].TotalSec > out[j].TotalSec
		}
		return out[i].Artist < out[j].Artist
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// TotalListenedSec sums the seconds listened since the given time (zero time =
// all history). This is total listening time across all media — songs and
// podcast episodes alike (unlike TopTracks/TopArtists, which are music-only).
func (s *Store) TotalListenedSec(since time.Time) (int64, error) {
	if err := s.load(); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	cut := sinceUnix(since)
	var total int64
	for _, e := range s.entries {
		if e.StartedAt >= cut {
			total += e.DurationPlayedSec
		}
	}
	return total, nil
}

// sinceUnix maps a zero time to "everything" and anything else to its unix
// seconds cutoff.
func sinceUnix(since time.Time) int64 {
	if since.IsZero() {
		return 0
	}
	return since.Unix()
}
