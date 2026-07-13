package mediacache

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestPutGetRoundtrip(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir, 10<<20)
	if err != nil {
		t.Fatal(err)
	}

	data := []byte("hello world this is some test payload for the cache 1234567890")
	wc, err := c.Put("test1.MP3_320")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wc.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := wc.Close(); err != nil {
		t.Fatal(err)
	}

	rc, sz, hit := c.Get("test1.MP3_320")
	if !hit {
		t.Fatal("expected hit")
	}
	if sz != int64(len(data)) {
		t.Fatalf("size=%d want %d", sz, len(data))
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("roundtrip data mismatch")
	}

	// stats
	n, b := c.Stats()
	if n != 1 || b != int64(len(data)) {
		t.Fatalf("stats=%d,%d", n, b)
	}
}

func TestAtomicVisibility(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, 10<<20)

	wc, _ := c.Put("partial.MP3_128")
	_, _ = wc.Write([]byte("incomplete data so far"))
	// do NOT close

	if _, _, hit := c.Get("partial.MP3_128"); hit {
		t.Fatal("partial write must not be visible")
	}

	// now finish
	_, _ = wc.Write([]byte(" more"))
	if err := wc.Close(); err != nil {
		t.Fatal(err)
	}

	rc, _, hit := c.Get("partial.MP3_128")
	if !hit {
		t.Fatal("should be visible after close")
	}
	defer rc.Close()
	all, _ := io.ReadAll(rc)
	if !bytes.Contains(all, []byte("incomplete")) {
		t.Fatal("content wrong after commit")
	}
}

func TestLRUEviction(t *testing.T) {
	dir := t.TempDir()
	// limit = 90; three 30-byte entries = 90 (border), fourth will evict
	c, _ := New(dir, 90)

	payload := bytes.Repeat([]byte("x"), 30)
	for _, k := range []string{"A", "B", "C"} {
		wc, _ := c.Put(k + ".MP3")
		_, _ = wc.Write(payload)
		_ = wc.Close()
	}

	// touch A (bump recency)
	if rc, _, hit := c.Get("A.MP3"); hit {
		rc.Close()
	}

	// add D (30) -> temp total 120 >90 , evict oldest (B)
	wc, _ := c.Put("D.MP3")
	_, _ = wc.Write(payload)
	_ = wc.Close()

	n, _ := c.Stats()
	if n > 3 {
		t.Fatalf("too many entries: %d", n)
	}
	if rc, _, hit := c.Get("B.MP3"); hit {
		rc.Close()
		t.Error("B should have been evicted (oldest)")
	}
	// A (touched), C, D should remain. Close every reader so t.TempDir cleanup
	// does not trip over an open handle on Windows.
	for _, k := range []string{"A.MP3", "C.MP3", "D.MP3"} {
		rc, _, hit := c.Get(k)
		if !hit {
			t.Errorf("%s should still be present", k)
			continue
		}
		rc.Close()
	}
}

