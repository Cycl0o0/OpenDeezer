package history

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "history.jsonl"))
}

func entry(id, title, artist string, at int64, secs int64) Entry {
	return Entry{TrackID: id, Title: title, Artist: artist, StartedAt: at, DurationPlayedSec: secs}
}

func TestRecordAndRecent(t *testing.T) {
	s := testStore(t)
	for i := 1; i <= 5; i++ {
		e := entry(fmt.Sprint(i), fmt.Sprintf("Song %d", i), "Artist", int64(1000+i), 60)
		if err := s.Record(e); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	got, err := s.Recent(3)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(got) != 3 || got[0].TrackID != "5" || got[2].TrackID != "3" {
		t.Fatalf("Recent(3) = %+v, want newest-first 5,4,3", got)
	}
	all, _ := s.Recent(0)
	if len(all) != 5 {
		t.Fatalf("Recent(0) = %d entries, want 5", len(all))
	}
}

func TestRecentReloadsFromDisk(t *testing.T) {
	s := testStore(t)
	if err := s.Record(entry("1", "A", "X", 100, 30)); err != nil {
		t.Fatal(err)
	}
	// A fresh store on the same file must lazily rebuild the index.
	s2 := New(s.Path())
	got, err := s2.Recent(10)
	if err != nil || len(got) != 1 || got[0].Title != "A" {
		t.Fatalf("reload got %+v err %v", got, err)
	}
}

func TestCorruptLineSkipped(t *testing.T) {
	s := testStore(t)
	if err := s.Record(entry("1", "A", "X", 100, 30)); err != nil {
		t.Fatal(err)
	}
	// Simulate a torn write from a crash.
	f, err := os.OpenFile(s.Path(), os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"trackId":"2","tit`); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	s2 := New(s.Path())
	got, err := s2.Recent(0)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(got) != 1 || got[0].TrackID != "1" {
		t.Fatalf("got %+v, want just the intact entry", got)
	}
}

func TestStartedAtDefaultsToNow(t *testing.T) {
	s := testStore(t)
	before := time.Now().Unix()
	if err := s.Record(Entry{TrackID: "1", Title: "A", Artist: "X", DurationPlayedSec: 5}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Recent(1)
	if len(got) != 1 || got[0].StartedAt < before {
		t.Fatalf("StartedAt not defaulted: %+v", got)
	}
}

func TestTopTracksAndArtists(t *testing.T) {
	s := testStore(t)
	now := time.Now().Unix()
	plays := []Entry{
		entry("1", "Alpha", "Ann", now-100, 200),
		entry("1", "Alpha", "Ann", now-90, 200),
		entry("2", "Beta", "Bob", now-80, 500),
		entry("2", "Beta", "Bob", now-70, 500),
		entry("3", "Gamma", "Ann", now-60, 100),
		// Old play outside the "since" window.
		entry("9", "Old", "Old Guy", now-100000, 999),
	}
	for _, e := range plays {
		if err := s.Record(e); err != nil {
			t.Fatal(err)
		}
	}
	since := time.Unix(now-1000, 0)

	tracks, err := s.TopTracks(since, 10)
	if err != nil {
		t.Fatalf("TopTracks: %v", err)
	}
	if len(tracks) != 3 {
		t.Fatalf("TopTracks = %+v, want 3", tracks)
	}
	// Ties on plays (Alpha vs Beta, 2 each) break by TotalSec desc.
	if tracks[0].TrackID != "2" || tracks[0].Plays != 2 || tracks[0].TotalSec != 1000 {
		t.Errorf("top track = %+v, want Beta 2 plays 1000s", tracks[0])
	}
	if tracks[1].TrackID != "1" || tracks[2].TrackID != "3" {
		t.Errorf("order = %v %v", tracks[1].TrackID, tracks[2].TrackID)
	}

	artists, err := s.TopArtists(since, 10)
	if err != nil {
		t.Fatalf("TopArtists: %v", err)
	}
	if len(artists) != 2 {
		t.Fatalf("TopArtists = %+v, want 2", artists)
	}
	if artists[0].Artist != "Ann" || artists[0].Plays != 3 || artists[0].TotalSec != 500 {
		t.Errorf("top artist = %+v, want Ann 3 plays 500s", artists[0])
	}

	total, err := s.TotalListenedSec(since)
	if err != nil || total != 1500 {
		t.Errorf("TotalListenedSec(since) = %d err %v, want 1500", total, err)
	}
	all, err := s.TotalListenedSec(time.Time{})
	if err != nil || all != 2499 {
		t.Errorf("TotalListenedSec(zero) = %d err %v, want 2499", all, err)
	}

	top1, _ := s.TopTracks(since, 1)
	if len(top1) != 1 {
		t.Errorf("TopTracks(n=1) = %+v", top1)
	}
}

func TestRotationCapsEntries(t *testing.T) {
	s := testStore(t)
	s.cap = 10
	for i := 0; i < 25; i++ {
		if err := s.Record(entry(fmt.Sprint(i), "T", "A", int64(i+1), 1)); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.Recent(0)
	if len(got) != 10 || got[0].TrackID != "24" || got[9].TrackID != "15" {
		t.Fatalf("after rotation Recent(0) = %d entries first=%v last=%v, want 10/24/15",
			len(got), got[0].TrackID, got[len(got)-1].TrackID)
	}
	// The file itself must have been trimmed too.
	b, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Count(string(b), "\n")
	if lines != 10 {
		t.Fatalf("file has %d lines, want 10", lines)
	}
	// And a fresh store sees the same trimmed view.
	s2 := New(s.Path())
	got2, _ := s2.Recent(0)
	if len(got2) != 10 || got2[0].TrackID != "24" {
		t.Fatalf("reloaded after rotation = %d entries first=%v", len(got2), got2[0].TrackID)
	}
}

func TestConcurrentWriterAndReaders(t *testing.T) {
	s := testStore(t)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	// Concurrent readers.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = s.Recent(5)
				_, _ = s.TopTracks(time.Time{}, 3)
				_, _ = s.TopArtists(time.Time{}, 3)
				_, _ = s.TotalListenedSec(time.Time{})
			}
		}()
	}
	// One writer.
	for i := 0; i < 200; i++ {
		if err := s.Record(entry(fmt.Sprint(i%7), "T", "A", int64(i+1), 1)); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	got, err := s.Recent(0)
	if err != nil || len(got) != 200 {
		t.Fatalf("got %d entries err %v, want 200", len(got), err)
	}
}
