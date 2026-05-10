package repository

import (
	"context"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/uptrace/bun"
)

type TenantIssuerRepository struct {
	txs *TxManager
}

func NewTenantIssuerRepository(txs *TxManager) *TenantIssuerRepository {
	return &TenantIssuerRepository{txs: txs}
}

func (r *TenantIssuerRepository) List(ctx context.Context) ([]model.TenantIssuer, error) {
	securityContext, ok := security.FromContext(ctx)
	if !ok {
		return nil, ErrMissingSecurityContext
	}
	var issuers []model.TenantIssuer
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
			SELECT tenant_id, issuer, name, created_at, updated_at
			  FROM tenant_issuers
			 WHERE tenant_id = ?
			 ORDER BY name, issuer
		`, securityContext.TenantID).Scan(ctx, &issuers)
	})
	return issuers, err
}

func (r *TenantIssuerRepository) UpdateName(ctx context.Context, issuer string, name string) (model.TenantIssuer, error) {
	securityContext, ok := security.FromContext(ctx)
	if !ok {
		return model.TenantIssuer{}, ErrMissingSecurityContext
	}
	issuer = strings.TrimSpace(issuer)
	name = strings.TrimSpace(name)
	if issuer == "" || name == "" {
		return model.TenantIssuer{}, ErrInvalidInput
	}

	var tenantIssuer model.TenantIssuer
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			UPDATE tenant_issuers
			   SET name = ?
			 WHERE tenant_id = ?
			   AND issuer = ?
			RETURNING tenant_id, issuer, name, created_at, updated_at
		`, name, securityContext.TenantID, issuer).Scan(ctx, &tenantIssuer)
		return normalizeNoRows(err)
	})
	return tenantIssuer, err
}
