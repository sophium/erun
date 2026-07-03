package repository

import (
	"context"
	"errors"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

// DefaultMaxEnvironments is the environment cap for a tenant that has no
// per-tenant override configured yet.
const DefaultMaxEnvironments = 10

type TenantQuotaRepository struct {
	txs *TxManager
}

func NewTenantQuotaRepository(txs *TxManager) *TenantQuotaRepository {
	return &TenantQuotaRepository{txs: txs}
}

// MaxEnvironments returns the caller's environment cap; the unfiltered read
// returns only the caller's row because RLS scopes it to the caller's tenant.
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

// Set upserts a tenant's environment cap. It is operations-only: RLS lets the
// operations role write any tenant's row, so tenant_id is passed explicitly
// rather than defaulting to the caller's own tenant.
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
