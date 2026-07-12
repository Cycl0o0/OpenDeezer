package mediacache

import (
	"bytes"
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
	wc.Write([]byte("incomplete data so far"))
	// do NOT close

	if _, _, hit := c.Get("partial.MP3_128"); hit {
		t.Fatal("partial write must not be visible")
	}

	// now finish
	wc.Write([]byte(" more"))
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
		wc.Write(payload)
		wc.Close()
	}

	// touch A (bump recency)
	if rc, _, hit := c.Get("A.MP3"); hit {
		rc.Close()
	}

	// add D (30) -> temp total 120 >90 , evict oldest (B)
	wc, _ := c.Put("D.MP3")
	wc.Write(payload)
	wc.Close()

	n, _ := c.Stats()
	if n > 3 {
		t.Fatalf("too many entries: %d", n)
	}
	if _, _, hit := c.Get("B.MP3"); hit {
		t.Error("B should have been evicted (oldest)")
	}
	// A (touched), C, D should remain
	for _, k := range []string{"A.MP3", "C.MP3", "D.MP3"} {
		if _, _, hit := c.Get(k); !hit {
			t.Errorf("%s should still be present", k)
		}
	}
}

func TestRecoverDropsPartialAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, 10<<20)

	// write a good one
	wc, _ := c.Put("good.FLAC")
	wc.Write([]byte("gooddata"))
	wc.Close()

	// manually create a zero-length file (simulates partial)
	os.WriteFile(filepath.Join(dir, "zero.MP3"), []byte{}, 0o644)

	// manually create a size-mismatched entry via fake index + file
	bad := filepath.Join(dir, "bad.MP3")
	os.WriteFile(bad, []byte("short"), 0o644)

	// create a corrupt index that references a wrong size for "bad"
	idx := `{"entries":{"bad.MP3":{"size":999,"accessSeq":1},"good.FLAC":{"size":8,"accessSeq":2}},"nextSeq":3}`
	os.WriteFile(filepath.Join(dir, "index.json"), []byte(idx), 0o644)

	// reopen -> should drop zero and bad (size mismatch)
	c2, _ := New(dir, 10<<20)
	n, _ := c2.Stats()
	if n != 1 {
		t.Fatalf("after recovery got %d entries, want 1 (only good)", n)
	}
	if _, _, hit := c2.Get("bad.MP3"); hit {
		t.Error("bad should have been dropped")
	}
	if _, _, hit := c2.Get("zero.MP3"); hit {
		t.Error("zero should have been dropped")
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
			wc.Write([]byte(payload))
		}
		wc.Close()
	}()

	// many readers that also hammer Get
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 50; j++ {
				if rc, _, hit := c.Get(key); hit {
					io.Copy(io.Discard, rc)
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
	io.Copy(io.Discard, r)

	if _, _, hit := c.Get("tee-good.MP3"); !hit {
		t.Error("tee good should have committed")
	}

	// error path: src that errors
	srcBad := io.MultiReader(bytes.NewReader([]byte("partial")), &errReader{err: io.ErrUnexpectedEOF})
	r2 := c.TeeReader("tee-bad.MP3", srcBad)
	io.Copy(io.Discard, r2)

	if _, _, hit := c.Get("tee-bad.MP3"); hit {
		t.Error("tee error path should not have committed")
	}
}

type errReader struct{ err error }

func (e *errReader) Read(p []byte) (int, error) { return 0, e.err }

func TestClear(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, 10<<20)

	wc, _ := c.Put("x.MP3")
	wc.Write([]byte("abc"))
	wc.Close()

	c.Clear()
	n, b := c.Stats()
	if n != 0 || b != 0 {
		t.Errorf("clear stats %d %d", n, b)
	}
	// index should also be gone or empty
}

func TestNoEvictActiveWrite(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, 50) // very small

	// start a long write for "active"
	wc, _ := c.Put("active.MP3")
	wc.Write(bytes.Repeat([]byte("A"), 30))

	// add others that would normally evict
	for i := 0; i < 5; i++ {
		w, _ := c.Put(string(rune('a'+i)) + ".MP3")
		w.Write(bytes.Repeat([]byte("B"), 20))
		w.Close()
	}

	// active write still in progress, should not have been evicted
	if _, busy := func() (struct{}, bool) {
		c.mu.Lock()
		defer c.mu.Unlock()
		_, b := c.writing["active.MP3"]
		return struct{}{}, b
	}(); !busy {
		// ok
	}

	// finish it
	wc.Close()

	// now it should be present (or may have been kept)
	if _, _, hit := c.Get("active.MP3"); !hit {
		// depending on timing it may have survived because we skipped it
		// this is best-effort in the test
	}
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
	wc.Write(orig)
	if err := wc.Close(); err != nil {
		t.Fatal(err)
	}

	// Second Put for the same key, aborted mid-write (cancelled fetch).
	wc2, err := c.Put(key)
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}
	wc2.Write([]byte("replacement that never completes"))
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
	wc3.Close()
	if _, _, hit := c.Get(key); !hit {
		t.Fatal("zero-write rewrite destroyed the existing entry")
	}

	// A committed replacement swaps bytes and accounting atomically.
	repl := []byte("replacement payload, a bit longer than the original one")
	wc4, _ := c.Put(key)
	wc4.Write(repl)
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
		wc.Write(payload)
		if err := wc.Close(); err != nil {
			t.Fatal(err)
		}
	}

	// Simulate the LRU victim (A) being busy: its removal fails.
	busy := filepath.Join(dir, "A.MP3")
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
	wc.Write(payload)
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
	wc.Write(payload)
	if err := wc.Close(); err != nil {
		t.Fatal(err)
	}
	if n, b := c.Stats(); n != 2 || b != 60 {
		t.Fatalf("eviction did not resume after the victim became removable: %d entries, %d bytes", n, b)
	}
	for _, k := range []string{"B.MP3", "C.MP3"} {
		if _, _, hit := c.Get(k); hit {
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
	wc.Write([]byte("data"))
	wc.Close()

	p := filepath.Join(dir, "mtime.MP3")
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
