package eruncommon

import (
	"testing"
	"time"
)

// This covers what the integration suite cannot reach deterministically: the
// exact 23:59 boundary needs an injected clock, since driving the compiled
// binary against the real wall clock only lands on that minute once a day.

// TestWorkingHoursAllDaySpanIsAlwaysInside pins the boundary fix: 00:00-23:59
// is the only literal all-day span HH:MM-HH:MM can express (24:00 is not a
// valid clock value), so it must never exclude the day's last minute the way
// an ordinary end-exclusive window does.
func TestWorkingHoursAllDaySpanIsAlwaysInside(t *testing.T) {
	for _, second := range []int{0, 1, 30, 59} {
		now := time.Date(2026, time.January, 1, 23, 59, second, 0, time.UTC)
		outside, _, err := workingHoursStatus("00:00-23:59", "UTC", now)
		if err != nil {
			t.Fatalf("second=%d: %v", second, err)
		}
		if outside {
			t.Errorf("second=%d: expected 23:59 to be inside an all-day window, got outside", second)
		}
	}
}

// TestWorkingHoursOrdinaryWindowStillExcludesItsEndMinute guards against
// overcorrecting the fix above: an ordinary window's end-exclusive behavior
// (09:00-17:00 excludes 17:00 exactly) is deliberate and must not change.
func TestWorkingHoursOrdinaryWindowStillExcludesItsEndMinute(t *testing.T) {
	now := time.Date(2026, time.January, 1, 17, 0, 0, 0, time.UTC)
	outside, _, err := workingHoursStatus("09:00-17:00", "UTC", now)
	if err != nil {
		t.Fatal(err)
	}
	if !outside {
		t.Errorf("expected 17:00 to remain outside 09:00-17:00")
	}
}
