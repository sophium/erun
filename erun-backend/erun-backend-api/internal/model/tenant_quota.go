package model

import (
	"time"

	"github.com/uptrace/bun"
)

// TenantQuota mirrors the tenant_quotas table — the per-tenant guardrail row
// (one row per tenant) carrying the operator-set caps. MaxEnvironments caps how
// many environments the tenant may register; when no row exists the API applies
// repository.DefaultMaxEnvironments. tenant_id and the timestamps are owned by
// the database (tenant_id DEFAULT + RLS bind the row to the caller's tenant; the
// timestamp trigger populates created_at/updated_at), so they are scan-only.
type TenantQuota struct {
	bun.BaseModel   `bun:"table:tenant_quotas,alias:tq"`
	TenantID        string    `json:"tenantId" bun:"tenant_id,pk,scanonly"`
	MaxEnvironments int       `json:"maxEnvironments" bun:"max_environments"`
	CreatedAt       time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt       time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
