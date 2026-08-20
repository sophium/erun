package repository

import (
	"context"
	"errors"

	eruncommon "github.com/sophium/erun/erun-common"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

// DefaultMaxEnvironments is the environment-count cap for a tenant that has no
// tenant_quotas row yet.
const DefaultMaxEnvironments = 10

// DefaultMaxCPUMillicores/MemoryMB/StorageGB are the resource caps for a
// tenant that has no tenant_quotas row yet, derived from
// eruncommon.MinimumRuntimeNamespaceQuota rather than restated as independent
// literals: they are exactly the floor a stock runtime environment's own pod
// needs (the erun-devops and erun-dind containers' limits summed, plus its
// PVCs), so a tenant with no quota row set can still provision one — and if
// the chart's pod shape changes, this default moves with it instead of
// quietly falling back below the new floor (#1061).
var (
	DefaultMaxCPUMillicores int
	DefaultMaxMemoryMB      int
	DefaultMaxStorageGB     int
)

func init() {
	cpu, memory, storage := eruncommon.MinimumRuntimeNamespaceQuota()
	DefaultMaxCPUMillicores = int(cpu)
	DefaultMaxMemoryMB = int(memory)
	DefaultMaxStorageGB = int(storage)
}

type TenantQuotaRepository struct {
	txs *TxManager
}

func NewTenantQuotaRepository(txs *TxManager) *TenantQuotaRepository {
	return &TenantQuotaRepository{txs: txs}
}

func defaultTenantQuota() model.TenantQuota {
	return model.TenantQuota{
		MaxEnvironments:  DefaultMaxEnvironments,
		MaxCPUMillicores: DefaultMaxCPUMillicores,
		MaxMemoryMB:      DefaultMaxMemoryMB,
		MaxStorageGB:     DefaultMaxStorageGB,
	}
}

// Get returns the caller's full quota row (env count plus the per-environment
// CPU/memory/storage namespace ceiling), defaulted when the tenant has no row
// yet. The unfiltered read returns only the caller's row because RLS scopes it
// to the caller's tenant.
func (r *TenantQuotaRepository) Get(ctx context.Context) (model.TenantQuota, error) {
	var quota model.TenantQuota
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			SELECT tenant_id, max_environments, max_cpu_millicores, max_memory_mb, max_storage_gb, created_at, updated_at
			  FROM tenant_quotas
		`).Scan(ctx, &quota)
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

// MaxEnvironments returns the caller's environment cap alone, the narrow read
// the create/provision quota check needs.
func (r *TenantQuotaRepository) MaxEnvironments(ctx context.Context) (int, error) {
	quota, err := r.Get(ctx)
	if err != nil {
		return 0, err
	}
	return quota.MaxEnvironments, nil
}

// Set upserts a tenant's full quota row. It is operations-only: RLS lets the
// operations role write any tenant's row, so tenant_id is passed explicitly
// rather than defaulting to the caller's own tenant.
func (r *TenantQuotaRepository) Set(ctx context.Context, tenantID string, quota model.TenantQuota) (model.TenantQuota, error) {
	var result model.TenantQuota
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
			INSERT INTO tenant_quotas (tenant_id, max_environments, max_cpu_millicores, max_memory_mb, max_storage_gb)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (tenant_id) DO UPDATE SET
				max_environments = EXCLUDED.max_environments,
				max_cpu_millicores = EXCLUDED.max_cpu_millicores,
				max_memory_mb = EXCLUDED.max_memory_mb,
				max_storage_gb = EXCLUDED.max_storage_gb
			RETURNING tenant_id, max_environments, max_cpu_millicores, max_memory_mb, max_storage_gb, created_at, updated_at
		`, tenantID, quota.MaxEnvironments, quota.MaxCPUMillicores, quota.MaxMemoryMB, quota.MaxStorageGB).Scan(ctx, &result)
	})
	if err != nil {
		return model.TenantQuota{}, err
	}
	return result, nil
}
