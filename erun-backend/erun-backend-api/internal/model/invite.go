package model

import (
	"time"

	"github.com/uptrace/bun"
)

// Invite is a revocable, single-use invitation into a tenant (issue #1483):
// a server-side record rather than a self-contained signed token, so it can
// be listed and revoked before it is ever used.
type Invite struct {
	bun.BaseModel `bun:"table:invites,alias:i"`
	InviteID      string `json:"inviteId" bun:"invite_id,pk,scanonly"`
	TenantID      string `json:"tenantId" bun:"tenant_id,scanonly"`
	// CreatedByUserID is a read-only, database-defaulted field (erun_current_user_id()).
	CreatedByUserID string `json:"createdByUserId" bun:"created_by_user_id,scanonly"`
	// Issuer is the inviter's own authenticated issuer, captured at creation
	// so the unauthenticated accept flow can link the new user's external
	// identity to the same IdP without a caller session to read it from.
	Issuer string `json:"-" bun:"issuer,scanonly"`
	Token  string `json:"token" bun:"token,scanonly"`
	Email  string `json:"email,omitempty" bun:"email,scanonly"`
	// ExpiresAt is set at creation from a fixed TTL, not caller-supplied.
	ExpiresAt time.Time `json:"expiresAt" bun:"expires_at,scanonly"`
	// ConsumedAt is nil until the invite is accepted.
	ConsumedAt *time.Time `json:"consumedAt,omitempty" bun:"consumed_at,scanonly"`
	CreatedAt  time.Time  `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt  time.Time  `json:"updatedAt" bun:"updated_at,scanonly"`
}