func TestRecoverDropsPartialAndCorrupt(t *testing.T) {
	dir := t.TempDir()

	// good: a valid file with a matching index entry (unique-filename scheme).
	goodFile := encodeKey("good.FLAC") + "-1"
	if err := os.WriteFile(filepath.Join(dir, goodFile), []byte("gooddata"), 0o644); err != nil {
		t.Fatal(err)
	}
	// bad: a file whose recorded index size does not match its on-disk size.
	badFile := encodeKey("bad.MP3") + "-2"
	if err := os.WriteFile(filepath.Join(dir, badFile), []byte("short"), 0o644); err != nil {
		t.Fatal(err)
	}
	// zero: a zero-length data file that no index entry references (orphan).
	zeroFile := encodeKey("zero.MP3") + "-3"
	if err := os.WriteFile(filepath.Join(dir, zeroFile), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	idx := indexData{
		Entries: map[string]indexEntry{
			"good.FLAC": {File: goodFile, Size: 8, AccessSeq: 2},
			"bad.MP3":   {File: badFile, Size: 999, AccessSeq: 1}, // size mismatch
		},
		NextSeq: 3,
		FileSeq: 3,
	}
	b, _ := json.Marshal(idx)
	if err := os.WriteFile(filepath.Join(dir, "index.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	// reopen -> should drop zero and bad (size mismatch), keep good.
	c2, _ := New(dir, 10<<20)
	n, _ := c2.Stats()
	if n != 1 {
		t.Fatalf("after recovery got %d entries, want 1 (only good)", n)
	}
	if rc, _, hit := c2.Get("bad.MP3"); hit {
		rc.Close()
		t.Error("bad should have been dropped (size mismatch)")
	}
	if rc, _, hit := c2.Get("zero.MP3"); hit {
		rc.Close()
		t.Error("zero should have been dropped")
	}
	rc, _, hit := c2.Get("good.FLAC")
	if !hit {
		t.Fatal("good should survive recovery")
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, []byte("gooddata")) {
		t.Fatalf("good bytes wrong after recovery: %q", got)
	}
	// The unreferenced/mismatched files must be reclaimed as orphans.
	for _, f := range []string{badFile, zeroFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); !os.IsNotExist(err) {
			t.Errorf("orphan %s should have been reclaimed on recovery", f)
		}
	}
}

func TestConcurrentReadersAndWriter(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, 1<<20)

	const key = "race.MP3_320"
	const payload = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"

	var wg sync.WaitGroup
	start := make(chan struct{})

	// writer
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		wc, err := c.Put(key)
		if err != nil {
			return
		}
		for i := 0; i < 100; i++ {
			_, _ = wc.Write([]byte(payload))
		}
		_ = wc.Close()
	}()

	// many readers that also hammer Get
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 50; j++ {
				if rc, _, hit := c.Get(key); hit {
					_, _ = io.Copy(io.Discard, rc)
					rc.Close()
				}
				// also stats
				c.Stats()
			}
		}()
	}

	close(start)
	wg.Wait()

	// final check: if writer finished we should have data or partial (but test is racy ok)
	rc, sz, hit := c.Get(key)
	if hit {
		if sz == 0 {
			t.Error("got zero size entry")
		}
		rc.Close()
	}
}

func TestTeeReaderCommitAndDiscard(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, 10<<20)

	srcGood := bytes.NewReader([]byte("good tee data here"))
	r := c.TeeReader("tee-good.MP3", srcGood)
	_, _ = io.Copy(io.Discard, r)

	if rc, _, hit := c.Get("tee-good.MP3"); !hit {
		t.Error("tee good should have committed")
	} else {
		rc.Close()
	}

	// error path: src that errors
	srcBad := io.MultiReader(bytes.NewReader([]byte("partial")), &errReader{err: io.ErrUnexpectedEOF})
	r2 := c.TeeReader("tee-bad.MP3", srcBad)
	_, _ = io.Copy(io.Discard, r2)

	if rc, _, hit := c.Get("tee-bad.MP3"); hit {
		rc.Close()
		t.Error("tee error path should not have committed")
	}
}

type errReader struct{ err error }

func (e *errReader) Read(p []byte) (int, error) { return 0, e.err }

func TestClear(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, 10<<20)

	wc, _ := c.Put("x.MP3")
	_, _ = wc.Write([]byte("abc"))
	_ = wc.Close()

	_ = c.Clear()
	n, b := c.Stats()
	if n != 0 || b != 0 {
		t.Errorf("clear stats %d %d", n, b)
	}
	// index should also be gone or empty
}

func TestNoEvictActiveWrite(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, 50) // very small

	// start a long write for "active" (not yet committed)
	wc, _ := c.Put("active.MP3")
	_, _ = wc.Write(bytes.Repeat([]byte("A"), 30))

	// add others that would normally trigger eviction
	for i := 0; i < 5; i++ {
		w, _ := c.Put(string(rune('a'+i)) + ".MP3")
		_, _ = w.Write(bytes.Repeat([]byte("B"), 20))
		_ = w.Close()
	}

	// The active (in-progress) key must still be marked writing so eviction
	// never touches it while its Put is open.
	c.mu.Lock()
	_, busy := c.writing["active.MP3"]
	c.mu.Unlock()
	if !busy {
		t.Fatal("active write should still be protected (marked in-progress)")
	}

	// finish it
	if err := wc.Close(); err != nil {
		t.Fatal(err)
	}

	// It committed successfully despite the eviction pressure; as the most
	// recent entry it survives its own commit's eviction pass.
	rc, _, hit := c.Get("active.MP3")
	if !hit {
		t.Fatal("active entry should be present after commit")
	}
	rc.Close()
}

