package eruncommon

const (

	// Thresholds below are the values #1233 measured and fixed as named
	// constants with the reasoning attached, per the issue's own ask.
	//
	// RuntimeUsageMemoryWarnPercent: a container that has already used 85% of
	// its limit is one build step away from the OOM kill
	// erun-common/ai_launch.go can currently only report after the fact.
	RuntimeUsageMemoryWarnPercent = 85.0
)
