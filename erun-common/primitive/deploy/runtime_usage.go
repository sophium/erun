package deploy

const (

	// RuntimeUsageMemoryWarnPercent: a container that has already used 85% of
	// its limit is one build step away from an OOM kill that can otherwise
	// only be reported after the fact.
	RuntimeUsageMemoryWarnPercent = 85.0
)
