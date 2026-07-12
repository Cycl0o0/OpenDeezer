// Package mediacache implements an opt-in, on-disk, thread-safe LRU cache for
// raw Deezer CDN stream payloads.
//
// Keys are of the form "trackID.format" (e.g. "12345.MP3_320" or "67890.FLAC").
// The stored bytes are exactly the response body bytes fetched from the CDN
// (stripe-encrypted ciphertext for tracks; plaintext for some podcasts/previews).
// Decryption (via deezer.NewStripeDecryptor) and all higher-level decode/playback
// still occur in the normal pipeline; the cache never stores plaintext audio.
//
// Persistence: entries survive process restarts. On New the directory is
// scanned to rebuild the in-memory index. Recency is approximated by file
// mtime for the initial ordering after a restart; during a run Get/Put update
// both mtime (via Chtimes) and an in-memory sequence number for precise LRU.
//
// Atomicity: Put returns a WriteCloser that writes to a temp file. The entry
// only becomes visible to Get (and is added to the LRU) when Close() returns
// successfully after an atomic rename. Partial writes, crashes, or Close
// without full data leave no visible entry (temps are cleaned on New and on
// failed commits).
//
// Eviction: performed synchronously on successful Put commit. We evict the
// least-recently-used *complete* entries until under maxBytes. We never evict
// a key that currently has an in-progress Put (tracked cheaply via an
// in-memory set). We do not track open Get readers (to avoid refcounting
// overhead); on Unix an unlinked file remains readable by existing fds until
// they are closed. On Windows a busy file may cause eviction of that entry to
// be skipped until the reader closes. This is documented and acceptable for
// the streaming use-case.
//
// Concurrency: all public methods are safe for concurrent use. No background
// goroutines are spawned.
//
// Recovery: New drops zero-length files, temps, and entries whose on-disk size
// does not match the recorded size in the small index.json (if present). A
// corrupt index.json is ignored and the cache is rebuilt from the filesystem.
//
// Usage from the coordinator (see integration notes at end of file):
//   - The cache lives under the normal config dir (e.g. <config.Dir()>/mediacache).
//   - It is OFF by default; a config toggle (size>0) creates it.
//   - In the source download path the body is obtained from cache when possible
//     or wrapped with TeeReader on miss before being fed to decrypt/plain path.
//   - PrepareStream (token dance + CDN URL resolution) can be skipped or
//     short-circuited when a hit is known in advance (the higher layer may keep
//     a tiny side cache of "known cached track+format" metadata).
package mediacache

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// removeFile is os.Remove, indirected so tests can simulate removal failures
// (e.g. a Windows sharing violation while a reader still has the file open).
var removeFile = os.Remove

// Cache is an on-disk LRU cache of raw stream payloads.
type Cache struct {
	dir     string
	max     int64
	mu      sync.Mutex
	entries map[string]entry
	total   int64
	nextSeq uint64
	writing map[string]struct{} // keys with active Put writers (protect from eviction)
}

type entry struct {
	path      string
	size      int64
	accessSeq uint64 // higher = more recent (precise LRU in this process)
}

type indexData struct {
	Entries map[string]indexEntry `json:"entries"`
	NextSeq uint64                `json:"nextSeq"`
}

type indexEntry struct {
	Size      int64  `json:"size"`
	AccessSeq uint64 `json:"accessSeq"`
}

// New creates or opens the cache in dir. maxBytes is the soft limit (entries
// are evicted on Put commits to stay at or below it). dir is created if needed.
// Partial, zero-length, and temp files are cleaned; a corrupt index is ignored.
func New(dir string, maxBytes int64) (*Cache, error) {
	if dir == "" {
		return nil, errors.New("mediacache: empty dir")
	}
	if maxBytes < 0 {
		return nil, errors.New("mediacache: maxBytes must be >= 0")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	c := &Cache{
		dir:     dir,
		max:     maxBytes,
		entries: make(map[string]entry),
		writing: make(map[string]struct{}),
	}
	c.recover()
	return c, nil
}

func (c *Cache) recover() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cleanTempsLocked()

	if ents, seq, ok := c.loadIndexLocked(); ok && len(ents) > 0 {
		c.entries = ents
		c.total = 0
		for _, e := range ents {
			c.total += e.size
		}
		c.nextSeq = seq
		return
	}

	// Fallback: rebuild from filesystem using mtimes for initial recency order.
	c.rebuildFromFSLocked()
}

