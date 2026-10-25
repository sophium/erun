package idlestatus

import (
	"time"
)

func workingHoursReason(outside bool, workingHours string) string {
	if outside {
		return "outside working hours " + workingHours
	}
	return "inside working hours " + workingHours
}

func secondsRemaining(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return int64((duration + time.Second - 1) / time.Second)
}
