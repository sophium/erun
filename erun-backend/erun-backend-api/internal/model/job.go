package model

import (
	"time"

	"github.com/uptrace/bun"
)

// JobType is a closed vocabulary so the queue can group by it.
type JobType string

const (
	JobTypeFix         JobType = "fix"
	JobTypeReview      JobType = "review"
	JobTypeGate        JobType = "gate"
	JobTypeRelease     JobType = "release"
	JobTypeDeploy      JobType = "deploy"
	JobTypeInvestigate JobType = "investigate"
	JobTypePlan        JobType = "plan"
	JobTypeTriage      JobType = "triage"
	JobTypeMaintenance JobType = "maintenance"
)

// JobStatus is a job's lifecycle; only PLANNED and RUNNING are open.
type JobStatus string

const (
	// JobStatusPlanned holds its scope like RUNNING but is exempt from the
	// abandonment sweep: a parked plan has no actor to stop updating it.
	JobStatusPlanned   JobStatus = "PLANNED"
	JobStatusRunning   JobStatus = "RUNNING"
	JobStatusSucceeded JobStatus = "SUCCEEDED"
	JobStatusFailed    JobStatus = "FAILED"
	// JobStatusAbandoned closes a job whose actor stopped updating it.
	JobStatusAbandoned  JobStatus = "ABANDONED"
	JobStatusSuperseded JobStatus = "SUPERSEDED"
)

type ActorKind string

const (
	ActorKindAgent        ActorKind = "agent"
	ActorKindOrchestrator ActorKind = "orchestrator"
	ActorKindHuman        ActorKind = "human"
)

// Job records work before it starts, so a second actor claiming the same scope
// is told who holds it.
type Job struct {
	bun.BaseModel `bun:"table:jobs,alias:j"`
	JobID         string `json:"jobId" bun:"job_id,pk,scanonly"`
	TenantID      string `json:"tenantId" bun:"tenant_id,scanonly"`
	// EnvironmentID is empty for host-side work.
	EnvironmentID string  `json:"environmentId,omitempty" bun:"environment_id,nullzero"`
	JobType       JobType `json:"jobType" bun:"job_type"`
	// IssueRef is owner/repo#number, or empty.
	IssueRef string `json:"issueRef,omitempty" bun:"issue_ref,nullzero"`
	// Summary is prose, never just a shell command.
	Summary   string    `json:"summary" bun:"summary"`
	Status    JobStatus `json:"status" bun:"status"`
	ActorKind ActorKind `json:"actorKind" bun:"actor_kind"`
	ActorID   string    `json:"actorId" bun:"actor_id"`
	// Scope is what the job claims; empty never collides.
	Scope      string `json:"scope,omitempty" bun:"scope,nullzero"`
	LocalJobID string `json:"localJobId,omitempty" bun:"local_job_id,nullzero"`
	// StartedAt is required: a refused claim names when the holder started.
	StartedAt time.Time `json:"startedAt" bun:"started_at"`
	// EndedAt is set exactly when the job is closed (enforced by a CHECK constraint).
	EndedAt   *time.Time `json:"endedAt,omitempty" bun:"ended_at,nullzero"`
	CreatedAt time.Time  `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt time.Time  `json:"updatedAt" bun:"updated_at,scanonly"`
}

// IsOpen reports whether the job still holds its scope.
func (j Job) IsOpen() bool {
	return j.Status == JobStatusRunning || j.Status == JobStatusPlanned
}