func (c *Cache) cleanTempsLocked() {
	des, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, de := range des {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if strings.HasSuffix(name, ".tmp") || strings.HasPrefix(name, ".tmp-") ||
			strings.HasPrefix(name, "index.json.tmp") {
			_ = os.Remove(filepath.Join(c.dir, name))
		}
	}
}

func (c *Cache) loadIndexLocked() (map[string]entry, uint64, bool) {
	p := filepath.Join(c.dir, "index.json")
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, 0, false
	}
	var d indexData
	if json.Unmarshal(b, &d) != nil || d.Entries == nil {
		return nil, 0, false
	}
	valid := make(map[string]entry)
	var tot int64
	maxSeq := d.NextSeq
	for k, ie := range d.Entries {
		if k == "" {
			continue
		}
		pth := filepath.Join(c.dir, k)
		fi, serr := os.Stat(pth)
		if serr != nil || fi.Size() != ie.Size {
			_ = os.Remove(pth) // drop partial/corrupt
			continue
		}
		valid[k] = entry{path: pth, size: ie.Size, accessSeq: ie.AccessSeq}
		tot += ie.Size
		if ie.AccessSeq > maxSeq {
			maxSeq = ie.AccessSeq
		}
	}
	return valid, maxSeq + 1, true
}

func (c *Cache) rebuildFromFSLocked() {
	c.entries = make(map[string]entry)
	c.total = 0
	c.nextSeq = 1

	des, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}

	type cand struct {
		key string
		e   entry
		mt  time.Time
	}
	var cands []cand
	for _, de := range des {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if strings.HasSuffix(name, ".tmp") || strings.HasPrefix(name, ".tmp-") ||
			strings.HasPrefix(name, "index.json") {
			continue
		}
		pth := filepath.Join(c.dir, name)
		fi, serr := os.Stat(pth)
		if serr != nil || fi.Size() == 0 {
			_ = os.Remove(pth)
			continue
		}
		cands = append(cands, cand{
			key: name,
			e:   entry{path: pth, size: fi.Size()},
			mt:  fi.ModTime(),
		})
	}

	// sort oldest (smallest mtime) first so we can assign low seqs to LRU
	sort.Slice(cands, func(i, j int) bool { return cands[i].mt.Before(cands[j].mt) })

	for i, cd := range cands {
		cd.e.accessSeq = uint64(i + 1)
		c.entries[cd.key] = cd.e
		c.total += cd.e.size
	}
	if len(cands) > 0 {
		c.nextSeq = uint64(len(cands)) + 1
	}
}

func (c *Cache) saveIndexLocked() {
	d := indexData{
		Entries: make(map[string]indexEntry, len(c.entries)),
		NextSeq: c.nextSeq,
	}
	for k, e := range c.entries {
		d.Entries[k] = indexEntry{Size: e.size, AccessSeq: e.accessSeq}
	}
	b, err := json.Marshal(d)
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(c.dir, "index.json.tmp*")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return
	}
	_ = os.Rename(tmpName, filepath.Join(c.dir, "index.json"))
}

// Get returns a reader for a cached entry (if present), its size, and true.
// A hit bumps the entry to most-recent (both in-memory seq and file mtime).
// The caller must Close the returned ReadCloser.
func (c *Cache) Get(key string) (io.ReadCloser, int64, bool) {
	if key == "" {
		return nil, 0, false
	}
	c.mu.Lock()
	e, ok := c.entries[key]
	if !ok {
		c.mu.Unlock()
		return nil, 0, false
	}
	sz := e.size
	// bump recency
	c.nextSeq++
	e.accessSeq = c.nextSeq
	c.entries[key] = e
	pth := e.path
	c.mu.Unlock()

	// update mtime for restart recency (best-effort)
	now := time.Now()
	_ = os.Chtimes(pth, now, now)

	f, err := os.Open(pth)
	if err != nil {
		// raced with delete or vanished; drop from index
		c.mu.Lock()
		if cur, still := c.entries[key]; still && cur.path == pth {
			delete(c.entries, key)
			c.total -= sz
		}
		c.mu.Unlock()
		_ = os.Remove(pth)
		return nil, 0, false
	}
	return f, sz, true
}

