package model

import (
	"time"

	"github.com/uptrace/bun"
)

type RolePermission struct {
	bun.BaseModel    `bun:"table:role_permissions,alias:rp"`
	RolePermissionID string `json:"rolePermissionId" bun:"role_permission_id,pk,scanonly"`
	TenantID         string `json:"tenantId" bun:"tenant_id,scanonly"`
	RoleID           string `json:"roleId" bun:"role_id"`
	// APIMethod/APIPath are set together for an exact grant; APIMethodPattern/
	// APIPathPattern are set together for a regex grant. Exactly one pair is
	// non-empty, enforced by role_permissions_exact_or_pattern_check.
	APIMethod        string    `json:"apiMethod,omitempty" bun:"api_method,nullzero"`
	APIPath          string    `json:"apiPath,omitempty" bun:"api_path,nullzero"`
	APIMethodPattern string    `json:"apiMethodPattern,omitempty" bun:"api_method_pattern,nullzero"`
	APIPathPattern   string    `json:"apiPathPattern,omitempty" bun:"api_path_pattern,nullzero"`
	CreatedAt        time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt        time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
