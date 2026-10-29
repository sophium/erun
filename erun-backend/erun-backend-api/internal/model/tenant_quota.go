package model

import (
	"time"

	"github.com/uptrace/bun"
)

// TenantQuota holds operator-set caps; without a row, repository.Default* apply.
// Max{CPUMillicores,MemoryMB,StorageGB} cap each environment, and admission
// refuses a create when (count+1) * that cap exceeds MaxTotal*.
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
