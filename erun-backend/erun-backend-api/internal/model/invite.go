package model

import (
	"time"

	"github.com/uptrace/bun"
)

// Invite is a revocable, single-use, server-side invitation into a tenant.
type Invite struct {
	bun.BaseModel `bun:"table:invites,alias:i"`
	InviteID      string `json:"inviteId" bun:"invite_id,pk,scanonly"`
	TenantID      string `json:"tenantId" bun:"tenant_id,scanonly"`
	// CreatedByUserID is read-only, defaulted by the database (erun_current_user_id()).
	CreatedByUserID string `json:"createdByUserId" bun:"created_by_user_id,scanonly"`
	// Issuer is captured at creation so the unauthenticated accept flow can link the same IdP.
	Issuer     string     `json:"-" bun:"issuer,scanonly"`
	Token      string     `json:"token" bun:"token,scanonly"`
	Email      string     `json:"email,omitempty" bun:"email,scanonly"`
	ExpiresAt  time.Time  `json:"expiresAt" bun:"expires_at,scanonly"`
	ConsumedAt *time.Time `json:"consumedAt,omitempty" bun:"consumed_at,scanonly"`
	CreatedAt  time.Time  `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt  time.Time  `json:"updatedAt" bun:"updated_at,scanonly"`
}
