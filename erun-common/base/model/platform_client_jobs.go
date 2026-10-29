package model

import (
	"time"
)

// PlatformJob mirrors model.Job's JSON shape.
type PlatformJob struct {
	JobID    string `json:"jobId"`
	TenantID string `json:"tenantId,omitempty"`
	// EnvironmentID is empty for host-side orchestrator work.
	EnvironmentID string `json:"environmentId,omitempty"`
	JobType       string `json:"jobType"`
	// IssueRef is owner/repo#number.
	IssueRef string `json:"issueRef,omitempty"`
	// Summary must be prose; the API refuses a bare shell command.
	Summary   string `json:"summary"`
	Status    string `json:"status"`
	ActorKind string `json:"actorKind"`
	ActorID   string `json:"actorId"`
	// Scope is what the job claims; empty never collides.
	Scope      string     `json:"scope,omitempty"`
	LocalJobID string     `json:"localJobId,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	EndedAt    *time.Time `json:"endedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}
