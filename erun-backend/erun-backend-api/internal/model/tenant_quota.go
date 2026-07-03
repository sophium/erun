package model

import (
	"time"

	"github.com/uptrace/bun"
)

// TenantQuota is the per-tenant guardrail row carrying operator-set caps;
// absent a row, the tenant falls back to the default environment cap.
type TenantQuota struct {
	bun.BaseModel   `bun:"table:tenant_quotas,alias:tq"`
	TenantID        string    `json:"tenantId" bun:"tenant_id,pk,scanonly"`
	MaxEnvironments int       `json:"maxEnvironments" bun:"max_environments"`
	CreatedAt       time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt       time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
