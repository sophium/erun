package model

import (
	"time"

	"github.com/uptrace/bun"
)

// GateRunStatus is a gate run's own account of what happened, distinct from a
// caller's mere assertion. INCONCLUSIVE exists so a wrapper that never
// reached a real verdict (its own timeout, an environment fault) has
// somewhere to report that is not FAILED.
type GateRunStatus string

const (
	GateRunStatusRunning      GateRunStatus = "RUNNING"
	GateRunStatusPassed       GateRunStatus = "PASSED"
	GateRunStatusFailed       GateRunStatus = "FAILED"
	GateRunStatusInconclusive GateRunStatus = "INCONCLUSIVE"
)

// GateRun is the first-class record of one attempt to gate a prospective
// merge, independent of whether an erun review exists for the change it
// gates. A review-driven merge is one producer (ReviewID set, alongside the
// review's own GATE build); a repository whose changes arrive as GitHub pull
// requests, with no erun review at all, is the other (ReviewID empty).
type GateRun struct {
	bun.BaseModel `bun:"table:gate_runs,alias:g"`
	GateRunID     string   `json:"gateRunId" bun:"gate_run_id,pk,scanonly"`
	TenantID      string   `json:"tenantId" bun:"tenant_id,scanonly"`
	SourceBranch  string   `json:"sourceBranch" bun:"source_branch"`
	TargetBranch  string   `json:"targetBranch" bun:"target_branch"`
	SourceCommit  string   `json:"sourceCommit" bun:"source_commit"`
	// MergeCommit is the prospective squash-merge commit this run actually
	// tested; empty only for a run that failed before that commit existed at
	// all (e.g. a squash conflict).
	MergeCommit string `json:"mergeCommit,omitempty" bun:"merge_commit,nullzero"`
	// ReviewID links this run to the erun review it gates, when one exists.
	ReviewID string `json:"reviewId,omitempty" bun:"review_id,nullzero"`
	// ReviewName is read-only display data populated by list/get queries.
	ReviewName string        `json:"reviewName,omitempty" bun:"review_name,scanonly"`
	Status     GateRunStatus `json:"status" bun:"status"`
	// FailingStep names which gate step produced a FAILED verdict. Required
	// exactly when Status is FAILED — a red verdict is never reported with
	// nothing to point at.
	FailingStep string `json:"failingStep,omitempty" bun:"failing_step,nullzero"`
	// LogRef points at where to read this run's own output (a job id, URL, or
	// path); optional even for a FAILED run.
	LogRef    string    `json:"logRef,omitempty" bun:"log_ref,nullzero"`
	CreatedAt time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
