package main

import (
	"log"
	"os"
	"strings"
	"time"
)

const demoClockLayout = "2006-01-02 15:04"

// demoClock is a clock for demos. Writing a local time like "2026-10-08 08:40" to path sets it,
// and it runs on from there in real time, so a running app jumps without a restart. An unreadable
// clock falls back to the real one.
func demoClock(path string) func() time.Time {
	return func() time.Time {
		real := time.Now()
		now, err := readDemoClock(path, real)
		if err != nil {
			log.Printf("demo clock: %v", err)
			return real
		}
		return now
	}
}

// readDemoClock is the time in path, plus the real time since path was written.
func readDemoClock(path string, real time.Time) (time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, err
	}
	set, err := time.ParseInLocation(demoClockLayout, strings.TrimSpace(string(b)), time.Local)
	if err != nil {
		return time.Time{}, err
	}
	return set.Add(real.Sub(info.ModTime())), nil
}
