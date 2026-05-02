package model

import "time"

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
	ReviewID          string       `json:"reviewId"`
	TenantID          string       `json:"tenantId"`
	Name              string       `json:"name"`
	TargetBranch      string       `json:"targetBranch"`
	SourceBranch      string       `json:"sourceBranch"`
	Status            ReviewStatus `json:"status"`
	LastFailedBuildID string       `json:"lastFailedBuildId,omitempty"`
	LastReadyBuildID  string       `json:"lastReadyBuildId,omitempty"`
	LastMergedBuildID string       `json:"lastMergedBuildId,omitempty"`
	CreatedAt         time.Time    `json:"createdAt"`
	UpdatedAt         time.Time    `json:"updatedAt"`
}
