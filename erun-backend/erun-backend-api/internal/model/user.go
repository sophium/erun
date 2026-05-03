package model

import (
	"time"

	"github.com/uptrace/bun"
)

type User struct {
	bun.BaseModel `bun:"table:users,alias:u"`
	UserID        string `json:"userId" bun:"user_id,pk,scanonly"`
	TenantID      string `json:"tenantId" bun:"tenant_id,scanonly"`
	Username      string `json:"username" bun:"username"`
	// ExternalIssuer is read-only identity display data populated by user read queries.
	ExternalIssuer string `json:"issuer,omitempty" bun:"external_issuer,scanonly"`
	// ExternalUserID is read-only identity display data populated by user read queries.
	ExternalUserID string    `json:"subject,omitempty" bun:"external_user_id,scanonly"`
	CreatedAt      time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt      time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
