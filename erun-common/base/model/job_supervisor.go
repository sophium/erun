package model

import (
	"time"
)

// EnvironmentJobSupervisorParams is what the supervisor process is handed.
type EnvironmentJobSupervisorParams struct {
	Tenant      string
	Environment string
	ID          string
	Name        string
	Dir         string
	// Agent is the AI tool whose stream is folded into progress; empty for a command job.
	Agent          string
	Command        []string
	Env            map[string]string
	MaxOutputBytes int64
	LeaseTTL       time.Duration
	Handoff        bool
	// Exclusive means the start already took the claim; the supervisor only renews and drops it.
	Exclusive      bool
	StartedByJobID string
}