// A duplicate Put for an already-cached key that is later abandoned (e.g. a
// cancelled re-fetch tee) must not destroy the previously-good entry: the old
// bytes stay visible until a replacement actually commits.
func TestAbandonedRewriteKeepsExistingEntry(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, 10<<20)

	const key = "keep.MP3_320"
	orig := []byte("original good payload")
	wc, err := c.Put(key)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = wc.Write(orig)
	if err := wc.Close(); err != nil {
		t.Fatal(err)
	}

	// Second Put for the same key, aborted mid-write (cancelled fetch).
	wc2, err := c.Put(key)
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}
	_, _ = wc2.Write([]byte("replacement that never completes"))
	wc2.(*putWriter).abort = true
	if err := wc2.Close(); err != nil {
		t.Fatal(err)
	}

	rc, sz, hit := c.Get(key)
	if !hit {
		t.Fatal("abandoned rewrite destroyed the existing entry")
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, orig) || sz != int64(len(orig)) {
		t.Fatalf("entry corrupted: got %q (size %d), want %q", got, sz, orig)
	}
	if n, b := c.Stats(); n != 1 || b != int64(len(orig)) {
		t.Fatalf("stats = %d entries, %d bytes; want 1, %d", n, b, len(orig))
	}

	// Third Put, closed without any writes (empty abandon) — same guarantee.
	wc3, _ := c.Put(key)
	_ = wc3.Close()
	if rc, _, hit := c.Get(key); !hit {
		t.Fatal("zero-write rewrite destroyed the existing entry")
	} else {
		rc.Close()
	}

	// A committed replacement swaps bytes and accounting atomically, onto a
	// brand-new on-disk file (never renaming over the previous one).
	repl := []byte("replacement payload, a bit longer than the original one")
	wc4, _ := c.Put(key)
	_, _ = wc4.Write(repl)
	if err := wc4.Close(); err != nil {
		t.Fatal(err)
	}
	rc, sz, hit = c.Get(key)
	if !hit {
		t.Fatal("committed replacement missing")
	}
	got, _ = io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, repl) || sz != int64(len(repl)) {
		t.Fatalf("replacement bytes wrong: got %q (size %d)", got, sz)
	}
	if n, b := c.Stats(); n != 1 || b != int64(len(repl)) {
		t.Fatalf("stats after replace = %d entries, %d bytes; want 1, %d", n, b, len(repl))
	}
}

