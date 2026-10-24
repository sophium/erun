package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

type IdentityRepository struct {
	db      *bun.DB
	dialect Dialect
	orgKeys *issuerOrgKeyCache
	// platformTenant (ERUN_TENANT) names the tenant empty-database bootstrap enrols.
	platformTenant string
}

func NewIdentityRepository(db *sql.DB, dialect Dialect, platformTenant string) *IdentityRepository {
	if dialect == "" {
		dialect = DialectPostgres
	}
	return &IdentityRepository{
		db:             bun.NewDB(db, pgdialect.New()),
		dialect:        dialect,
		orgKeys:        newIssuerOrgKeyCache(),
		platformTenant: strings.TrimSpace(platformTenant),
	}
}

// issuerOrgKeyCacheTTL lets an issuer's org-scoping change converge without a restart.
const issuerOrgKeyCacheTTL = 5 * time.Minute

type issuerOrgKeyCache struct {
	mu      sync.Mutex
	entries map[string]issuerOrgKeyEntry
}

type issuerOrgKeyEntry struct {
	orgFieldKey string
	registered  bool
	expiresAt   time.Time
}

func newIssuerOrgKeyCache() *issuerOrgKeyCache {
	return &issuerOrgKeyCache{entries: make(map[string]issuerOrgKeyEntry)}
}

func (c *issuerOrgKeyCache) get(issuer string) (issuerOrgKeyEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[issuer]
	if !ok {
		return issuerOrgKeyEntry{}, false
	}
	if !time.Now().Before(entry.expiresAt) {
		delete(c.entries, issuer)
		return issuerOrgKeyEntry{}, false
	}
	return entry, true
}

func (c *issuerOrgKeyCache) set(issuer string, entry issuerOrgKeyEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry.expiresAt = time.Now().Add(issuerOrgKeyCacheTTL)
	c.entries[issuer] = entry
}

// orgFieldKeyForIssuer returns the issuer's org claim key: registered=false for
// an unknown issuer, empty for single-tenant.
func (r *IdentityRepository) orgFieldKeyForIssuer(ctx context.Context, issuer string) (orgFieldKey string, registered bool, err error) {
	if r.orgKeys != nil {
		if entry, ok := r.orgKeys.get(issuer); ok {
			return entry.orgFieldKey, entry.registered, nil
		}
	}
	var key sql.NullString
	scanErr := r.db.NewRaw(`SELECT org_field_key FROM issuers WHERE issuer = ?`, issuer).Scan(ctx, &key)
	if scanErr != nil {
		if errors.Is(normalizeNoRows(scanErr), ErrNotFound) {
			// Not cached: bootstrap may register the issuer within the TTL.
			return "", false, nil
		}
		return "", false, scanErr
	}
	value := ""
	if key.Valid {
		value = strings.TrimSpace(key.String)
	}
	if r.orgKeys != nil {
		r.orgKeys.set(issuer, issuerOrgKeyEntry{orgFieldKey: value, registered: true})
	}
	return value, true, nil
}

// ResolveOrg returns the org claim value for an org-scoped issuer, or "". It
// keys the identity cache so differing org claims cannot share a tenant.
func (r *IdentityRepository) ResolveOrg(ctx context.Context, claims security.Claims) (string, error) {
	orgFieldKey, registered, err := r.orgFieldKeyForIssuer(ctx, strings.TrimSpace(claims.Issuer))
	if err != nil {
		return "", err
	}
	if !registered || orgFieldKey == "" {
		return "", nil
	}
	return orgClaimValue(claims.Raw, orgFieldKey), nil
}

