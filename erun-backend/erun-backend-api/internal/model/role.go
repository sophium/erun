package model

import (
	"time"

	"github.com/uptrace/bun"
)

type Role struct {
	bun.BaseModel `bun:"table:roles,alias:ro"`
	RoleID        string `json:"roleId" bun:"role_id,pk,scanonly"`
	TenantID      string `json:"tenantId" bun:"tenant_id,scanonly"`
	Name          string `json:"name" bun:"name"`
	// Permissions is derived by role read queries; roles has no such column.
	Permissions []RolePermission `json:"permissions,omitempty" bun:"-"`
	CreatedAt   time.Time        `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt   time.Time        `json:"updatedAt" bun:"updated_at,scanonly"`
}
