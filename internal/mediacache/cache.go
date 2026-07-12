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
// On-disk filenames: every committed entry gets its OWN unique filename
// (a hex-encoding of the key plus a monotonic sequence, e.g.
// "31322e4d50335f333230-2a"). The actual path is stored in the index entry
// and Get opens exactly that path. Replacing a key never reuses or overwrites
// the previous file — a new commit renames its temp to a brand-new name and
// only then swaps the index pointer, leaving the old file to be reclaimed
// best-effort. This is what makes replace/evict safe on Windows, where an open
// file cannot be renamed over or deleted (see TestReplaceWhileReaderOpen).
//
// Atomicity: Put returns a WriteCloser that writes to a temp file. The entry
// only becomes visible to Get (and is added to the LRU) when Close() returns
// successfully after an atomic rename to a fresh, unique final name. Partial
// writes, crashes, or Close without full data leave no visible entry (temps
// are cleaned on New and on failed commits).
//
// Eviction: performed synchronously on successful Put commit. We evict the
// least-recently-used *complete* entries until under maxBytes. We never evict
// a key that currently has an in-progress Put (tracked cheaply via an
// in-memory set). We do not track open Get readers (to avoid refcounting
// overhead); on Unix an unlinked file remains readable by existing fds until
// they are closed. On Windows a busy file cannot be deleted, so that victim's
// eviction (and the best-effort removal of a replaced entry's old file) is
// skipped and the file is left as an orphan; the next New reclaims it once no
// reader holds it. This never blocks a commit or a rename.
//
// Concurrency: all public methods are safe for concurrent use. No background
// goroutines are spawned.
//
// Recovery: New rebuilds the index from index.json (falling back to a
// filesystem scan if it is missing or corrupt), drops entries whose file is
// missing or size-mismatched, and reclaims (best-effort deletes) any data file
// that is not referenced by a live index entry — orphans left by a crashed
// replace or by a delete that a reader was blocking. An undeletable orphan is
// left in place and retried on the next New.
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
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// removeFile is os.Remove, indirected so tests can simulate removal failures
// (e.g. a Windows sharing violation while a reader still has the file open).
var removeFile = os.Remove

// fileSep separates the hex-encoded key from the monotonic sequence in an
// on-disk filename. Both sides are drawn from the hex alphabet ([0-9a-f]) plus
// the sequence's decimal-less hex digits, so the separator is unambiguous and
// no data filename can collide with "index.json", "index.json.tmp*", "*.tmp",
// or ".tmp-*" (temps used by CreateTemp), keeping the recovery scan simple.
const fileSep = "-"

// encodeKey hex-encodes an arbitrary cache key into a filesystem-safe token.
// Hex is fully reversible, so a filesystem-only rebuild (when index.json is
// absent or corrupt) can recover the original key from the filename.
func encodeKey(key string) string { return hex.EncodeToString([]byte(key)) }

// parseFileName reverses the "<hexKey>-<hexSeq>" scheme. ok is false for names
// that are not ours (temps, index files, or anything not matching the scheme).
func parseFileName(name string) (key string, seq uint64, ok bool) {
	i := strings.LastIndex(name, fileSep)
	if i <= 0 || i == len(name)-1 {
		return "", 0, false
	}
	kb, err := hex.DecodeString(name[:i])
	if err != nil {
		return "", 0, false
	}
	seq, err = strconv.ParseUint(name[i+1:], 16, 64)
	if err != nil {
		return "", 0, false
	}
	return string(kb), seq, true
}

// newFilePath allocates a fresh, unique on-disk path for a commit of key. It
// never returns the name of an existing file, so os.Rename onto it can never
// overwrite (and thus never fail against) a file a reader currently holds open.
func (c *Cache) newFilePath(key string) string {
	enc := encodeKey(key)
	for {
		c.mu.Lock()
		c.fileSeq++
		seq := c.fileSeq
		c.mu.Unlock()
		p := filepath.Join(c.dir, enc+fileSep+strconv.FormatUint(seq, 16))
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			return p
		}
		// Extremely unlikely collision with a surviving orphan from a prior
		// run whose index was lost: bump the sequence and try again.
	}
}

