package model

import (
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/uptrace/bun"
)

type ReviewStatus string

// Spellings come from erun-common so the CLI, the route, and the reviews
// CHECK constraint validate the same set.
const (
	ReviewStatusOpen   ReviewStatus = model.ReviewStatusOpen
	ReviewStatusClosed ReviewStatus = model.ReviewStatusClosed
	ReviewStatusFailed ReviewStatus = model.ReviewStatusFailed
	ReviewStatusReady  ReviewStatus = model.ReviewStatusReady
	ReviewStatusMerge  ReviewStatus = model.ReviewStatusMerge
	ReviewStatusMerged ReviewStatus = model.ReviewStatusMerged
)

type Review struct {
	bun.BaseModel `bun:"table:reviews,alias:r"`
	ReviewID      string `json:"reviewId" bun:"review_id,pk,scanonly"`
	TenantID      string `json:"tenantId" bun:"tenant_id,scanonly"`
	// AuthorUserID is database-defaulted; a client-supplied value is never persisted.
	AuthorUserID string `json:"authorUserId" bun:"author_user_id,scanonly"`
	// Repository is the canonical remote identity, empty until a report names one.
	Repository   string `json:"repository,omitempty" bun:"repository,nullzero"`
	Name         string `json:"name" bun:"name"`
	TargetBranch string `json:"targetBranch" bun:"target_branch"`
	SourceBranch string `json:"sourceBranch" bun:"source_branch"`
	// DeclaredIssueRef is the author-declared issue. It is separate from IssueRef so
	// a branch-derived guess is never written back as declared.
	DeclaredIssueRef  string       `json:"-" bun:"issue_ref,nullzero"`
	Status            ReviewStatus `json:"status" bun:"status"`
	LastFailedBuildID string       `json:"lastFailedBuildId,omitempty" bun:"last_failed_build_id,nullzero"`
	LastReadyBuildID  string       `json:"lastReadyBuildId,omitempty" bun:"last_ready_build_id,nullzero"`
	LastMergedBuildID string       `json:"lastMergedBuildId,omitempty" bun:"last_merged_build_id,nullzero"`
	CreatedAt         time.Time    `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt         time.Time    `json:"updatedAt" bun:"updated_at,scanonly"`
	// IssueRef is resolved on read from DeclaredIssueRef or the branch name;
	// IssueRefSource tells a declared link from a guess.
	IssueRef       string                     `json:"issueRef,omitempty" bun:"-"`
	IssueRefSource model.IssueReferenceSource `json:"issueRefSource,omitempty" bun:"-"`
}
