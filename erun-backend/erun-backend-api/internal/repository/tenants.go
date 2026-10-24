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

// CreateTenantParams is the operations-only tenant-registration input;
// OrgFieldKey/OrgFieldValue are empty for a single-tenant issuer.
type CreateTenantParams struct {
	Name          string
	Type          model.TenantType
	Issuer        string
	OrgFieldKey   string
	OrgFieldValue string
	DisplayName   string
}

// Create atomically registers a tenant and its issuer mapping. Operations-only;
// tenant_issuers.tenant_id is explicit because the caller's tenant differs.
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
			return classifyTenantCreateError(err)
		}

		// ON CONFLICT lets an existing shared issuer map an additional tenant.
		if _, err := tx.NewRaw(
			`INSERT INTO issuers (issuer, org_field_key) VALUES (?, ?) ON CONFLICT (issuer) DO NOTHING`,
			issuer, nullIfEmpty(orgFieldKey),
		).Exec(ctx); err != nil {
			return err
		}

		// The registry's existing mode, not the requested key, decides resolvability.
		var effectiveOrgFieldKey string
		if err := tx.NewRaw(
			`SELECT COALESCE(org_field_key, '') FROM issuers WHERE issuer = ?`, issuer,
		).Scan(ctx, &effectiveOrgFieldKey); err != nil {
			return err
		}
		if err := assertResolvableIssuerMapping(issuer, effectiveOrgFieldKey, orgFieldValue); err != nil {
			return err
		}

		if _, err := tx.NewRaw(
			`INSERT INTO tenant_issuers (tenant_id, issuer, org_field_value, name) VALUES (?, ?, ?, ?)`,
			tenant.TenantID, issuer, nullIfEmpty(orgFieldValue), displayName,
		).Exec(ctx); err != nil {
			// (issuer, org_field_value) is the token-resolution key.
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

// classifyTenantCreateError maps tenants_name_key violations to
// ErrTenantNameConflict, a resolvable race distinct from an issuer conflict.
func classifyTenantCreateError(err error) error {
	if constraint, ok := pgConstraintName(err); ok && constraint == "tenants_name_key" {
		return ErrTenantNameConflict
	}
	return normalizeNoRows(err)
}

// nullIfEmpty stores NULL, the single-tenant marker; an empty string would be
// a distinct value under the uniqueness constraints.
func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// List returns every tenant for an operations caller, otherwise only the
// caller's own; tenants has no RLS.
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
			SELECT t.tenant_id, t.name, t.type, t.created_at, t.updated_at,
			       COUNT(u.user_id) AS user_count,
			       EXISTS (
			         SELECT 1
			           FROM tenant_issuers ti
			           JOIN issuers i ON i.issuer = ti.issuer
			          WHERE ti.tenant_id = t.tenant_id
			            AND `+sqlIssuerMappingIsResolvable+`
			       ) AS resolvable
			  FROM tenants t
			  LEFT JOIN users u ON u.tenant_id = t.tenant_id
			 GROUP BY t.tenant_id, t.name, t.type, t.created_at, t.updated_at
			 ORDER BY t.created_at ASC
		`).Scan(ctx, &tenants)
	})
	return tenants, err
}

// Reachable lists tenants the caller's verified identity maps to, with a
// Reachability verdict for the org it presents. It crosses tenants under
// WithinSystemTx, returns identity only, and keeps unresolvable rows so the
// misconfiguration stays visible.
func (r *TenantRepository) Reachable(ctx context.Context) ([]model.Tenant, error) {
	securityContext, ok := security.FromContext(ctx)
	if !ok {
		return nil, ErrMissingSecurityContext
	}
	issuer := strings.TrimSpace(securityContext.ExternalIssuer)
	externalID := strings.TrimSpace(securityContext.ExternalUserID)
	if issuer == "" || externalID == "" {
		return nil, ErrMissingSecurityContext
	}
	org := strings.TrimSpace(securityContext.ExternalOrgID)
	var tenants []model.Tenant
	err := r.txs.WithinSystemTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		// LEFT JOIN so a broken FK surfaces rather than dropping the membership.
		return tx.NewRaw(`
			SELECT t.tenant_id, t.name, t.type, t.created_at, t.updated_at,
			       CASE
			         WHEN ti.tenant_id IS NULL THEN ?
			         WHEN ti.org_field_value IS NOT DISTINCT FROM NULLIF(?, '') THEN ?
			         WHEN NOT `+sqlIssuerMappingIsResolvable+` THEN ?
			         ELSE ?
			       END AS reachability
			  FROM user_external_ids uei
			  JOIN tenants t ON t.tenant_id = uei.tenant_id
			  LEFT JOIN tenant_issuers ti
			         ON ti.tenant_id = uei.tenant_id AND ti.issuer = uei.issuer
			  LEFT JOIN issuers i ON i.issuer = ti.issuer
			 WHERE uei.issuer = ? AND uei.external_id = ?
			 ORDER BY t.created_at ASC
		`,
			string(model.TenantReachabilityIssuerNotMapped),
			org, string(model.TenantReachabilityResolvable),
			string(model.TenantReachabilityNoOrgMapping),
			string(model.TenantReachabilityOrgMismatch),
			issuer, externalID,
		).Scan(ctx, &tenants)
	})
	return tenants, err
}

// Current returns the caller's tenant; tenants has no RLS, so it filters explicitly.
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

// GetByName returns the tenant holding name, unscoped, for Create's name-race path.
func (r *TenantRepository) GetByName(ctx context.Context, name string) (model.Tenant, error) {
	var tenant model.Tenant
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			SELECT tenant_id, name, type, created_at, updated_at
			  FROM tenants
			 WHERE name = ?
		`, strings.TrimSpace(name)).Scan(ctx, &tenant)
		return normalizeNoRows(err)
	})
	return tenant, err
}

// UpdateName renames a tenant. Authorization is the caller's job
// (TenantService.ReconcileBootstrapName).
func (r *TenantRepository) UpdateName(ctx context.Context, tenantID, name string) (model.Tenant, error) {
	var tenant model.Tenant
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			UPDATE tenants
			   SET name = ?
			 WHERE tenant_id = ?
			RETURNING tenant_id, name, type, created_at, updated_at
		`, name, tenantID).Scan(ctx, &tenant)
		if err != nil {
			if isUniqueViolation(err) {
				return ErrConflict
			}
			return normalizeNoRows(err)
		}
		return nil
	})
	return tenant, err
}
