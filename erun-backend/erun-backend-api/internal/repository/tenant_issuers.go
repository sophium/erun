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

// TenantIssuerFilter scopes List. TenantID is the explicit target tenant —
// required for an operations-scoped caller, mirroring UserFilter/InviteFilter.
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

// UpdateOrgScope converts an issuer from single-tenant to org-scoped and
// backfills tenantID's own mapping with the org value that keeps it
// resolving. Both halves are one transaction because either alone breaks
// resolution: setting org_field_key while the existing mapping still has a
// NULL org_field_value means the issuer's own first tenant stops resolving,
// and setting a value under a NULL key means nothing reads it.
//
// tenantID defaults to the caller's own tenant (first-identity bootstrap
// registers a platform's own IdP single-tenant, which permanently blocks
// every later tenant on that issuer and cannot otherwise be undone through
// the API), but the route also lets an operations caller target another
// tenant's mapping directly — the repair path for a tenant already stuck in
// the same dead-mapping state assertResolvableIssuerMapping now refuses at
// creation time. Org-scoping mode lives on the shared issuers row, so this is
// an operations-only operation — the route enforces that.
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
