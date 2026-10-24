package model

// EnvironmentActivityLeaseReleaseOutcome reports what a release did; every outcome is
// success so a repeated release never fails a finished job.
type EnvironmentActivityLeaseReleaseOutcome int

const (
	// EnvironmentActivityLeaseReleased means this call removed a held claim.
	EnvironmentActivityLeaseReleased EnvironmentActivityLeaseReleaseOutcome = iota
	// EnvironmentActivityLeaseNotHeld means nothing was held: never taken, already released, or reclaimed.
	EnvironmentActivityLeaseNotHeld
	// EnvironmentActivityLeaseHeldElsewhere: the id holds a lease of the other
	// shape (shared vs exclusive), which stays held.
	EnvironmentActivityLeaseHeldElsewhere
)