// Put returns a WriteCloser that receives the raw bytes. The bytes are written
// to a temporary file. Only when Close returns nil is the entry atomically
// renamed into place, added to the index, and made visible to Get. The caller
// is responsible for calling Close. If Close is never called (or returns an
// error) no partial entry is left.
func (c *Cache) Put(key string) (io.WriteCloser, error) {
	if key == "" {
		return nil, errors.New("mediacache: empty key")
	}
	c.mu.Lock()
	if _, busy := c.writing[key]; busy {
		c.mu.Unlock()
		return nil, fmt.Errorf("mediacache: put already in progress for %s", key)
	}
	c.writing[key] = struct{}{}
	c.mu.Unlock()

	// Any previous complete entry for this key stays visible (and accounted)
	// until the replacement commits in putWriter.Close: os.Rename atomically
	// swaps the same-key file, so an abandoned or failed rewrite never
	// destroys a good cached copy. The writing marker above keeps the old
	// entry safe from eviction in the meantime.

	tmp, err := os.CreateTemp(c.dir, ".tmp-")
	if err != nil {
		c.unmarkWriting(key)
		return nil, err
	}

	return &putWriter{
		c:     c,
		key:   key,
		tmp:   tmp,
		final: filepath.Join(c.dir, key),
	}, nil
}

type putWriter struct {
	c       *Cache
	key     string
	tmp     *os.File
	final   string
	abort   bool
	closed  bool
	written int64
}

func (w *putWriter) Write(p []byte) (int, error) {
	if w.closed || w.abort {
		return 0, errors.New("mediacache: write after close")
	}
	n, err := w.tmp.Write(p)
	w.written += int64(n)
	return n, err
}

func (w *putWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	defer w.c.unmarkWriting(w.key)

	name := w.tmp.Name()
	_ = w.tmp.Close()

	if w.abort || w.written == 0 {
		_ = os.Remove(name)
		return nil
	}

	// atomic install
	if err := os.Rename(name, w.final); err != nil {
		_ = os.Remove(name)
		return err
	}

	// add under lock
	w.c.mu.Lock()
	if old, ok := w.c.entries[w.key]; ok {
		// Same-key replacement: the rename above already atomically swapped
		// the file (old.path == w.final), so only the accounting moves.
		w.c.total -= old.size
	}
	w.c.total += w.written
	w.c.nextSeq++
	w.c.entries[w.key] = entry{
		path:      w.final,
		size:      w.written,
		accessSeq: w.c.nextSeq,
	}
	w.c.mu.Unlock()

	w.c.evictIfNeeded()
	w.c.mu.Lock()
	w.c.saveIndexLocked()
	w.c.mu.Unlock()
	return nil
}

func (c *Cache) unmarkWriting(key string) {
	c.mu.Lock()
	delete(c.writing, key)
	c.mu.Unlock()
}

// TeeReader returns a reader that consumes src while simultaneously writing a
// copy through the cache under key. On clean EOF the cache entry is committed.
// On any error (or non-EOF termination) the partial cache write is discarded.
// The returned reader does not close src; the caller remains responsible for
// src's lifecycle (typical for http response bodies).
func (c *Cache) TeeReader(key string, src io.Reader) io.Reader {
	if c == nil || key == "" {
		return src
	}
	wc, err := c.Put(key)
	if err != nil {
		return src
	}
	return &teeReader{src: src, wc: wc}
}

type teeReader struct {
	src io.Reader
	wc  io.WriteCloser
	err error
}

func (t *teeReader) Read(p []byte) (int, error) {
	n, rerr := t.src.Read(p)
	if n > 0 && t.err == nil {
		if _, werr := t.wc.Write(p[:n]); werr != nil {
			t.err = werr
		}
	}
	if rerr != nil {
		// decide commit vs discard
		doCommit := (rerr == io.EOF && t.err == nil)
		if pw, ok := t.wc.(*putWriter); ok {
			if !doCommit {
				pw.abort = true
			}
		}
		_ = t.wc.Close()
		return n, rerr
	}
	return n, nil
}

