package model

import (
	"time"

	"github.com/uptrace/bun"
)

type UserRole struct {
	bun.BaseModel `bun:"table:user_roles,alias:ur"`
	TenantID      string    `json:"tenantId" bun:"tenant_id,scanonly"`
	UserID        string    `json:"userId" bun:"user_id"`
	RoleID        string    `json:"roleId" bun:"role_id"`
	CreatedAt     time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt     time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
