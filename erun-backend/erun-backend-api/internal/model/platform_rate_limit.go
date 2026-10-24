package model

import (
	"time"

	"github.com/uptrace/bun"
)

// PlatformRateLimit is a single platform-wide row: invite requests have no tenant yet.
type PlatformRateLimit struct {
	bun.BaseModel              `bun:"table:platform_rate_limits,alias:prl"`
	InviteRequestWindowSeconds int       `json:"inviteRequestWindowSeconds" bun:"invite_request_window_seconds"`
	CreatedAt                  time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt                  time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
