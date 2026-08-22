package model

import (
	"time"

	"github.com/uptrace/bun"
)

// TenantQuota is the per-tenant guardrail row carrying operator-set caps;
// absent a row, the tenant falls back to the default caps (repository.Default*).
// MaxCPUMillicores/MaxMemoryMB/MaxStorageGB are a per-environment namespace
// ceiling: every runtime environment this tenant provisions gets a
// ResourceQuota/LimitRange capped at these values, identically. MaxTotal*
// (#1113) is the separate aggregate tenant-wide budget: since every
// environment's cap is the same per-environment value, admission projects
// (existing runtime environment count + 1) * the per-environment cap against
// MaxTotal* and refuses a create that would exceed it.
type TenantQuota struct {
	bun.BaseModel         `bun:"table:tenant_quotas,alias:tq"`
	TenantID              string    `json:"tenantId" bun:"tenant_id,pk,scanonly"`
	MaxEnvironments       int       `json:"maxEnvironments" bun:"max_environments"`
	MaxCPUMillicores      int       `json:"maxCpuMillicores" bun:"max_cpu_millicores"`
	MaxMemoryMB           int       `json:"maxMemoryMb" bun:"max_memory_mb"`
	MaxStorageGB          int       `json:"maxStorageGb" bun:"max_storage_gb"`
	MaxTotalCPUMillicores int       `json:"maxTotalCpuMillicores" bun:"max_total_cpu_millicores"`
	MaxTotalMemoryMB      int       `json:"maxTotalMemoryMb" bun:"max_total_memory_mb"`
	MaxTotalStorageGB     int       `json:"maxTotalStorageGb" bun:"max_total_storage_gb"`
	CreatedAt             time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt             time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
