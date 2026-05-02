package model

import "time"

type Build struct {
	BuildID    string    `json:"buildId"`
	TenantID   string    `json:"tenantId"`
	ReviewID   string    `json:"reviewId"`
	Successful bool      `json:"successful"`
	CommitID   string    `json:"commitId"`
	Version    string    `json:"version"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}
