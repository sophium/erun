package model

import (
	"time"
)

type AttachEnvironmentJobParams struct {
	Tenant      string
	Environment string
	ID          string
	Name        string
	// PID decides the job's state; erun never sees its exit status, so it
	// resolves to unknown, not a made-up outcome.
	PID int
	// LogPath is where the caller already writes the work's output.
	LogPath  string
	LeaseTTL time.Duration
}
