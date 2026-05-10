package model

import (
	"time"

	"github.com/uptrace/bun"
)

type ReviewMergeQueueEntry struct {
	bun.BaseModel      `bun:"table:review_merge_queue,alias:q"`
	ReviewMergeQueueID int64     `json:"reviewMergeQueueId" bun:"review_merge_queue_id,pk,scanonly"`
	TenantID           string    `json:"tenantId" bun:"tenant_id,scanonly"`
	TargetBranch       string    `json:"targetBranch" bun:"target_branch"`
	ReviewID           string    `json:"reviewId" bun:"review_id"`
	CreatedAt          time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt          time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