// Cache is an on-disk LRU cache of raw stream payloads.
type Cache struct {
	dir     string
	max     int64
	mu      sync.Mutex
	entries map[string]entry
	total   int64
	nextSeq uint64
	fileSeq uint64              // monotonic; makes every committed file name unique
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
	FileSeq uint64                `json:"fileSeq"`
}

type indexEntry struct {
	File      string `json:"file"` // on-disk basename (unique per commit)
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

	if ents, seq, fseq, ok := c.loadIndexLocked(); ok && len(ents) > 0 {
		c.entries = ents
		c.total = 0
		for _, e := range ents {
			c.total += e.size
		}
		c.nextSeq = seq
		c.fileSeq = fseq
	} else {
		// Fallback: rebuild from filesystem using mtimes for initial recency
		// order (index.json missing or corrupt).
		c.rebuildFromFSLocked()
	}

	// Any data file not referenced by a live entry is an orphan: a leftover
	// from a crashed replace, or a file whose delete a reader was blocking.
	c.reclaimOrphansLocked()
}

// reclaimOrphansLocked best-effort deletes every data file in the cache dir
// that is not referenced by a live index entry. Index/temp files are left to
// their own cleanup. An undeletable orphan (e.g. a reader still holds it open
// on Windows) is left in place; the next New retries it.
func (c *Cache) reclaimOrphansLocked() {
	live := make(map[string]struct{}, len(c.entries))
	var maxSeq uint64
	for _, e := range c.entries {
		live[filepath.Base(e.path)] = struct{}{}
	}
	des, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, de := range des {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if strings.HasPrefix(name, "index.json") ||
			strings.HasSuffix(name, ".tmp") || strings.HasPrefix(name, ".tmp-") {
			continue
		}
		// Keep fileSeq above any sequence still present on disk so a future
		// commit can never regenerate a name that collides with an orphan.
		if _, seq, ok := parseFileName(name); ok && seq > maxSeq {
			maxSeq = seq
		}
		if _, ok := live[name]; ok {
			continue
		}
		// Orphan: best-effort delete. If a reader still holds it open (Windows)
		// the remove fails and we simply leave it — the next New retries it.
		_ = removeFile(filepath.Join(c.dir, name))
	}
	if maxSeq >= c.fileSeq {
		c.fileSeq = maxSeq
	}
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

func (c *Cache) loadIndexLocked() (ents map[string]entry, nextSeq, fileSeq uint64, ok bool) {
	p := filepath.Join(c.dir, "index.json")
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, 0, 0, false
	}
	var d indexData
	if json.Unmarshal(b, &d) != nil || d.Entries == nil {
		return nil, 0, 0, false
	}
	valid := make(map[string]entry)
	maxSeq := d.NextSeq
	maxFileSeq := d.FileSeq
	for k, ie := range d.Entries {
		if k == "" || ie.File == "" {
			continue
		}
		pth := filepath.Join(c.dir, ie.File)
		fi, serr := os.Stat(pth)
		if serr != nil || fi.Size() != ie.Size {
			// Missing or size-mismatched: drop the entry. The (possibly
			// corrupt) file is now unreferenced and reclaimOrphansLocked
			// deletes it.
			continue
		}
		valid[k] = entry{path: pth, size: ie.Size, accessSeq: ie.AccessSeq}
		if ie.AccessSeq > maxSeq {
			maxSeq = ie.AccessSeq
		}
		if _, seq, pok := parseFileName(ie.File); pok && seq > maxFileSeq {
			maxFileSeq = seq
		}
	}
	return valid, maxSeq + 1, maxFileSeq, true
}

