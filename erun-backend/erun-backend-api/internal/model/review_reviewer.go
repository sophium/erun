package model

import (
	"time"

	"github.com/uptrace/bun"
)

type ReviewReviewer struct {
	bun.BaseModel `bun:"table:review_reviewers,alias:rr"`
	TenantID      string    `json:"tenantId" bun:"tenant_id,scanonly"`
	ReviewID      string    `json:"reviewId" bun:"review_id"`
	UserID        string    `json:"userId" bun:"user_id"`
	CreatedAt     time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt     time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
