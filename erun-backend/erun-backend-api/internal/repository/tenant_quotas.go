package repository

import (
	"context"
	"errors"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

// DefaultMaxEnvironments is the environment-count cap applied to a tenant that
// has no tenant_quotas row. The per-tenant override is set out-of-band today;
// until a row exists the guardrail uses this default.
const DefaultMaxEnvironments = 10

type TenantQuotaRepository struct {
	txs *TxManager
}

func NewTenantQuotaRepository(txs *TxManager) *TenantQuotaRepository {
	return &TenantQuotaRepository{txs: txs}
}

// MaxEnvironments returns the caller's environment-count cap. It reads the
// caller's tenant_quotas row (RLS scopes the read to the caller's tenant); when
// no row exists the tenant is unconfigured and the default cap applies.
func (r *TenantQuotaRepository) MaxEnvironments(ctx context.Context) (int, error) {
	var quota model.TenantQuota
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			SELECT tenant_id, max_environments, created_at, updated_at
			  FROM tenant_quotas
		`).Scan(ctx, &quota)
		return normalizeNoRows(err)
	})
	if errors.Is(err, ErrNotFound) {
		return DefaultMaxEnvironments, nil
	}
	if err != nil {
		return 0, err
	}
	return quota.MaxEnvironments, nil
}

// Set upserts a tenant's environment-count cap and returns the stored row. It is
// operations-only at the route layer; the operations role's RLS policy lets it
// write any tenant's row, and tenant_id is set explicitly to the target — not
// the erun_current_tenant_id() column default, which would be the operations
// caller's own tenant.
func (r *TenantQuotaRepository) Set(ctx context.Context, tenantID string, maxEnvironments int) (model.TenantQuota, error) {
	var quota model.TenantQuota
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
			INSERT INTO tenant_quotas (tenant_id, max_environments)
			VALUES (?, ?)
			ON CONFLICT (tenant_id) DO UPDATE SET max_environments = EXCLUDED.max_environments
			RETURNING tenant_id, max_environments, created_at, updated_at
		`, tenantID, maxEnvironments).Scan(ctx, &quota)
	})
	if err != nil {
		return model.TenantQuota{}, err
	}
	return quota, nil
}
