package audio

import (
	"io"
	"sync"
)

// streamBuffer is an in-memory, growing, seekable byte buffer fed by a
// background producer (HTTP download + Blowfish decrypt). The decode goroutine
// reads it as an io.ReadSeeker; Read/Seek block until enough data has arrived
// (or the producer finishes), so the decoder never sees a short/torn read.
// In-memory only — nothing is written to disk (no offline cache).
type streamBuffer struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	pos    int
	done   bool
	err    error
	closed bool
}

func newStreamBuffer() *streamBuffer {
	b := &streamBuffer{}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *streamBuffer) append(p []byte) {
	b.mu.Lock()
	b.buf = append(b.buf, p...)
	b.cond.Broadcast()
	b.mu.Unlock()
}

func (b *streamBuffer) finish(err error) {
	b.mu.Lock()
	b.done = true
	b.err = err
	b.cond.Broadcast()
	b.mu.Unlock()
}

// close unblocks any waiter and makes further reads return EOF (used on teardown).
func (b *streamBuffer) close() {
	b.mu.Lock()
	b.closed = true
	b.cond.Broadcast()
	b.mu.Unlock()
}

func (b *streamBuffer) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.pos >= len(b.buf) && !b.done && !b.closed {
		b.cond.Wait()
	}
	if b.closed {
		return 0, io.EOF
	}
	if b.pos >= len(b.buf) {
		if b.err != nil {
			return 0, b.err
		}
		return 0, io.EOF
	}
	n := copy(p, b.buf[b.pos:])
	b.pos += n
	return n, nil
}

func (b *streamBuffer) Seek(off int64, whence int) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = off
	case io.SeekCurrent:
		abs = int64(b.pos) + off
	case io.SeekEnd:
		// Relative to the end: wait for the full download so the length is known.
		for !b.done && !b.closed {
			b.cond.Wait()
		}
		abs = int64(len(b.buf)) + off
	}
	if abs < 0 {
		abs = 0
	}
	// Block until the target byte has been downloaded (or the stream ends).
	for int64(len(b.buf)) < abs && !b.done && !b.closed {
		b.cond.Wait()
	}
	if abs > int64(len(b.buf)) {
		abs = int64(len(b.buf))
	}
	b.pos = int(abs)
	return abs, nil
}

// pcmRing is a fixed-capacity circular FIFO of decoded interleaved s16 PCM. The
// decode goroutine writes (blocking while full, which paces decoding); the audio
// callback reads (non-blocking — a short read is an underrun the caller pads
// with silence). It is a true ring buffer: read/write touch only the bytes moved
// (no full-buffer memmove), so the lock the realtime audio callback contends for
// is held only briefly — the earlier slice-shift implementation held the lock
// for an O(buffer) copy on every callback and starved the producer, causing
// choppy playback. flush() drops buffered PCM and bumps a sequence so an
// in-flight write for the pre-seek position is discarded.
type pcmRing struct {
	mu       sync.Mutex
	cond     *sync.Cond
	buf      []byte
	head     int // read index
	size     int // bytes currently buffered
	flushSeq uint64
	closed   bool
}

func newPCMRing(capacity int) *pcmRing {
	r := &pcmRing{buf: make([]byte, capacity)}
	r.cond = sync.NewCond(&r.mu)
	return r
}

// write copies p into the ring, blocking until it all fits (or the ring is
// closed/flushed). Returns false if closed or flushed (seq changed) — caller
// stops/refreshes. Large p is written in capacity-sized waves as room frees.
func (r *pcmRing) write(p []byte, seq uint64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(p) > 0 {
		for r.size == len(r.buf) && !r.closed && seq == r.flushSeq {
			r.cond.Wait()
		}
		if r.closed || seq != r.flushSeq {
			return false
		}
		n := len(r.buf) - r.size // free space
		if n > len(p) {
			n = len(p)
		}
		tail := (r.head + r.size) % len(r.buf)
		c := copy(r.buf[tail:], p[:n]) // up to wrap
		if c < n {
			copy(r.buf, p[c:n]) // wrapped remainder
		}
		r.size += n
		p = p[n:]
		r.cond.Broadcast()
	}
	return true
}

// read copies up to len(p) bytes into p and returns the count (may be < len(p)).
func (r *pcmRing) read(p []byte) int {
	r.mu.Lock()
	n := r.size
	if n > len(p) {
		n = len(p)
	}
	if n > 0 {
		c := copy(p, r.buf[r.head:]) // up to wrap
		if c < n {
			copy(p[c:n], r.buf) // wrapped remainder
		}
		r.head = (r.head + n) % len(r.buf)
		r.size -= n
		r.cond.Broadcast()
	}
	r.mu.Unlock()
	return n
}

// buffered reports how many PCM bytes are queued.
func (r *pcmRing) buffered() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.size
}

// flush empties the ring and returns the new sequence number.
func (r *pcmRing) flush() uint64 {
	r.mu.Lock()
	r.head, r.size = 0, 0
	r.flushSeq++
	s := r.flushSeq
	r.cond.Broadcast()
	r.mu.Unlock()
	return s
}

func (r *pcmRing) seq() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.flushSeq
}

func (r *pcmRing) close() {
	r.mu.Lock()
	r.closed = true
	r.cond.Broadcast()
	r.mu.Unlock()
}
