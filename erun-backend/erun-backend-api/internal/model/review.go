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
	AuthorUserID      string       `json:"authorUserId" bun:"author_user_id,scanonly"`
	Name              string       `json:"name" bun:"name"`
	TargetBranch      string       `json:"targetBranch" bun:"target_branch"`
	SourceBranch      string       `json:"sourceBranch" bun:"source_branch"`
	Status            ReviewStatus `json:"status" bun:"status"`
	LastFailedBuildID string       `json:"lastFailedBuildId,omitempty" bun:"last_failed_build_id,nullzero"`
	LastReadyBuildID  string       `json:"lastReadyBuildId,omitempty" bun:"last_ready_build_id,nullzero"`
	LastMergedBuildID string       `json:"lastMergedBuildId,omitempty" bun:"last_merged_build_id,nullzero"`
	CreatedAt         time.Time    `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt         time.Time    `json:"updatedAt" bun:"updated_at,scanonly"`
}