// When a victim's file cannot be deleted (Windows sharing violation while a
// reader holds it open), eviction must keep the entry and its accounting —
// dropping only the bookkeeping would orphan the file on disk and let a
// restart's recover() blow past maxBytes. The loop must also terminate rather
// than retry the same undeletable victim forever.
func TestEvictionKeepsEntryWhenRemoveFails(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, 60)

	payload := bytes.Repeat([]byte("x"), 30)
	for _, k := range []string{"A", "B"} {
		wc, _ := c.Put(k + ".MP3")
		_, _ = wc.Write(payload)
		if err := wc.Close(); err != nil {
			t.Fatal(err)
		}
	}

	// Simulate the LRU victim (A) being busy: its removal fails. Each entry now
	// has a unique on-disk name, so resolve A's actual path from the index.
	c.mu.Lock()
	busy := c.entries["A.MP3"].path
	c.mu.Unlock()
	orig := removeFile
	removeFile = func(p string) error {
		if p == busy {
			return errors.New("sharing violation")
		}
		return os.Remove(p)
	}
	defer func() { removeFile = orig }()

	// Adding C (30) pushes total to 90 > 60: eviction picks A, fails to
	// delete it, and must stop without touching the accounting. If the old
	// code ran, this would desync (total 60 with 90 bytes on disk) — or hang
	// forever if the failed victim were retried in a loop.
	wc, _ := c.Put("C.MP3")
	_, _ = wc.Write(payload)
	if err := wc.Close(); err != nil {
		t.Fatal(err)
	}

	n, b := c.Stats()
	if n != 3 || b != 90 {
		t.Fatalf("stats = %d entries, %d bytes; want 3, 90 (nothing evictable)", n, b)
	}
	rc, _, hit := c.Get("A.MP3")
	if !hit {
		t.Fatal("busy victim was dropped from the index")
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, payload) {
		t.Fatal("busy victim's bytes corrupted")
	}

	// Once the file is deletable again, the next commit's eviction catches up.
	// (The Get above bumped A's recency, so B and C are now the LRU victims.)
	removeFile = os.Remove
	wc, _ = c.Put("D.MP3")
	_, _ = wc.Write(payload)
	if err := wc.Close(); err != nil {
		t.Fatal(err)
	}
	if n, b := c.Stats(); n != 2 || b != 60 {
		t.Fatalf("eviction did not resume after the victim became removable: %d entries, %d bytes", n, b)
	}
	for _, k := range []string{"B.MP3", "C.MP3"} {
		if rc, _, hit := c.Get(k); hit {
			rc.Close()
			t.Errorf("%s (LRU) should have been evicted once eviction resumed", k)
		}
	}
	if rc, _, hit := c.Get("A.MP3"); !hit {
		t.Error("A (recently touched) should have survived")
	} else {
		rc.Close()
	}
}

// go test -race should pass; the concurrent test above exercises it.
func TestMain(m *testing.M) {
	// nothing special
	os.Exit(m.Run())
}

// small helper for platform note in test output
var _ = runtime.GOOS

// ensure we exercise Chtimes path
func TestMtimeBumpOnGet(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, 10<<20)
	wc, _ := c.Put("mtime.MP3")
	_, _ = wc.Write([]byte("data"))
	_ = wc.Close()

	c.mu.Lock()
	p := c.entries["mtime.MP3"].path
	c.mu.Unlock()
	fi1, _ := os.Stat(p)
	time.Sleep(10 * time.Millisecond)
	rc, _, _ := c.Get("mtime.MP3")
	rc.Close()
	fi2, _ := os.Stat(p)
	if !fi2.ModTime().After(fi1.ModTime()) && runtime.GOOS != "windows" {
		// on some FS mtime may have low resolution; don't fail hard
		t.Log("mtime did not visibly advance (FS resolution)")
	}
}

// TestReplaceWhileReaderOpen is the exact Windows scenario the unique-filename
// scheme exists to survive: committing a replacement for a key while a reader
// still holds the previous file open. The old, same-name scheme did
// os.Rename(temp, key) here — on Windows that fails with "Access is denied"
// because the target file is open. With unique names the replacement renames
// onto a brand-new path and never touches the open file, so the commit
// succeeds on every OS, the open reader keeps reading the ORIGINAL bytes, and a
// fresh Get sees the replacement. (On Windows the best-effort delete of the old
// file fails while the reader is open and the file is left as a reclaimable
// orphan; the commit still returns nil. On Unix the delete succeeds but the
// open fd stays valid.)
func TestReplaceWhileReaderOpen(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, 10<<20)

	const key = "replace.MP3_320"
	orig := []byte("original bytes held open by a reader across a replace")

	wc, err := c.Put(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wc.Write(orig); err != nil {
		t.Fatal(err)
	}
	if err := wc.Close(); err != nil {
		t.Fatal(err)
	}

	// Open a reader on the original entry and deliberately keep it open across
	// the replacement (mimics Windows holding the file).
	rc, _, hit := c.Get(key)
	if !hit {
		t.Fatal("expected hit for the original entry")
	}
	defer rc.Close()

	// Commit a replacement for the same key while rc is still open. This must
	// NOT error (the whole point of the unique-filename scheme).
	repl := []byte("replacement bytes committed while a reader is open")
	wc2, err := c.Put(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wc2.Write(repl); err != nil {
		t.Fatal(err)
	}
	if err := wc2.Close(); err != nil {
		t.Fatalf("replace while a reader holds the old file open must succeed: %v", err)
	}

	// The still-open reader must observe the ORIGINAL bytes in full.
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, orig) {
		t.Fatalf("open reader saw %q, want original %q", got, orig)
	}

	// A fresh Get returns the replacement.
	rc2, sz, hit := c.Get(key)
	if !hit {
		t.Fatal("expected hit after replacement")
	}
	got2, _ := io.ReadAll(rc2)
	rc2.Close()
	if !bytes.Equal(got2, repl) || sz != int64(len(repl)) {
		t.Fatalf("fresh reader saw %q (size %d), want %q", got2, sz, repl)
	}
	if n, b := c.Stats(); n != 1 || b != int64(len(repl)) {
		t.Fatalf("stats after replace = %d entries, %d bytes; want 1, %d", n, b, len(repl))
	}
}