func (r *IdentityRepository) ResolveIdentity(ctx context.Context, claims security.Claims) (model.Tenant, model.User, error) {
	tenant, err := r.ResolveTenantByIssuer(ctx, claims)
	if err == nil {
		user, err := r.ResolveUserByExternalID(ctx, tenant.TenantID, claims.Issuer, claims.Subject)
		if err == nil {
			user, err = r.refreshUserUsername(ctx, tenant, user, claims)
			if err != nil {
				return model.Tenant{}, model.User{}, sanitizeResolutionError(err, "username refresh", tenant.TenantID, claims)
			}
			return tenant, user, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return model.Tenant{}, model.User{}, sanitizeResolutionError(err, "user lookup", tenant.TenantID, claims)
		}
		// A user-level outcome, never security.ErrTenantUnresolved.
		user, err = r.bootstrapFirstTenantUser(ctx, tenant, claims)
		return tenant, user, sanitizeResolutionError(err, "bootstrap first tenant user", tenant.TenantID, claims)
	}
	if errors.Is(err, security.ErrTenantUnresolved) {
		return model.Tenant{}, model.User{}, err
	}
	if !errors.Is(err, ErrNotFound) {
		return model.Tenant{}, model.User{}, sanitizeResolutionError(err, "tenant lookup", "", claims)
	}
	tenant, user, err := r.bootstrapFirstIdentity(ctx, claims)
	if err != nil {
		// Bootstrap only succeeds on an empty tenants table, so failure means
		// the issuer maps to no tenant.
		err = sanitizeResolutionError(err, "bootstrap first identity", "", claims)
		return model.Tenant{}, model.User{}, fmt.Errorf("%w: %s", security.ErrTenantUnresolved, err.Error())
	}
	return tenant, user, nil
}

// sanitizeResolutionError logs a raw database error with step for context and
// returns ErrIdentityResolutionFailed; ErrNotFound and
// security.ErrTenantUnresolved pass through.
func sanitizeResolutionError(err error, step string, tenantID string, claims security.Claims) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, security.ErrTenantUnresolved) {
		return err
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	log.Printf("erun api identity resolution failed step=%q tenant=%q issuer=%q subject=%q reason=%q", step, tenantID, claims.Issuer, claims.Subject, "unexpected database error")
	return ErrIdentityResolutionFailed
}

func (r *IdentityRepository) refreshUserUsername(ctx context.Context, tenant model.Tenant, user model.User, claims security.Claims) (model.User, error) {
	username := strings.TrimSpace(claims.Username)
	if username == "" || username == strings.TrimSpace(user.Username) {
		return user, nil
	}

	refreshed := user
	err := r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if r.dialect == DialectPostgres {
			if err := r.setPostgresSecurityContext(ctx, tx, security.Context{
				TenantID:   tenant.TenantID,
				TenantType: string(tenant.Type),
				ErunUserID: user.UserID,
			}); err != nil {
				return err
			}
		}
		err := tx.NewRaw(`
			UPDATE users
			   SET username = ?
			 WHERE tenant_id = ?
			   AND user_id = ?
			RETURNING user_id, tenant_id, username, created_at, updated_at
		`, username, tenant.TenantID, user.UserID).Scan(ctx, &refreshed)
		return normalizeNoRows(err)
	})
	if err != nil {
		if isUniqueViolation(err) {
			// A username collision on refresh must not refuse authentication;
			// keep the username on record.
			log.Printf("erun api identity username refresh skipped tenant=%q user=%q requestedUsername=%q reason=%q", tenant.TenantID, user.UserID, username, "username already in use by another user in this tenant")
			return user, nil
		}
		return model.User{}, err
	}
	log.Printf("erun api identity refreshed username tenant=%q user=%q username=%q", tenant.TenantID, user.UserID, refreshed.Username)
	return refreshed, nil
}

