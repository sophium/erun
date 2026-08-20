package repository

import (
	"context"
	"strings"

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

// CreateTenantParams is the operations-only tenant-registration input: tenant
// identity plus the OIDC issuer mapping that resolves tokens to the new tenant.
// OrgFieldKey/OrgFieldValue are set only for an org-scoped (shared) issuer; a
// single-tenant issuer leaves both empty.
type CreateTenantParams struct {
	Name          string
	Type          model.TenantType
	Issuer        string
	OrgFieldKey   string
	OrgFieldValue string
	DisplayName   string
}

// Create atomically registers a new tenant and its OIDC issuer mapping. The root
// resolution tables it writes are operations-only, so the caller must be an
// OPERATIONS tenant. No first user is bootstrapped here; the per-tenant
// first-user bootstrap enrols the tenant's first admin when its first valid token
// arrives. Unlike tenant-owned tables, tenant_issuers.tenant_id is set explicitly
// rather than defaulted from the security context, because the operations
// caller's tenant is not the new tenant's.
func (r *TenantRepository) Create(ctx context.Context, params CreateTenantParams) (model.Tenant, error) {
	tenant := model.Tenant{Name: strings.TrimSpace(params.Name), Type: params.Type}
	issuer := strings.TrimSpace(params.Issuer)
	orgFieldKey := strings.TrimSpace(params.OrgFieldKey)
	orgFieldValue := strings.TrimSpace(params.OrgFieldValue)
	displayName := strings.TrimSpace(params.DisplayName)
	if displayName == "" {
		displayName = issuer
	}

	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewInsert().
			Model(&tenant).
			Column("name", "type").
			Returning("*").
			Scan(ctx); err != nil {
			return normalizeNoRows(err)
		}

		// ON CONFLICT DO NOTHING lets a shared issuer already in the registry map
		// an additional tenant via the tenant_issuers row below.
		if _, err := tx.NewRaw(
			`INSERT INTO issuers (issuer, org_field_key) VALUES (?, ?) ON CONFLICT (issuer) DO NOTHING`,
			issuer, nullIfEmpty(orgFieldKey),
		).Exec(ctx); err != nil {
			return err
		}

		if _, err := tx.NewRaw(
			`INSERT INTO tenant_issuers (tenant_id, issuer, org_field_value, name) VALUES (?, ?, ?, ?)`,
			tenant.TenantID, issuer, nullIfEmpty(orgFieldValue), displayName,
		).Exec(ctx); err != nil {
			// (issuer, org_field_value) is the token-resolution key: a caller that
			// repeats an already-mapped issuer with the same org discriminator (or
			// no discriminator at all) collides here, not on (tenant_id, issuer) —
			// this insert always mints a fresh tenant_id first.
			if isUniqueViolation(err) {
				return ErrConflict
			}
			return err
		}
		return nil
	})
	if err != nil {
		return model.Tenant{}, err
	}
	return tenant, nil
}

// nullIfEmpty stores NULL — the single-tenant marker — for empty issuer columns;
// an empty string would count as a distinct value under the resolution
// uniqueness constraints.
func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// List returns every tenant for an operations-scoped caller, or a
// single-item list containing only the caller's own tenant otherwise.
// tenants is a root resolution table (not RLS-scoped), so a non-operations
// caller must be filtered in application code rather than relying on the
// database to scope the query.
func (r *TenantRepository) List(ctx context.Context) ([]model.Tenant, error) {
	securityContext, ok := security.FromContext(ctx)
	if !ok {
		return nil, ErrMissingSecurityContext
	}
	if securityContext.TenantType != string(model.TenantTypeOperations) {
		tenant, err := r.Current(ctx)
		if err != nil {
			return nil, err
		}
		return []model.Tenant{tenant}, nil
	}
	var tenants []model.Tenant
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
			SELECT tenant_id, name, type, created_at, updated_at
			  FROM tenants
			 ORDER BY created_at ASC
		`).Scan(ctx, &tenants)
	})
	return tenants, err
}

// Current returns the row for the caller's resolved tenant. Because tenants is a
// root resolution table (not RLS-scoped), the query must scope explicitly by the
// security context's tenant ID.
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
