package model

import (
	"time"

	"github.com/uptrace/bun"
)

// ReleaseStatus is the lifecycle of one queued release. A row is `queued` when
// the trigger enqueues it, `running` once the serial queue claims it, and then
// terminal: `released` naming the version it minted, or `failed` carrying the
// reason the release itself gave.
type ReleaseStatus string

const (
	ReleaseStatusQueued   ReleaseStatus = "queued"
	ReleaseStatusRunning  ReleaseStatus = "running"
	ReleaseStatusReleased ReleaseStatus = "released"
	ReleaseStatusFailed   ReleaseStatus = "failed"
)

// Terminal reports whether the release has finished, either way.
func (s ReleaseStatus) Terminal() bool {
	return s == ReleaseStatusReleased || s == ReleaseStatusFailed
}

// Release is one entry in a tenant's serial release queue: a merge commit that
// should become a published version. There is at most one row per (tenant,
// commit), so the row is also the answer to "has this commit already been
// released".
type Release struct {
	bun.BaseModel `bun:"table:releases,alias:rel"`
	ReleaseID     string `json:"releaseId" bun:"release_id,pk,scanonly"`
	TenantID      string `json:"tenantId" bun:"tenant_id,scanonly"`
	// ReviewID is the accepted review this release came from. Empty when a merge
	// to the target branch was triggered directly.
	ReviewID     string `json:"reviewId,omitempty" bun:"review_id,nullzero"`
	TargetBranch string `json:"targetBranch" bun:"target_branch"`
	CommitID     string `json:"commitId" bun:"commit_id"`
	// Status, Attempt, Version, BuildID and FailureReason are owned by the queue
	// and the executor, never by the caller that enqueues.
	Status  ReleaseStatus `json:"status" bun:"status,scanonly"`
	Attempt int           `json:"attempt" bun:"attempt,scanonly"`
	// Version is the version the release minted, recorded only once it published.
	// A run that failed after minting locally never published it, so leaving it
	// empty is what keeps the row from naming a version nothing can resolve.
	Version string `json:"version,omitempty" bun:"version,scanonly,nullzero"`
	// BuildID links the build row this release recorded against the review.
	BuildID       string    `json:"buildId,omitempty" bun:"build_id,scanonly,nullzero"`
	FailureReason string    `json:"failureReason,omitempty" bun:"failure_reason,scanonly,nullzero"`
	CreatedAt     time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt     time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