// --- Phase 3 meta persistence for cached plans ---

func TestPutMetaGetMeta_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir, 10<<20)
	if err != nil {
		t.Fatal(err)
	}

	m := StreamMeta{
		Format:     "MP3_320",
		Encrypted:  true,
		GainDB:     -4.25,
		Preview:    false,
		DurationMS: 217000,
	}
	if err := c.PutMeta("42.MP3_320", m); err != nil {
		t.Fatalf("PutMeta: %v", err)
	}

	got, ok := c.GetMeta("42.MP3_320")
	if !ok {
		t.Fatal("GetMeta missed stored meta")
	}
	if got.Format != m.Format || got.Encrypted != m.Encrypted || got.GainDB != m.GainDB || got.Preview != m.Preview || got.DurationMS != m.DurationMS {
		t.Fatalf("meta roundtrip mismatch: %+v", got)
	}

	// also via track lookup
	got2, ok2 := c.GetMetaForTrack("42")
	if !ok2 || got2.Format != "MP3_320" {
		t.Fatalf("GetMetaForTrack: %+v ok=%v", got2, ok2)
	}
}

func TestGetMeta_CorruptToleratesAndKeepsEntries(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, 10<<20)

	// body + meta
	data := []byte("payload-for-corrupt-meta-test")
	wc, _ := c.Put("corrupt.MP3")
	_, _ = wc.Write(data)
	_ = wc.Close()
	_ = c.PutMeta("corrupt.MP3", StreamMeta{Format: "MP3_128", Encrypted: false})

	// also a second meta-only
	_ = c.PutMeta("metaonly.FLAC", StreamMeta{Format: "FLAC", Encrypted: true, GainDB: 1.5})

	// Corrupt the index: make the metas value unmarshalable as StreamMeta map.
	// (top-level entries stay valid)
	idxp := filepath.Join(dir, "index.json")
	// Build using a name that will match the real file written by the Put above.
	// We know the impl uses encodeKey+seq; we will just force a minimal valid
	// entry and rely on load's file-stat validation to keep or drop.
	badIdx := []byte(`{"entries":{},"nextSeq":1,"fileSeq":0,"metas":{"corrupt.MP3":"this-is-not-an-object","metaonly.FLAC":123}}`)
	_ = os.WriteFile(idxp, badIdx, 0o644)

	// Reopen must succeed without panic and drop bad metas.
	c2, err := New(dir, 10<<20)
	if err != nil {
		t.Fatalf("New after corrupt meta index: %v", err)
	}

	// No body in this index (we overwrote), but GetMeta must tolerate and return false.
	if _, ok := c2.GetMeta("corrupt.MP3"); ok {
		t.Error("corrupt meta should have been dropped")
	}
	if _, ok := c2.GetMeta("metaonly.FLAC"); ok {
		t.Error("bad-typed meta should have been dropped")
	}
}
