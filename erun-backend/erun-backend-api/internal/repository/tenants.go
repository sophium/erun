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
			return classifyTenantCreateError(err)
		}

		// ON CONFLICT DO NOTHING lets a shared issuer already in the registry map
		// an additional tenant via the tenant_issuers row below.
		if _, err := tx.NewRaw(
			`INSERT INTO issuers (issuer, org_field_key) VALUES (?, ?) ON CONFLICT (issuer) DO NOTHING`,
			issuer, nullIfEmpty(orgFieldKey),
		).Exec(ctx); err != nil {
			return err
		}

		// The registry's own org-scoping mode decides whether this mapping can
		// resolve, not the key the caller asked for: an issuer already
		// registered keeps its mode, and ON CONFLICT DO NOTHING above leaves
		// the caller's key unused. Reading it back is what stops a tenant being
		// registered against a mode no token it will ever receive can satisfy.
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

// classifyTenantCreateError maps a failure inserting the tenants row itself
// to the sentinel a caller needs. name's own UNIQUE constraint
// (tenants_name_key, the column's inline UNIQUE) means a tenant this call
// did not create already holds the name — an expected, resolvable race
// (erun#1722), not a fault, and named specifically so it is never confused
// with the tenant_issuers conflict Create's caller checks separately, which
// is a real "this issuer is already spoken for" refusal with no equivalent
// resolution. Any other error (including a different unique/check violation)
// passes through normalizeNoRows unclassified rather than being guessed at.
func classifyTenantCreateError(err error) error {
	if constraint, ok := pgConstraintName(err); ok && constraint == "tenants_name_key" {
		return ErrTenantNameConflict
	}
	return normalizeNoRows(err)
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
//
// The operations branch computes UserCount with a single LEFT JOIN/GROUP BY
// rather than one query per tenant, so a platform with many tenants costs
// this endpoint one round trip, not N.
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

// Reachable answers "which tenants does the caller's own identity map to, and
// which of those can it actually sign into" — deliberately the one place
// besides List's operations branch that crosses the tenant boundary every
// other query is scoped by, and unlike that branch it is available to any
// authenticated caller, not just an OPERATIONS tenant. It looks up
// user_external_ids by the caller's own verified (issuer, external_id) from
// the security context — never a caller-supplied value — across every
// tenant_id, and returns tenant identity only (name, type): the caller is
// authenticated to the one tenant this request already resolved to, not to
// any of the others this reports, so nothing scoped inside them may leak
// through here. Runs under WithinSystemTx because the query is keyed on
// identity, not on the request's own resolved tenant_id, the same reason
// invites' unauthenticated accept flow needs it.
//
// Membership and resolution use different keys: a membership row is
// (issuer, external_id), while sign-in resolves (issuer, org claim). Reporting
// membership alone offered switch targets no token could ever produce, so
// every row carries a Reachability verdict computed against the org this
// identity actually presents. Unresolvable memberships are annotated, never
// dropped — filtering them would hide the misconfiguration from the one
// operator who can repair it, trading a wasted sign-in round trip for silence.
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
		// The LEFT JOIN's unmapped branch should be unreachable — a membership
		// row's (tenant_id, issuer) is a foreign key into tenant_issuers — but
		// an inner join would answer a broken schema with a missing row, and a
		// membership that vanishes is exactly the silence this query exists to
		// end.
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

// GetByName returns the tenant currently holding name, or ErrNotFound. Its
// one caller is Create's ErrTenantNameConflict path: resolving the tenant a
// name race lost to, not a general lookup, so unlike Current it applies no
// tenant-scoping — the caller (an operations-only workflow) is allowed to
// resolve any tenant by name.
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

// UpdateName renames a tenant, filtering explicitly by tenant_id since tenants
// is a root resolution table with no RLS. Its only intended caller is
// TenantService.ReconcileBootstrapName, which always passes the caller's own
// resolved tenant ID (never one a caller supplies) and has already verified
// the operations-only, no-environments precondition — this method itself
// enforces neither, the same division `Create` already draws between
// repository persistence and route/service-level authorization.
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
