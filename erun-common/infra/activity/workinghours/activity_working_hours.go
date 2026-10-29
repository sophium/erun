package workinghours

import (
	"fmt"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

// HH:MM cannot spell 24:00, so 00:00-23:59 is how an operator writes an all-day window.
const lastMinuteOfDay = 23*60 + 59

func applyWorkingHoursTimezone(now time.Time, timezone string) (time.Time, error) {
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		return now, nil
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return now, fmt.Errorf("invalid environment idle timezone %q: %w", timezone, err)
	}
	return now.In(loc), nil
}

func WorkingHoursStatus(value string, timezone string, now time.Time) (bool, int64, error) {
	start, end, err := configpkg.ParseWorkingHours(value)
	if err != nil {
		return false, 0, err
	}
	now, err = applyWorkingHoursTimezone(now, timezone)
	if err != nil {
		return false, 0, err
	}
	outside, remainingSeconds := evaluateWorkingHoursWindow(start, end, now)
	return outside, remainingSeconds, nil
}

func evaluateWorkingHoursWindow(start, end int, now time.Time) (bool, int64) {
	minute := now.Hour()*60 + now.Minute()
	if start == 0 && end == lastMinuteOfDay {
		// The all-day span includes the last minute despite the end-exclusive rule.
		return false, int64((24*60-minute)*60 - now.Second())
	}
	if start < end {
		if minute < start || minute >= end {
			return true, 0
		}
		return false, int64((end-minute)*60 - now.Second())
	}
	if minute >= end && minute < start {
		return true, 0
	}
	remainingMinutes := end - minute
	if remainingMinutes <= 0 {
		remainingMinutes += 24 * 60
	}
	return false, int64(remainingMinutes*60 - now.Second())
}
