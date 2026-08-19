package model

import (
	"time"

	"github.com/uptrace/bun"
)

// TenantQuota is the per-tenant guardrail row carrying operator-set caps;
// absent a row, the tenant falls back to the default caps (repository.Default*).
// The CPU/memory/storage caps are a per-environment namespace ceiling, not an
// aggregate tenant budget: every runtime environment this tenant provisions
// gets a ResourceQuota/LimitRange capped at these values.
type TenantQuota struct {
	bun.BaseModel    `bun:"table:tenant_quotas,alias:tq"`
	TenantID         string    `json:"tenantId" bun:"tenant_id,pk,scanonly"`
	MaxEnvironments  int       `json:"maxEnvironments" bun:"max_environments"`
	MaxCPUMillicores int       `json:"maxCpuMillicores" bun:"max_cpu_millicores"`
	MaxMemoryMB      int       `json:"maxMemoryMb" bun:"max_memory_mb"`
	MaxStorageGB     int       `json:"maxStorageGb" bun:"max_storage_gb"`
	CreatedAt        time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt        time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
