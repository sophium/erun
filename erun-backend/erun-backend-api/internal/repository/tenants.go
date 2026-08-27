package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/uptrace/bun"
)

type TenantRepository struct {
	txs *TxManager
	// platformTenant is this instance's own declared tenant identity
	// (ERUN_TENANT) — the name ReconcileSelfName renames the caller's own
	// OPERATIONS tenant to, and the value Current compares against to report
	// a legacy-named tenant. Empty means that configuration is genuinely
	// absent, not merely unset by a caller.
	platformTenant string
}

func NewTenantRepository(txs *TxManager, platformTenant string) *TenantRepository {
	return &TenantRepository{txs: txs, platformTenant: strings.TrimSpace(platformTenant)}
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
	if err != nil {
		return model.Tenant{}, err
	}
	r.decorateNameMismatch(&tenant)
	return tenant, nil
}

// decorateNameMismatch sets PlatformDeclaredName exactly when there is a
// disagreement worth reporting: tenant is this platform's own OPERATIONS
// tenant and its Name is not what this instance declares itself to be.
func (r *TenantRepository) decorateNameMismatch(tenant *model.Tenant) {
	if tenant.Type == model.TenantTypeOperations && r.platformTenant != "" && tenant.Name != r.platformTenant {
		tenant.PlatformDeclaredName = r.platformTenant
	}
}

// ReconcileSelfName renames the caller's own tenant to this platform's
// declared identity (ERUN_TENANT) when the two disagree — the migration path
// for a platform bootstrapped before its own tenant name was read from
// ERUN_TENANT, whose OPERATIONS tenant is stuck under the name empty-database
// bootstrap fell back to. It is deliberately not a general tenant-rename
// endpoint: it only ever acts on the caller's own tenant, only when that
// tenant is OPERATIONS (a platform's own tenant, never an arbitrary customer
// tenant a caller happens to administer), and only when it owns no
// environments yet, since the <tenant>-<env> runtime namespace is derived
// from the tenant name and renaming a tenant that already has one would
// orphan it. The environments check and the rename happen in one statement
// so a concurrent environment create cannot race between them.
//
// Idempotent by construction: once Name already matches platformTenant (the
// common case on a second call, since the first call's rename already
// converged it), or when this platform declares no ERUN_TENANT at all, this
// is a no-op that returns the tenant unchanged.
func (r *TenantRepository) ReconcileSelfName(ctx context.Context) (model.Tenant, bool, error) {
	securityContext, ok := security.FromContext(ctx)
	if !ok {
		return model.Tenant{}, false, ErrMissingSecurityContext
	}
	if securityContext.TenantType != string(model.TenantTypeOperations) {
		return model.Tenant{}, false, ErrForbidden
	}
	if r.platformTenant == "" {
		current, err := r.Current(ctx)
		return current, false, err
	}

	var tenant model.Tenant
	var renamed bool
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var txErr error
		tenant, renamed, txErr = reconcileSelfNameTx(ctx, tx, securityContext.TenantID, r.platformTenant)
		return txErr
	})
	if err != nil {
		return model.Tenant{}, false, err
	}
	return tenant, renamed, nil
}

// reconcileSelfNameTx does the actual check-and-rename inside an
// already-open transaction: read the tenant, no-op if its name already
// matches, otherwise rename it atomically with the environments check baked
// into the same UPDATE so a concurrent environment create cannot race
// between checking and renaming.
func reconcileSelfNameTx(ctx context.Context, tx bun.Tx, tenantID, platformTenant string) (model.Tenant, bool, error) {
	var tenant model.Tenant
	if scanErr := tx.NewRaw(`
		SELECT tenant_id, name, type, created_at, updated_at
		  FROM tenants
		 WHERE tenant_id = ?
	`, tenantID).Scan(ctx, &tenant); scanErr != nil {
		return model.Tenant{}, false, normalizeNoRows(scanErr)
	}
	if tenant.Name == platformTenant {
		return tenant, false, nil
	}

	updateErr := tx.NewRaw(`
		UPDATE tenants
		   SET name = ?
		 WHERE tenant_id = ?
		   AND NOT EXISTS (SELECT 1 FROM environments e WHERE e.tenant_id = tenants.tenant_id)
		RETURNING tenant_id, name, type, created_at, updated_at
	`, platformTenant, tenantID).Scan(ctx, &tenant)
	switch {
	case updateErr == nil:
		return tenant, true, nil
	case isUniqueViolation(updateErr):
		return model.Tenant{}, false, ErrConflict
	case errors.Is(normalizeNoRows(updateErr), ErrNotFound):
		return model.Tenant{}, false, ErrTenantHasEnvironments
	default:
		return model.Tenant{}, false, updateErr
	}
}
