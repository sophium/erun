package model

import (
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/uptrace/bun"
)

type ReviewStatus string

// The stored spellings come from erun-common's vocabulary rather than being
// restated here, because that is the list `erun review list --status` and the
// reviews route both validate against: a status this model accepts but the
// shared validator does not (or the reverse) would be a filter no caller could
// ever reach. The reviews table's own CHECK constraint is the third place the
// set appears; it is the database's enforcement of the same vocabulary.
const (
	ReviewStatusOpen   ReviewStatus = eruncommon.ReviewStatusOpen
	ReviewStatusClosed ReviewStatus = eruncommon.ReviewStatusClosed
	ReviewStatusFailed ReviewStatus = eruncommon.ReviewStatusFailed
	ReviewStatusReady  ReviewStatus = eruncommon.ReviewStatusReady
	ReviewStatusMerge  ReviewStatus = eruncommon.ReviewStatusMerge
	ReviewStatusMerged ReviewStatus = eruncommon.ReviewStatusMerged
)

type Review struct {
	bun.BaseModel `bun:"table:reviews,alias:r"`
	ReviewID      string `json:"reviewId" bun:"review_id,pk,scanonly"`
	TenantID      string `json:"tenantId" bun:"tenant_id,scanonly"`
	// AuthorUserID defaults to the authenticated caller in the database
	// (erun_current_user_id()); a client-supplied value is never persisted.
	AuthorUserID string `json:"authorUserId" bun:"author_user_id,scanonly"`
	// Repository is the repository the review's branches belong to, as a
	// canonical remote identity (eruncommon.RepositoryIdentity), so a tenant
	// serving more than one repository can tell whose review this is. Empty
	// means none was recorded: the column is nullable because reviews created
	// before the platform recorded one have nothing to backfill from, and it
	// is set the first time a report names a repository for one.
	Repository   string `json:"repository,omitempty" bun:"repository,nullzero"`
	Name         string `json:"name" bun:"name"`
	TargetBranch string `json:"targetBranch" bun:"target_branch"`
	SourceBranch string `json:"sourceBranch" bun:"source_branch"`
	// DeclaredIssueRef is the issue this review's work belongs to, in the
	// canonical owner/repo#number spelling, as the review's author stated it.
	// Empty means none was declared: the review's link, if any, is then the
	// number its source branch names.
	//
	// It is deliberately a separate field from IssueRef below rather than the
	// same one. IssueRef is the answer resolved for a response, and a
	// branch-derived number written back into this column would record a
	// guess as though the author had declared it -- the exact confusion the
	// source marker exists to prevent. Nothing writes this field from a
	// response.
	DeclaredIssueRef  string       `json:"-" bun:"issue_ref,nullzero"`
	Status            ReviewStatus `json:"status" bun:"status"`
	LastFailedBuildID string       `json:"lastFailedBuildId,omitempty" bun:"last_failed_build_id,nullzero"`
	LastReadyBuildID  string       `json:"lastReadyBuildId,omitempty" bun:"last_ready_build_id,nullzero"`
	LastMergedBuildID string       `json:"lastMergedBuildId,omitempty" bun:"last_merged_build_id,nullzero"`
	CreatedAt         time.Time    `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt         time.Time    `json:"updatedAt" bun:"updated_at,scanonly"`
	// IssueRef and IssueRefSource are resolved on read, never written back:
	// IssueRef is DeclaredIssueRef when the author recorded one, and
	// otherwise the number the source branch names under the documented
	// convention. Both are omitted together when neither applies, which is an
	// unlinked review rather than a review linked to a guess.
	//
	// IssueRefSource is not decoration. A reference parsed out of a branch
	// name is a guess that the branch was named honestly, and a client that
	// renders it as a link the author declared claims a provenance the
	// platform does not have; the source is what makes the two tellable apart.
	IssueRef       string                          `json:"issueRef,omitempty" bun:"-"`
	IssueRefSource eruncommon.IssueReferenceSource `json:"issueRefSource,omitempty" bun:"-"`
}