// ResolveTenantByIssuer maps a verified token to its tenant. An org-scoped
// issuer with a missing or unmatched org claim returns
// security.ErrTenantUnresolved; an unregistered issuer returns ErrNotFound.
func (r *IdentityRepository) ResolveTenantByIssuer(ctx context.Context, claims security.Claims) (model.Tenant, error) {
	issuer := strings.TrimSpace(claims.Issuer)
	orgFieldKey, registered, err := r.orgFieldKeyForIssuer(ctx, issuer)
	if err != nil {
		return model.Tenant{}, err
	}
	if !registered {
		return model.Tenant{}, ErrNotFound
	}

	var tenant model.Tenant
	var org string
	if orgFieldKey != "" {
		org = orgClaimValue(claims.Raw, orgFieldKey)
		if org == "" {
			return model.Tenant{}, fmt.Errorf("%w: issuer %q is org-scoped (claim %q) but the token carries no matching claim", security.ErrTenantUnresolved, issuer, orgFieldKey)
		}
		err = r.db.NewRaw(`
			SELECT t.tenant_id, t.name, t.type, t.created_at, t.updated_at
			  FROM tenant_issuers ti
			  JOIN tenants t ON t.tenant_id = ti.tenant_id
			 WHERE ti.issuer = ? AND ti.org_field_value = ?
		`, issuer, org).Scan(ctx, &tenant)
	} else {
		err = r.db.NewRaw(`
			SELECT t.tenant_id, t.name, t.type, t.created_at, t.updated_at
			  FROM tenant_issuers ti
			  JOIN tenants t ON t.tenant_id = ti.tenant_id
			 WHERE ti.issuer = ? AND ti.org_field_value IS NULL
		`, issuer).Scan(ctx, &tenant)
	}
	if err != nil {
		err = normalizeNoRows(err)
		if errors.Is(err, ErrNotFound) {
			if orgFieldKey != "" {
				return model.Tenant{}, fmt.Errorf("%w: issuer %q has no tenant registered for org %q", security.ErrTenantUnresolved, issuer, org)
			}
			return model.Tenant{}, fmt.Errorf("%w: issuer %q is registered but has no tenant mapping", security.ErrTenantUnresolved, issuer)
		}
		return model.Tenant{}, err
	}
	return tenant, nil
}

// orgClaimValue renders the org claim as a string; numeric claims decode as
// float64 and render without a decimal to match org_field_value.
func orgClaimValue(raw map[string]any, key string) string {
	if raw == nil {
		return ""
	}
	switch v := raw[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}

func (r *IdentityRepository) ResolveUserByExternalID(ctx context.Context, tenantID string, issuer string, externalID string) (model.User, error) {
	query := `
		SELECT u.user_id, u.tenant_id, u.username, u.created_at, u.updated_at
		  FROM user_external_ids uei
		  JOIN users u
		    ON u.tenant_id = uei.tenant_id
		   AND u.user_id = uei.user_id
		 WHERE uei.tenant_id = ?
		   AND uei.issuer = ?
		   AND uei.external_id = ?
	`
	return r.resolveUserByExternalIDPostgres(ctx, query, tenantID, issuer, externalID)
}

func (r *IdentityRepository) resolveUserByExternalIDPostgres(ctx context.Context, query string, tenantID string, issuer string, externalID string) (model.User, error) {
	var user model.User
	err := r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if r.dialect == DialectPostgres {
			if err := r.setPostgresSecurityContext(ctx, tx, security.Context{
				TenantID: tenantID,
			}); err != nil {
				return err
			}
		}
		return tx.NewRaw(query, tenantID, issuer, externalID).Scan(ctx, &user)
	})
	if err != nil {
		return model.User{}, normalizeNoRows(err)
	}
	return user, nil
}

func (r *IdentityRepository) setPostgresSecurityContext(ctx context.Context, tx bun.Tx, securityContext security.Context) error {
	role := "erun_tenant"
	if securityContext.TenantType == string(model.TenantTypeOperations) {
		role = "erun_operations"
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL ROLE `+role); err != nil {
		return err
	}
	if _, err := tx.NewRaw(`SELECT set_config('erun.tenant_id', ?, true)`, securityContext.TenantID).Exec(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(securityContext.ErunUserID) == "" {
		return nil
	}
	if _, err := tx.NewRaw(`SELECT set_config('erun.user_id', ?, true)`, securityContext.ErunUserID).Exec(ctx); err != nil {
		return err
	}
	return nil
}
