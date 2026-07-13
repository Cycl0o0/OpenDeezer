package audio

import (
	"io"
	"os"
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
	pos    int64
	done   bool
	err    error
	closed bool
	// total is the full stream length in bytes when known (from the HTTP
	// Content-Length / Content-Range), else 0. It lets SeekEnd resolve without
	// waiting for the whole download and lets preallocate size the backing array
	// up front (avoiding the append-doubling reallocation ladder on big FLACs).
	total int64

	// Bounded-memory disk spill fields
	useDiskSpill bool
	file         *os.File
	writePos     int64 // total bytes appended/written to disk
	inMemOffset  int64 // start offset in stream for b.buf
}

func newStreamBuffer() *streamBuffer {
	b := &streamBuffer{}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *streamBuffer) enableDiskSpill() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.useDiskSpill = true
	f, err := os.CreateTemp("", "opdeezer-podcast-*.tmp")
	if err != nil {
		return err
	}
	b.file = f
	return nil
}

func (b *streamBuffer) append(p []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.useDiskSpill {
		if b.file != nil {
			if _, err := b.file.Write(p); err != nil {
				b.err = err
			}
		}
		b.writePos += int64(len(p))
		b.buf = append(b.buf, p...)

		// Cap the resident memory (in b.buf) to a window size of 16MB.
		const maxInMemory = 16 * 1024 * 1024
		if len(b.buf) > maxInMemory {
			discard := len(b.buf) - maxInMemory
			newBuf := make([]byte, maxInMemory)
			copy(newBuf, b.buf[discard:])
			b.buf = newBuf
			b.inMemOffset += int64(discard)
		}
	} else {
		b.buf = append(b.buf, p...)
	}
	b.cond.Broadcast()
}

// preallocate grows the backing array to hold n bytes up front so a large
// download (a 30-40MB FLAC) doesn't repeatedly reallocate and copy through the
// append growth ladder. It never shrinks or discards buffered data.
func (b *streamBuffer) preallocate(n int) {
	b.mu.Lock()
	if !b.useDiskSpill {
		b.preallocLocked(n)
	}
	b.mu.Unlock()
}

func (b *streamBuffer) preallocLocked(n int) {
	if n > cap(b.buf) {
		nb := make([]byte, len(b.buf), n)
		copy(nb, b.buf)
		b.buf = nb
	}
}

// maxPrealloc caps the speculative up-front allocation driven by the
// server-controlled Content-Length / Content-Range total. Without a cap a
// malicious or broken host advertising a huge length would turn straight into
// make([]byte, 0, n) — a makeslice panic or an OOM. 256MB comfortably covers
// the largest real streams (a long FLAC is 30-40MB); anything genuinely bigger
// still works, it just grows via the append ladder past the cap.
const maxPrealloc = 256 << 20

// setContentLength records the full stream length (once known from HTTP headers)
// and preallocates the backing array to fit it. The recorded total is kept in
// full — Seek clamps the resolved offset to the buffered length, so a lying
// total stays harmless — but the speculative preallocation is capped at
// maxPrealloc so an attacker-controlled header can't force a giant allocation.
func (b *streamBuffer) setContentLength(n int64) {
	if n <= 0 {
		return
	}
	b.mu.Lock()
	b.total = n
	if !b.useDiskSpill {
		pre := n
		if pre > maxPrealloc {
			pre = maxPrealloc
		}
		b.preallocLocked(int(pre))
	}
	b.mu.Unlock()
}

func (b *streamBuffer) Total() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.total
}

// waitWatermark blocks until at least n bytes have been buffered, or the stream
// has finished/closed (whichever comes first). It lets the decoder start on a
// safe margin of encoded audio instead of the entire file — cutting time to
// first audio — while streamBuffer's blocking Read then paces the decoder as the
// rest arrives.
func (b *streamBuffer) waitWatermark(n int) {
	b.mu.Lock()
	if b.useDiskSpill {
		for b.writePos < int64(n) && !b.done && !b.closed {
			b.cond.Wait()
		}
	} else {
		for len(b.buf) < n && !b.done && !b.closed {
			b.cond.Wait()
		}
	}
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
	if b.useDiskSpill && b.file != nil {
		name := b.file.Name()
		_ = b.file.Close()
		_ = os.Remove(name)
		b.file = nil
	}
	b.mu.Unlock()
}

// waitDone blocks until the producer has finished downloading the whole stream
// (or the buffer is closed). The initial decode no longer waits for this — it
// starts on a watermark (see waitWatermark) for fast time-to-first-audio — but
// the MP3 seek path still uses it, because building go-mp3's seekable frame
// index requires the complete stream (so seeking gracefully blocks until the
// download finishes).
func (b *streamBuffer) waitDone() {
	b.mu.Lock()
	for !b.done && !b.closed {
		b.cond.Wait()
	}
	b.mu.Unlock()
}

func (b *streamBuffer) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.useDiskSpill {
		for b.pos >= b.writePos && !b.done && !b.closed {
			b.cond.Wait()
		}
		if b.closed {
			return 0, io.EOF
		}
		if b.pos >= b.writePos {
			if b.err != nil {
				return 0, b.err
			}
			return 0, io.EOF
		}
		if b.pos >= b.inMemOffset && b.pos < b.inMemOffset+int64(len(b.buf)) {
			offset := b.pos - b.inMemOffset
			n := copy(p, b.buf[offset:])
			b.pos += int64(n)
			return n, nil
		}
		if b.file == nil {
			return 0, io.ErrUnexpectedEOF
		}
		_, err := b.file.Seek(b.pos, io.SeekStart)
		if err != nil {
			return 0, err
		}
		n, err := b.file.Read(p)
		b.pos += int64(n)
		return n, err
	} else {
		for int(b.pos) >= len(b.buf) && !b.done && !b.closed {
			b.cond.Wait()
		}
		if b.closed {
			return 0, io.EOF
		}
		if int(b.pos) >= len(b.buf) {
			if b.err != nil {
				return 0, b.err
			}
			return 0, io.EOF
		}
		n := copy(p, b.buf[int(b.pos):])
		b.pos += int64(n)
		return n, nil
	}
}

func (b *streamBuffer) Seek(off int64, whence int) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = off
	case io.SeekCurrent:
		abs = b.pos + off
	case io.SeekEnd:
		if b.total > 0 {
			abs = b.total + off
		} else {
			if b.useDiskSpill {
				for !b.done && !b.closed {
					b.cond.Wait()
				}
				abs = b.writePos + off
			} else {
				for !b.done && !b.closed {
					b.cond.Wait()
				}
				abs = int64(len(b.buf)) + off
			}
		}
	}
	if abs < 0 {
		abs = 0
	}
	if b.useDiskSpill {
		for b.writePos < abs && !b.done && !b.closed {
			b.cond.Wait()
		}
		if abs > b.writePos {
			abs = b.writePos
		}
	} else {
		for int64(len(b.buf)) < abs && !b.done && !b.closed {
			b.cond.Wait()
		}
		if abs > int64(len(b.buf)) {
			abs = int64(len(b.buf))
		}
	}
	b.pos = abs
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
