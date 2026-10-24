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

// TenantIssuerFilter scopes List; TenantID is required for an operations caller.
type TenantIssuerFilter struct {
	TenantID string
}

func (r *TenantIssuerRepository) List(ctx context.Context, filter TenantIssuerFilter) ([]model.TenantIssuer, error) {
	tenantID := strings.TrimSpace(filter.TenantID)
	var issuers []model.TenantIssuer
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
			SELECT ti.tenant_id, ti.issuer, ti.name, ti.org_field_value,
			       COALESCE(i.org_field_key, '') AS org_field_key,
			       ti.created_at, ti.updated_at
			  FROM tenant_issuers ti
			  JOIN issuers i ON i.issuer = ti.issuer
			 WHERE ti.tenant_id = ?
			 ORDER BY ti.name, ti.issuer
		`, tenantID).Scan(ctx, &issuers)
	})
	return issuers, err
}

// UpdateOrgScope converts an issuer to org-scoped and backfills tenantID's
// mapping in one transaction, since either write alone breaks resolution.
// Operations-only (enforced by the route): issuers rows are shared.
func (r *TenantIssuerRepository) UpdateOrgScope(ctx context.Context, tenantID, issuer, orgFieldKey, orgFieldValue string) (model.TenantIssuer, error) {
	if _, ok := security.FromContext(ctx); !ok {
		return model.TenantIssuer{}, ErrMissingSecurityContext
	}
	tenantID = strings.TrimSpace(tenantID)
	issuer = strings.TrimSpace(issuer)
	orgFieldKey = strings.TrimSpace(orgFieldKey)
	orgFieldValue = strings.TrimSpace(orgFieldValue)
	if tenantID == "" || issuer == "" || orgFieldKey == "" || orgFieldValue == "" {
		return model.TenantIssuer{}, ErrInvalidInput
	}

	var tenantIssuer model.TenantIssuer
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`UPDATE issuers SET org_field_key = ? WHERE issuer = ?`, orgFieldKey, issuer).Exec(ctx); err != nil {
			return err
		}
		err := tx.NewRaw(`
			UPDATE tenant_issuers
			   SET org_field_value = ?
			 WHERE tenant_id = ?
			   AND issuer = ?
			RETURNING tenant_id, issuer, name, org_field_value, ? AS org_field_key, created_at, updated_at
		`, orgFieldValue, tenantID, issuer, orgFieldKey).Scan(ctx, &tenantIssuer)
		return normalizeNoRows(err)
	})
	return tenantIssuer, err
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