// evictIfNeeded removes least-recent entries (by accessSeq) until under limit.
// It skips keys that are currently being written.
func (c *Cache) evictIfNeeded() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for c.total > c.max && len(c.entries) > 0 {
		var victim string
		var victimSeq uint64
		for k, e := range c.entries {
			if _, busy := c.writing[k]; busy {
				continue
			}
			if victim == "" || e.accessSeq < victimSeq {
				victim = k
				victimSeq = e.accessSeq
			}
		}
		if victim == "" {
			break
		}
		e := c.entries[victim]
		if err := removeFile(e.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			// The file could not be deleted (e.g. Windows sharing violation
			// while a reader has it open). Keep the entry and its accounting
			// intact so the index never desyncs from disk — dropping only the
			// bookkeeping would orphan the file and let recover() reload it
			// past maxBytes. Stop here (a retry would pick the same LRU victim
			// and spin); the next Put commit retries eviction.
			break
		}
		c.total -= e.size
		delete(c.entries, victim)
	}
	// caller will saveIndex after unlock in the commit path
}

// Stats returns the current number of entries and total bytes stored.
func (c *Cache) Stats() (entries int, bytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries), c.total
}

// Clear removes all entries (best-effort) and resets the cache. It does not
// remove the directory itself.
func (c *Cache) Clear() error {
	c.mu.Lock()
	paths := make([]string, 0, len(c.entries))
	for _, e := range c.entries {
		paths = append(paths, e.path)
	}
	c.entries = make(map[string]entry)
	c.total = 0
	c.nextSeq = 1
	// also nuke any stray data files (temps cleaned separately)
	des, _ := os.ReadDir(c.dir)
	for _, de := range des {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if !strings.HasPrefix(name, "index.json") && !strings.HasSuffix(name, ".tmp") && !strings.HasPrefix(name, ".tmp-") {
			paths = append(paths, filepath.Join(c.dir, name))
		}
	}
	c.mu.Unlock()

	for _, p := range paths {
		_ = os.Remove(p)
	}
	_ = os.Remove(filepath.Join(c.dir, "index.json"))

	c.mu.Lock()
	c.saveIndexLocked()
	c.mu.Unlock()
	return nil
}

// -----------------------------------------------------------------------------
// Integration notes for the coordinator (DO NOT edit audio/deezer here).
//
// The cache is deliberately self-contained. Wire it from the layer that owns
// playback and stream plans (today: internal/audio + callers of deezer.Client).
//
// 1. Cache location & toggle
//    dir, _ := config.Dir()
//    cacheDir := filepath.Join(dir, "mediacache")
//    var mc *mediacache.Cache
//    if enabled && max > 0 {
//        mc, _ = mediacache.New(cacheDir, max)
//    }
//
// 2. In the download path (see internal/audio/player.go:804 func (s *source) download())
//    - The HTTP request is built with plan.CDNURL (from deezer.StreamPlan).
//    - Body bytes are fed either plain or through deezer.NewStripeDecryptor.Feed
//      into s.sb.append(...).
//    - To use the cache:
//      key := plan.TrackID + "." + plan.Format   // e.g. "12345.MP3_320"
//      if mc != nil {
//          if rc, _, hit := mc.Get(key); hit {
//              defer rc.Close()
//              // replace the source of bytes with the cached reader
//              // (see loops at ~830 and ~860)
//              src := rc
//              // ... feed from src instead of resp.Body ...
//              return
//          }
//          // miss: perform the request as today
//          resp, err := ...
//          defer resp.Body.Close()
//          var src io.Reader = resp.Body
//          if !plan.Preview {  // or always; previews are small
//              src = mc.TeeReader(key, resp.Body)
//          }
//          // then use src in the existing read loops (plain or decrypt path)
//      }
//
// 3. PrepareStream interaction (internal/deezer/client.go:929)
//    PrepareStream does the (relatively expensive) token + resolveMediaURL dance
//    (or falls back to preview). On a known cache hit the coordinator can avoid
//    the network round-trips for the URL if it also remembers the minimal
//    metadata (format, Encrypted flag, GainDB) that was used when the entry was
//    originally cached. The stream cache itself only stores the body bytes.
//
// 4. Other notes
//    - Do not feed decrypted bytes to the cache.
//    - The existing SaveTrack/SaveAlbum paths (internal/deezer/download.go) write
//      final tagged audio files; they are orthogonal to this raw-stream cache.
//    - Default OFF. Only instantiate when the user has opted in via config.
//    - The package has no dependency on audio or deezer; it is a pure cache.
//
// This design lets the other agent keep the playback pipeline unchanged while
// the cache is integrated in a follow-up step.