func (c *Cache) rebuildFromFSLocked() {
	c.entries = make(map[string]entry)
	c.total = 0
	c.nextSeq = 1
	c.fileSeq = 0

	des, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}

	type cand struct {
		e   entry
		mt  time.Time
		seq uint64
	}
	// Multiple files may map to the same key (orphans from a crashed replace).
	// Keep the newest (highest sequence) per key; the rest stay unreferenced
	// and are reclaimed by reclaimOrphansLocked.
	best := make(map[string]cand)
	for _, de := range des {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if strings.HasSuffix(name, ".tmp") || strings.HasPrefix(name, ".tmp-") ||
			strings.HasPrefix(name, "index.json") {
			continue
		}
		key, seq, ok := parseFileName(name)
		if !ok {
			continue // not one of ours; reclaimOrphansLocked handles it
		}
		if seq > c.fileSeq {
			c.fileSeq = seq
		}
		pth := filepath.Join(c.dir, name)
		fi, serr := os.Stat(pth)
		if serr != nil || fi.Size() == 0 {
			continue // zero-length/partial; leave for reclaimOrphansLocked
		}
		if ex, seen := best[key]; !seen || seq > ex.seq {
			best[key] = cand{
				e:   entry{path: pth, size: fi.Size()},
				mt:  fi.ModTime(),
				seq: seq,
			}
		}
	}

	// sort oldest (smallest mtime) first so we can assign low seqs to LRU
	cands := make([]cand, 0, len(best))
	for _, cd := range best {
		cands = append(cands, cd)
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mt.Before(cands[j].mt) })

	for i, cd := range cands {
		key, _, _ := parseFileName(filepath.Base(cd.e.path))
		cd.e.accessSeq = uint64(i + 1)
		c.entries[key] = cd.e
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
		FileSeq: c.fileSeq,
	}
	for k, e := range c.entries {
		d.Entries[k] = indexEntry{File: filepath.Base(e.path), Size: e.size, AccessSeq: e.accessSeq}
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
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
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
	// until the replacement commits in putWriter.Close, which renames its temp
	// to a BRAND-NEW unique file and only then swaps the index pointer. The old
	// file is never overwritten or deleted on this path (only best-effort
	// afterwards), so an abandoned or failed rewrite never destroys a good
	// cached copy and a reader holding the old file is never disturbed. The
	// writing marker above keeps the old entry safe from eviction meanwhile.

	tmp, err := os.CreateTemp(c.dir, ".tmp-")
	if err != nil {
		c.unmarkWriting(key)
		return nil, err
	}

	return &putWriter{
		c:   c,
		key: key,
		tmp: tmp,
	}, nil
}

type putWriter struct {
	c       *Cache
	key     string
	tmp     *os.File
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

	// Atomic install onto a BRAND-NEW unique name. Because final never names an
	// existing file, this rename can never overwrite (and thus never fail
	// against) a file a concurrent reader holds open — the key Windows fix.
	final := w.c.newFilePath(w.key)
	if err := os.Rename(name, final); err != nil {
		_ = os.Remove(name)
		return err
	}

	// Swap the index pointer to the new file under lock; move the accounting.
	w.c.mu.Lock()
	old, hadOld := w.c.entries[w.key]
	if hadOld {
		w.c.total -= old.size
	}
	w.c.total += w.written
	w.c.nextSeq++
	w.c.entries[w.key] = entry{
		path:      final,
		size:      w.written,
		accessSeq: w.c.nextSeq,
	}
	w.c.mu.Unlock()

	// Best-effort remove the OLD file now that no live entry references it. A
	// reader may still hold it open (guaranteed to fail on Windows); if so we
	// leave it as an orphan for the next New's recover() to reclaim. Whatever
	// the outcome (removed, already gone, or open-and-undeletable) we never
	// fail the commit on it.
	if hadOld && old.path != final {
		_ = removeFile(old.path)
	}

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
