package activity

import (
	"testing"
	"time"
)

// 00:00-23:59 is the only literal all-day span, so it must include 23:59.
func TestWorkingHoursAllDaySpanIsAlwaysInside(t *testing.T) {
	for _, second := range []int{0, 1, 30, 59} {
		now := time.Date(2026, time.January, 1, 23, 59, second, 0, time.UTC)
		outside, _, err := WorkingHoursStatus("00:00-23:59", "UTC", now)
		if err != nil {
			t.Fatalf("second=%d: %v", second, err)
		}
		if outside {
			t.Errorf("second=%d: expected 23:59 to be inside an all-day window, got outside", second)
		}
	}
}

// An ordinary window stays end-exclusive.
func TestWorkingHoursOrdinaryWindowStillExcludesItsEndMinute(t *testing.T) {
	now := time.Date(2026, time.January, 1, 17, 0, 0, 0, time.UTC)
	outside, _, err := WorkingHoursStatus("09:00-17:00", "UTC", now)
	if err != nil {
		t.Fatal(err)
	}
	if !outside {
		t.Errorf("expected 17:00 to remain outside 09:00-17:00")
	}
}
