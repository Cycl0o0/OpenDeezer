package audio

import (
	"io"
	"testing"
	"time"
)

// TestPreallocateSizesBackingArray verifies preallocate grows capacity up front
// without changing the visible contents, so a large download avoids the append
// reallocation ladder.
func TestPreallocateSizesBackingArray(t *testing.T) {
	sb := newStreamBuffer()
	sb.append([]byte("hello"))
	sb.preallocate(1 << 20) // 1MB
	if got := cap(sb.buf); got < 1<<20 {
		t.Fatalf("preallocate did not grow capacity: cap=%d", got)
	}
	if got := len(sb.buf); got != 5 {
		t.Fatalf("preallocate changed length: %d, want 5", got)
	}
	sb.finish(nil)
	if string(drainStreamBuffer(sb)) != "hello" {
		t.Fatal("preallocate corrupted buffered data")
	}
}

// TestSeekEndUsesContentLength verifies SeekEnd resolves against the known total
// length without blocking for the whole download.
func TestSeekEndUsesContentLength(t *testing.T) {
	sb := newStreamBuffer()
	sb.setContentLength(1000)
	sb.append(make([]byte, 1000)) // fully buffered, but NOT finished
	// SeekEnd(-100) should resolve to 900 immediately (total known), not block.
	done := make(chan int64, 1)
	go func() {
		off, _ := sb.Seek(-100, io.SeekEnd)
		done <- off
	}()
	select {
	case off := <-done:
		if off != 900 {
			t.Fatalf("SeekEnd(-100) with total 1000 = %d, want 900", off)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SeekEnd blocked despite a known content length")
	}
	if cap(sb.buf) < 1000 {
		t.Fatalf("setContentLength should have preallocated: cap=%d", cap(sb.buf))
	}
}

// TestSetContentLengthCapsPrealloc verifies that a server-controlled huge
// Content-Length/Content-Range total cannot force a giant speculative
// allocation (makeslice panic / OOM): the advertised total is still recorded in
// full, but the preallocation is capped at maxPrealloc and the buffer keeps
// working normally via append growth.
func TestSetContentLengthCapsPrealloc(t *testing.T) {
	sb := newStreamBuffer()
	sb.setContentLength(1 << 60) // must not panic or allocate ~an exabyte
	if got := cap(sb.buf); got > maxPrealloc {
		t.Fatalf("prealloc exceeded the cap: cap=%d > maxPrealloc=%d", got, maxPrealloc)
	}
	sb.mu.Lock()
	total := sb.total
	sb.mu.Unlock()
	if total != 1<<60 {
		t.Fatalf("total should record the advertised length in full, got %d", total)
	}
	// The buffer still behaves normally after a capped prealloc.
	sb.append([]byte("data"))
	sb.finish(nil)
	if string(drainStreamBuffer(sb)) != "data" {
		t.Fatal("buffered data corrupted after a capped prealloc")
	}
}

// TestWaitWatermarkUnblocksOnFinish verifies the watermark wait also returns when
// the stream finishes before the margin is reached (short tracks).
func TestWaitWatermarkUnblocksOnFinish(t *testing.T) {
	sb := newStreamBuffer()
	done := make(chan struct{})
	go func() {
		sb.waitWatermark(1 << 20) // 1MB margin, never reached
		close(done)
	}()
	sb.append(make([]byte, 100))
	sb.finish(nil)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("waitWatermark did not return when the stream finished below the margin")
	}
}
