package records

// The sampler records activity for uninstrumented work resident in the runtime container.
// Work is a CPU-time rate above a floor, not residency: a parked agent burns scheduler
// and repaint noise every tick and would otherwise keep the environment awake forever.

const (
	// ActivityKindProcess is the idle-marker kind for sampled work.
	ActivityKindProcess = "process"
)
