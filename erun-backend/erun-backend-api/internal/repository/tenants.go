package repository

import (
	"context"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/uptrace/bun"
)

type TenantRepository struct {
	txs *TxManager
}

func NewTenantRepository(txs *TxManager) *TenantRepository {
	return &TenantRepository{txs: txs}
}

// Current returns the row for the caller's resolved tenant. tenants is a root
// resolution table (not RLS-scoped), so the query is scoped explicitly by the
// authenticated security context's tenant ID, the same way TenantIssuer.List
// scopes its read.
func (r *TenantRepository) Current(ctx context.Context) (model.Tenant, error) {
	securityContext, ok := security.FromContext(ctx)
	if !ok {
		return model.Tenant{}, ErrMissingSecurityContext
	}
	var tenant model.Tenant
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			SELECT tenant_id, name, type, created_at, updated_at
			  FROM tenants
			 WHERE tenant_id = ?
		`, securityContext.TenantID).Scan(ctx, &tenant)
		return normalizeNoRows(err)
	})
	return tenant, err
}
