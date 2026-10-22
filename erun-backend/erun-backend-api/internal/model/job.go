package model

import (
	"time"

	"github.com/uptrace/bun"
)

// JobType is the operator-facing kind of work a job is. It is a closed
// vocabulary rather than free text so the queue can group by it without a
// bucket that never converges.
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

// JobStatus is where a job is in its own lifecycle. PLANNED and RUNNING are
// the open states; every other value closes the job and pairs with an
// ended_at.
type JobStatus string

const (
	// JobStatusPlanned is a job recorded before its work has started: a
	// triage or plan item parked against the issue it belongs to, flipped to
	// RUNNING when coding begins. It holds its scope no differently from a
	// RUNNING job, so two actors cannot both plan the same branch.
	//
	// Nothing has begun that could be abandoned, which is why the abandonment
	// sweep exempts it: ABANDONED means "its actor stopped updating it", and a
	// parked plan has no actor to stop. See JobService.SweepAbandoned.
	JobStatusPlanned JobStatus = "PLANNED"
	JobStatusRunning JobStatus = "RUNNING"
	// JobStatusSucceeded and JobStatusFailed are the outcomes the work
	// itself reached.
	JobStatusSucceeded JobStatus = "SUCCEEDED"
	JobStatusFailed    JobStatus = "FAILED"
	// JobStatusAbandoned closes a job whose actor stopped updating it — the
	// platform's answer to an orphaned running record nothing ever clears.
	JobStatusAbandoned JobStatus = "ABANDONED"
	// JobStatusSuperseded closes a job another actor deliberately replaced.
	JobStatusSuperseded JobStatus = "SUPERSEDED"
)

// ActorKind is what sort of thing holds a job.
type ActorKind string

const (
	ActorKindAgent        ActorKind = "agent"
	ActorKindOrchestrator ActorKind = "orchestrator"
	ActorKindHuman        ActorKind = "human"
)

// Job is the platform's record of work in flight: what is being done, by
// whom, and — when the work claims a scope — what it claims, so a second
// actor asking for the same scope is told who already holds it. Builds and
// gate runs record outcomes after the fact; a job is recorded before the
// work starts, which is the half that lets two agents stop duplicating each
// other.
type Job struct {
	bun.BaseModel `bun:"table:jobs,alias:j"`
	JobID         string `json:"jobId" bun:"job_id,pk,scanonly"`
	TenantID      string `json:"tenantId" bun:"tenant_id,scanonly"`
	// EnvironmentID is the pod-side environment the job runs in; empty for
	// host-side orchestrator work that never enters an environment.
	EnvironmentID string  `json:"environmentId,omitempty" bun:"environment_id,nullzero"`
	JobType       JobType `json:"jobType" bun:"job_type"`
	// IssueRef is the issue this work belongs to, in owner/repo#number form;
	// empty when the work is not issue-driven.
	IssueRef string `json:"issueRef,omitempty" bun:"issue_ref,nullzero"`
	// Summary is prose describing what is being done, never a command line —
	// the design deliberately rejects a summary that is only a shell command,
	// the same way a FAILED gate run must name its failing step.
	Summary   string    `json:"summary" bun:"summary"`
	Status    JobStatus `json:"status" bun:"status"`
	ActorKind ActorKind `json:"actorKind" bun:"actor_kind"`
	// ActorID is the orchestrator id or agent identity holding this job.
	ActorID string `json:"actorId" bun:"actor_id"`
	// Scope is what this job claims for the dedup query (a branch, a
	// component, or an issue ref). Empty means it claims nothing and can
	// never collide with another job.
	Scope string `json:"scope,omitempty" bun:"scope,nullzero"`
	// LocalJobID mirrors the in-pod job record's own id, when there is one,
	// so a platform row can be tied back to the pod's job.
	LocalJobID string `json:"localJobId,omitempty" bun:"local_job_id,nullzero"`
	// StartedAt is required rather than optional: a refused claim names when
	// the holder started, and an empty holder start time would make that
	// refusal unactionable.
	StartedAt time.Time `json:"startedAt" bun:"started_at"`
	// EndedAt is set exactly when the job is no longer open -- no longer
	// PLANNED or RUNNING -- which the jobs table's own CHECK constraint
	// enforces.
	EndedAt   *time.Time `json:"endedAt,omitempty" bun:"ended_at,nullzero"`
	CreatedAt time.Time  `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt time.Time  `json:"updatedAt" bun:"updated_at,scanonly"`
}

// IsOpen reports whether this job still holds its scope -- PLANNED and
// RUNNING alike, since a plan that has not started is as much a claim on the
// work as one underway. Only an open job can collide with a later claim.
func (j Job) IsOpen() bool {
	return j.Status == JobStatusRunning || j.Status == JobStatusPlanned
}
