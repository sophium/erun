package model

import (
	"time"

	"github.com/uptrace/bun"
)

type Build struct {
	bun.BaseModel `bun:"table:builds,alias:b"`
	BuildID       string `json:"buildId" bun:"build_id,pk,scanonly"`
	TenantID      string `json:"tenantId" bun:"tenant_id,scanonly"`
	ReviewID      string `json:"reviewId" bun:"review_id"`
	// ReviewName is read-only display data populated by build read queries.
	ReviewName string    `json:"reviewName,omitempty" bun:"review_name,scanonly"`
	Successful bool      `json:"successful" bun:"successful"`
	CommitID   string    `json:"commitId" bun:"commit_id"`
	Version    string    `json:"version" bun:"version"`
	CreatedAt  time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt  time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
