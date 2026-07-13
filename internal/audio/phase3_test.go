package audio

import (
	"bytes"
	"io"
	"os"
	"testing"
)

type latencyMockOutput struct {
	mockOutput
	latency int
}

func (l *latencyMockOutput) latencyFrames() int {
	return l.latency
}

func TestPositionMSLatencyCompensation(t *testing.T) {
	out := &latencyMockOutput{
		latency: 10000, // 10000 frames
	}
	p := &Player{out: out, stopMgr: make(chan struct{})}

	// frameBytes = 4, so 10000 frames = 40000 bytes.
	// sampleRate = 44100, bytesPerSec = 176400.
	// 40000 bytes * 1000 / 176400 = 226 ms.

	// Case 1: played is less than latency
	p.played.Store(20000) // 20000 bytes
	pos := p.PositionMS()
	if pos != 0 {
		t.Errorf("PositionMS() = %d, want 0 (clamped)", pos)
	}

	// Case 2: played is greater than latency
	p.played.Store(80000) // 80000 bytes
	// Expected position: (80000 - 40000) * 1000 / 176400 = 40000000 / 176400 = 226 ms.
	pos = p.PositionMS()
	if pos != 226 {
		t.Errorf("PositionMS() = %d, want 226", pos)
	}
}

func TestPodcastBufferDiskSpill(t *testing.T) {
	sb := newStreamBuffer()
	if err := sb.enableDiskSpill(); err != nil {
		t.Fatalf("failed to enable disk spill: %v", err)
	}

	// Create 20MB of test data
	data := make([]byte, 20*1024*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	// Append in 1MB chunks
	chunkSize := 1024 * 1024
	for i := 0; i < len(data); i += chunkSize {
		end := i + chunkSize
		if end > len(data) {
			end = len(data)
		}
		sb.append(data[i:end])
	}

	// Verify resident memory is capped at maxInMemory (16MB)
	if len(sb.buf) > 16*1024*1024 {
		t.Errorf("in-memory buffer size = %d, exceeds 16MB cap", len(sb.buf))
	}

	// Mark stream as done so reads don't block
	sb.finish(nil)

	// Verify we can read the entire stream back byte-identically
	readData := make([]byte, len(data))
	_, err := io.ReadFull(sb, readData)
	if err != nil {
		t.Fatalf("failed to read back full data: %v", err)
	}
	if !bytes.Equal(readData, data) {
		t.Fatal("read data is not identical to written data")
	}

	// Test seeking outside the current in-memory window (window is [4MB, 20MB))
	// Seek to 2MB (from disk)
	_, err = sb.Seek(2*1024*1024, io.SeekStart)
	if err != nil {
		t.Fatalf("Seek failed: %v", err)
	}
	buf := make([]byte, 100)
	_, err = io.ReadFull(sb, buf)
	if err != nil {
		t.Fatalf("Read after seek failed: %v", err)
	}
	if !bytes.Equal(buf, data[2*1024*1024:2*1024*1024+100]) {
		t.Error("data read from disk seek mismatch")
	}

	// Test seeking inside the current in-memory window (window is [4MB, 20MB))
	// Seek to 10MB (from memory)
	_, err = sb.Seek(10*1024*1024, io.SeekStart)
	if err != nil {
		t.Fatalf("Seek failed: %v", err)
	}
	_, err = io.ReadFull(sb, buf)
	if err != nil {
		t.Fatalf("Read after seek failed: %v", err)
	}
	if !bytes.Equal(buf, data[10*1024*1024:10*1024*1024+100]) {
		t.Error("data read from memory seek mismatch")
	}

	// Verify temp file cleanup on close
	tempFileName := sb.file.Name()
	if _, err := os.Stat(tempFileName); err != nil {
		t.Fatalf("temp file does not exist before close: %v", err)
	}

	sb.close()

	if _, err := os.Stat(tempFileName); err == nil {
		t.Error("temp file was not cleaned up after close")
	} else if !os.IsNotExist(err) {
		t.Errorf("unexpected error checking temp file: %v", err)
	}
}
