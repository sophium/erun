package model

import (
	"time"

	"github.com/uptrace/bun"
)

// ReleaseStatus is a queued release's lifecycle.
type ReleaseStatus string

const (
	ReleaseStatusQueued   ReleaseStatus = "queued"
	ReleaseStatusRunning  ReleaseStatus = "running"
	ReleaseStatusReleased ReleaseStatus = "released"
	ReleaseStatusFailed   ReleaseStatus = "failed"
)

func (s ReleaseStatus) Terminal() bool {
	return s == ReleaseStatusReleased || s == ReleaseStatusFailed
}

// Release is one entry in a tenant's serial release queue, unique per (tenant, commit).
type Release struct {
	bun.BaseModel `bun:"table:releases,alias:rel"`
	ReleaseID     string `json:"releaseId" bun:"release_id,pk,scanonly"`
	TenantID      string `json:"tenantId" bun:"tenant_id,scanonly"`
	// ReviewID is empty for a directly triggered merge.
	ReviewID     string `json:"reviewId,omitempty" bun:"review_id,nullzero"`
	TargetBranch string `json:"targetBranch" bun:"target_branch"`
	CommitID     string `json:"commitId" bun:"commit_id"`
	// The queue and executor own these fields, never the enqueuer.
	Status  ReleaseStatus `json:"status" bun:"status,scanonly"`
	Attempt int           `json:"attempt" bun:"attempt,scanonly"`
	// Version is set only once published, so it never names an unresolvable version.
	Version       string    `json:"version,omitempty" bun:"version,scanonly,nullzero"`
	BuildID       string    `json:"buildId,omitempty" bun:"build_id,scanonly,nullzero"`
	FailureReason string    `json:"failureReason,omitempty" bun:"failure_reason,scanonly,nullzero"`
	CreatedAt     time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt     time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
