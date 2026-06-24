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

// CreateTenantParams is the operations-only tenant-registration input. It carries
// the tenant identity plus the OIDC issuer mapping that resolves tokens to the
// new tenant. orgFieldKey/orgFieldValue are set only for an org-scoped (shared)
// issuer; a single-tenant issuer leaves both empty (NULL org_field_key on the
// issuers registry, NULL org_field_value on the tenant_issuers mapping).
type CreateTenantParams struct {
	Name          string
	Type          model.TenantType
	Issuer        string
	OrgFieldKey   string
	OrgFieldValue string
	DisplayName   string
}

// Create registers a new tenant and its OIDC issuer mapping in one transaction:
// the tenants row, the issuers registry row (the globally unique issuer key with
// its org-scoping mode), and the tenant_issuers mapping row binding the issuer
// (and org value, when org-scoped) to the new tenant. These are root resolution
// tables writable only by erun_operations, which WithinTx selects because the
// caller is an OPERATIONS tenant. No first user is bootstrapped here — the
// per-tenant first-user bootstrap enrols the tenant's first admin when its first
// valid token arrives. Unlike tenant-owned tables, tenant_issuers.tenant_id is
// set explicitly to the new tenant's id rather than defaulted from the security
// context, because the operations caller's own tenant_id is not the new tenant's.
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

		// Register the issuer once in the root issuers registry. org_field_key is
		// NULL for a single-tenant issuer and names the org claim for an org-scoped
		// (shared) issuer. ON CONFLICT DO NOTHING lets a shared issuer already in
		// the registry map an additional tenant via a new tenant_issuers row below.
		if _, err := tx.NewRaw(
			`INSERT INTO issuers (issuer, org_field_key) VALUES (?, ?) ON CONFLICT (issuer) DO NOTHING`,
			issuer, nullIfEmpty(orgFieldKey),
		).Exec(ctx); err != nil {
			return err
		}

		// Map the issuer (and org value, when org-scoped) to the new tenant.
		// tenant_id is set explicitly to the freshly minted tenant.
		if _, err := tx.NewRaw(
			`INSERT INTO tenant_issuers (tenant_id, issuer, org_field_value, name) VALUES (?, ?, ?, ?)`,
			tenant.TenantID, issuer, nullIfEmpty(orgFieldValue), displayName,
		).Exec(ctx); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return model.Tenant{}, err
	}
	return tenant, nil
}

// nullIfEmpty maps an empty string to a SQL NULL so optional issuer columns
// (org_field_key, org_field_value) store NULL — the single-tenant marker — rather
// than an empty string, which the resolution uniqueness constraints treat as a
// distinct value.
func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
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
