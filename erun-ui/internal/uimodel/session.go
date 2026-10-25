package uimodel

type StartSessionResult struct {
	SessionID int       `json:"sessionId"`
	Selection Selection `json:"selection"`
	Slot      int       `json:"slot,omitempty"`
	Kind      string    `json:"kind,omitempty"`
	// Orchestrated means a background orchestration ran instead of a PTY session.
	Orchestrated bool `json:"orchestrated,omitempty"`
	// Occupancy is set when an unconfirmed AI start found held leases; no session
	// started.
	Occupancy []EnvironmentLease `json:"occupancy,omitempty"`
}

// EnvironmentLease names a job holding an environment.
type EnvironmentLease struct {
	Name        string `json:"name"`
	SecondsHeld int64  `json:"secondsHeld,omitempty"`
	// JobID is empty for non-job holders, which cannot be cancelled.
	JobID string `json:"jobId,omitempty"`
}
