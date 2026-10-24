package core

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseRuntimeCPUMax parses cpu.max's "<quota> <period>"; a "max" quota has no ceiling.
func ParseRuntimeCPUMax(raw string) (quotaUsec, periodUsec int64, ok bool) {
	fields := strings.Fields(raw)
	if len(fields) != 2 || fields[0] == "max" {
		return 0, 0, false
	}
	quota, err1 := strconv.ParseInt(fields[0], 10, 64)
	period, err2 := strconv.ParseInt(fields[1], 10, 64)
	if err1 != nil || err2 != nil || quota <= 0 || period <= 0 {
		return 0, 0, false
	}
	return quota, period, true
}

func ParseRuntimeInt64(raw string) (int64, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, false
	}
	value, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func FormatMebibytes(bytes int64) string {
	return fmt.Sprintf("%.0fMi", float64(bytes)/(1<<20))
}
