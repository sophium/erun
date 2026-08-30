package model

import (
	"time"

	"github.com/uptrace/bun"
)

// PlatformRateLimit is the platform-scoped rate-limit configuration: a single
// row for the whole platform, never one per tenant, because the one caller it
// governs (POST /v1/invite-requests) has no tenant yet.
type PlatformRateLimit struct {
	bun.BaseModel `bun:"table:platform_rate_limits,alias:prl"`
	// InviteRequestWindowSeconds is the post-verification, per-(issuer,
	// subject) window POST /v1/invite-requests admits one request per.
	InviteRequestWindowSeconds int       `json:"inviteRequestWindowSeconds" bun:"invite_request_window_seconds"`
	CreatedAt                  time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt                  time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
