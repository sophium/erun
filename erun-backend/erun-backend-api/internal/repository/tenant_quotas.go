package repository

import (
	"context"
	"errors"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/uptrace/bun"
)

// DefaultMaxEnvironments is the environment cap for a tenant with no tenant_quotas row.
const DefaultMaxEnvironments = 10

// DefaultMaxCPUMillicores/MemoryMB/StorageGB are the stock runtime pod's floor,
// so a tenant with no tenant_quotas row can always provision one.
var (
	DefaultMaxCPUMillicores int
	DefaultMaxMemoryMB      int
	DefaultMaxStorageGB     int
)

func init() {
	cpu, memory, storage := config.MinimumRuntimeNamespaceQuota()
	DefaultMaxCPUMillicores = int(cpu)
	DefaultMaxMemoryMB = int(memory)
	DefaultMaxStorageGB = int(storage)
	// Enough to run DefaultMaxEnvironments at the default per-environment size.
	DefaultMaxTotalCPUMillicores = DefaultMaxEnvironments * DefaultMaxCPUMillicores
	DefaultMaxTotalMemoryMB = DefaultMaxEnvironments * DefaultMaxMemoryMB
	DefaultMaxTotalStorageGB = DefaultMaxEnvironments * DefaultMaxStorageGB
}

// DefaultMaxTotalCPUMillicores/MemoryMB/StorageGB are the aggregate caps for a
// tenant with no tenant_quotas row.
var (
	DefaultMaxTotalCPUMillicores int
	DefaultMaxTotalMemoryMB      int
	DefaultMaxTotalStorageGB     int
)

type TenantQuotaRepository struct {
	txs *TxManager
}

func NewTenantQuotaRepository(txs *TxManager) *TenantQuotaRepository {
	return &TenantQuotaRepository{txs: txs}
}

func defaultTenantQuota() model.TenantQuota {
	return model.TenantQuota{
		MaxEnvironments:       DefaultMaxEnvironments,
		MaxCPUMillicores:      DefaultMaxCPUMillicores,
		MaxMemoryMB:           DefaultMaxMemoryMB,
		MaxStorageGB:          DefaultMaxStorageGB,
		MaxTotalCPUMillicores: DefaultMaxTotalCPUMillicores,
		MaxTotalMemoryMB:      DefaultMaxTotalMemoryMB,
		MaxTotalStorageGB:     DefaultMaxTotalStorageGB,
	}
}

const tenantQuotaColumns = `tenant_id, max_environments, max_cpu_millicores, max_memory_mb, max_storage_gb, ` +
	`max_total_cpu_millicores, max_total_memory_mb, max_total_storage_gb, created_at, updated_at`

// Get returns the caller's quota row, defaulted when absent. Scoped explicitly
// by tenant_id because erun_operations' RLS policy is unconditional.
func (r *TenantQuotaRepository) Get(ctx context.Context) (model.TenantQuota, error) {
	var quota model.TenantQuota
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, secErr := security.RequiredFromContext(ctx)
		if secErr != nil {
			return ErrMissingSecurityContext
		}
		err := tx.NewRaw(`
			SELECT `+tenantQuotaColumns+`
			  FROM tenant_quotas
			 WHERE tenant_id = ?
		`, securityContext.TenantID).Scan(ctx, &quota)
		return normalizeNoRows(err)
	})
	if errors.Is(err, ErrNotFound) {
		return defaultTenantQuota(), nil
	}
	if err != nil {
		return model.TenantQuota{}, err
	}
	return quota, nil
}

// MaxEnvironments returns the caller's environment cap alone.
func (r *TenantQuotaRepository) MaxEnvironments(ctx context.Context) (int, error) {
	quota, err := r.Get(ctx)
	if err != nil {
		return 0, err
	}
	return quota.MaxEnvironments, nil
}

// Set upserts a tenant's quota row. Operations-only, so tenant_id is explicit.
func (r *TenantQuotaRepository) Set(ctx context.Context, tenantID string, quota model.TenantQuota) (model.TenantQuota, error) {
	var result model.TenantQuota
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
			INSERT INTO tenant_quotas (tenant_id, max_environments, max_cpu_millicores, max_memory_mb, max_storage_gb,
				max_total_cpu_millicores, max_total_memory_mb, max_total_storage_gb)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (tenant_id) DO UPDATE SET
				max_environments = EXCLUDED.max_environments,
				max_cpu_millicores = EXCLUDED.max_cpu_millicores,
				max_memory_mb = EXCLUDED.max_memory_mb,
				max_storage_gb = EXCLUDED.max_storage_gb,
				max_total_cpu_millicores = EXCLUDED.max_total_cpu_millicores,
				max_total_memory_mb = EXCLUDED.max_total_memory_mb,
				max_total_storage_gb = EXCLUDED.max_total_storage_gb
			RETURNING `+tenantQuotaColumns+`
		`, tenantID, quota.MaxEnvironments, quota.MaxCPUMillicores, quota.MaxMemoryMB, quota.MaxStorageGB,
			quota.MaxTotalCPUMillicores, quota.MaxTotalMemoryMB, quota.MaxTotalStorageGB).Scan(ctx, &result)
	})
	if err != nil {
		return model.TenantQuota{}, err
	}
	return result, nil
}
