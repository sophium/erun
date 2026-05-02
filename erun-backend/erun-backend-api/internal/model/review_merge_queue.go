package model

import "time"

type ReviewMergeQueueEntry struct {
	ReviewMergeQueueID int64     `json:"reviewMergeQueueId"`
	TenantID           string    `json:"tenantId"`
	TargetBranch       string    `json:"targetBranch"`
	ReviewID           string    `json:"reviewId"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}
