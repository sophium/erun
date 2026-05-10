package model

import (
	"time"

	"github.com/uptrace/bun"
)

type ReviewStatus string

const (
	ReviewStatusOpen   ReviewStatus = "OPEN"
	ReviewStatusClosed ReviewStatus = "CLOSED"
	ReviewStatusFailed ReviewStatus = "FAILED"
	ReviewStatusReady  ReviewStatus = "READY"
	ReviewStatusMerge  ReviewStatus = "MERGE"
	ReviewStatusMerged ReviewStatus = "MERGED"
)

type Review struct {
	bun.BaseModel     `bun:"table:reviews,alias:r"`
	ReviewID          string       `json:"reviewId" bun:"review_id,pk,scanonly"`
	TenantID          string       `json:"tenantId" bun:"tenant_id,scanonly"`
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
