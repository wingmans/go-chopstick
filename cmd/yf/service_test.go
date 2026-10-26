package main

import (
	"testing"
	"time"
)

func TestScheduledThroughUsesNewYorkWallClock(t *testing.T) {
	location, err := time.LoadLocation(defaultTimezone)
	if err != nil {
		t.Fatalf("load timezone: %v", err)
	}

	before := time.Date(2026, 10, 26, 20, 59, 0, 0, location)
	after := time.Date(2026, 10, 26, 21, 0, 0, 0, location)

	if got := scheduledThrough(before, location, 21, 0).Format(dateLayout); got != "2026-10-25" {
		t.Fatalf("target before schedule = %s", got)
	}

	if got := scheduledThrough(after, location, 21, 0).Format(dateLayout); got != "2026-10-26" {
		t.Fatalf("target at schedule = %s", got)
	}
}

func TestNextScheduledRunStaysAtNineAcrossDST(t *testing.T) {
	location, err := time.LoadLocation(defaultTimezone)
	if err != nil {
		t.Fatalf("load timezone: %v", err)
	}

	now := time.Date(2026, 3, 7, 22, 0, 0, 0, location)
	next := nextScheduledRun(now, location, 21, 0)

	if next.Day() != 8 || next.Hour() != 21 || next.Location() != location {
		t.Fatalf("next scheduled run = %s", next)
	}

	if elapsed := next.Sub(now); elapsed != 22*time.Hour {
		t.Fatalf("elapsed to DST run = %s, want 22h", elapsed)
	}
}
