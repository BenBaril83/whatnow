package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeClock(t *testing.T, text string, written time.Time) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clock")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadDemoClockRunsOnFromTheTimeSet(t *testing.T) {
	written := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	path := writeClock(t, "2026-10-08 08:40\n", written)

	got, err := readDemoClock(path, written.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 8, 8, 45, 0, 0, time.Local); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestReadDemoClockRejectsATimeItCantParse(t *testing.T) {
	path := writeClock(t, "half past eight", time.Now())
	if _, err := readDemoClock(path, time.Now()); err == nil {
		t.Fatal("got no error for an unparseable time")
	}
}

func TestDemoClockFallsBackToTheRealTimeWithoutAFile(t *testing.T) {
	before := time.Now()
	got := demoClock(filepath.Join(t.TempDir(), "missing"))()
	if got.Before(before) || got.After(time.Now()) {
		t.Errorf("got %v, want the real time", got)
	}
}
