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
