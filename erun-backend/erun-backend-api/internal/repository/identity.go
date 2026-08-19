package repository

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

type IdentityRepository struct {
	db      *bun.DB
	dialect Dialect
	orgKeys *issuerOrgKeyCache
}

func NewIdentityRepository(db *sql.DB, dialect Dialect) *IdentityRepository {
	if dialect == "" {
		dialect = DialectPostgres
	}
	return &IdentityRepository{db: bun.NewDB(db, pgdialect.New()), dialect: dialect, orgKeys: newIssuerOrgKeyCache()}
}

// issuerOrgKeyCacheTTL bounds staleness so an issuer reconfigured between
// single-tenant and org-scoped converges without a restart.
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

// orgFieldKeyForIssuer returns the issuer's org-scoping mode: registered=false
// means the issuer is not in the registry; registered=true with an empty
// orgFieldKey is a single-tenant issuer; a non-empty orgFieldKey names the token
// claim whose value selects the tenant.
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
			// Do not cache the unregistered case: an issuer registered by
			// first-identity bootstrap would otherwise stay falsely unknown for
			// the whole TTL. Unregistered lookups are rare and cheap.
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

// ResolveOrg returns the org claim value that, with the issuer, resolves the
// tenant for an org-scoped issuer; it returns "" for single-tenant or
// unregistered issuers. It is the authoritative source for the identity cache
// key's org dimension, so the same (issuer, subject) presenting different org
// claims cannot collide on one cached tenant. Resolution failures are surfaced
// as errors so the caller can fall through to full resolution.
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
				return model.Tenant{}, model.User{}, err
			}
			return tenant, user, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return model.Tenant{}, model.User{}, err
		}
		user, err = r.bootstrapFirstTenantUser(ctx, tenant, claims)
		return tenant, user, err
	}
	if !errors.Is(err, ErrNotFound) {
		return model.Tenant{}, model.User{}, err
	}
	return r.bootstrapFirstIdentity(ctx, claims)
}

func (r *IdentityRepository) refreshUserUsername(ctx context.Context, tenant model.Tenant, user model.User, claims security.Claims) (model.User, error) {
	username := strings.TrimSpace(claims.Username)
	if username == "" || username == strings.TrimSpace(user.Username) {
		return user, nil
	}

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
		`, username, tenant.TenantID, user.UserID).Scan(ctx, &user)
		return normalizeNoRows(err)
	})
	if err != nil {
		return model.User{}, err
	}
	log.Printf("erun api identity refreshed username tenant=%q user=%q username=%q", tenant.TenantID, user.UserID, user.Username)
	return user, nil
}

// ResolveTenantByIssuer maps a verified token to its tenant. The issuer's org-scoping
// mode lives once on issuers.org_field_key: NULL means a single-tenant issuer
// (resolve by issuer alone, the common BYO/external-IdP case); a set key names
// the token claim whose value selects the tenant among that issuer's orgs
// (shared multi-tenant issuer, e.g. a hosted Zitadel). An org-scoped issuer
// whose token carries no matching org claim returns ErrNotFound — unauthorized,
// and never bootstraps once tenants exist (bootstrapFirstIdentity guards on an
// empty tenants table). An unregistered issuer also returns ErrNotFound, which
// routes to first-identity bootstrap when the database is empty.
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
	if orgFieldKey != "" {
		org := orgClaimValue(claims.Raw, orgFieldKey)
		if org == "" {
			return model.Tenant{}, ErrNotFound
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
		return model.Tenant{}, normalizeNoRows(err)
	}
	return tenant, nil
}

// orgClaimValue extracts the org-identifying claim value as a string. JSON
// numbers (some IdPs emit numeric org/resource-owner IDs) decode as float64;
// integers render without a decimal so they match the stored org_field_value.
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

func (r *IdentityRepository) bootstrapFirstIdentity(ctx context.Context, claims security.Claims) (model.Tenant, model.User, error) {
	var tenant model.Tenant
	var user model.User

	err := r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var err error
		tenant, user, err = r.insertFirstIdentity(ctx, tx, claims)
		return err
	})
	if err != nil {
		return model.Tenant{}, model.User{}, err
	}
	log.Printf("erun api identity enrolled first tenant/user tenant=%q tenantName=%q tenantType=%q user=%q issuer=%q subject=%q username=%q", tenant.TenantID, tenant.Name, tenant.Type, user.UserID, claims.Issuer, claims.Subject, user.Username)
	return tenant, user, nil
}

// insertFirstIdentity creates the initial OPERATIONS tenant, its issuer, and the
// tenant's first user as one atomic unit. A non-empty tenants table means another
// caller won the bootstrap, which is ErrNotFound so resolution falls back to the
// normal path.
func (r *IdentityRepository) insertFirstIdentity(ctx context.Context, tx bun.Tx, claims security.Claims) (model.Tenant, model.User, error) {
	var tenantCount int
	if err := tx.NewRaw(`SELECT COUNT(*) FROM tenants`).Scan(ctx, &tenantCount); err != nil {
		return model.Tenant{}, model.User{}, err
	}
	if tenantCount != 0 {
		return model.Tenant{}, model.User{}, ErrNotFound
	}

	tenant, err := r.insertTenant(ctx, tx, "operations", model.TenantTypeOperations)
	if err != nil {
		return model.Tenant{}, model.User{}, err
	}
	if err := r.bindSecurityContext(ctx, tx, security.Context{
		TenantID:   tenant.TenantID,
		TenantType: string(model.TenantTypeOperations),
	}); err != nil {
		return model.Tenant{}, model.User{}, err
	}

	if err := insertBootstrapIssuer(ctx, tx, claims.Issuer); err != nil {
		return model.Tenant{}, model.User{}, err
	}
	user, err := r.insertUser(ctx, tx, bootstrapUsername(claims))
	if err != nil {
		return model.Tenant{}, model.User{}, err
	}
	if err := r.bindSecurityContext(ctx, tx, security.Context{
		TenantID:   tenant.TenantID,
		TenantType: string(model.TenantTypeOperations),
		ErunUserID: user.UserID,
	}); err != nil {
		return model.Tenant{}, model.User{}, err
	}
	if err := r.insertDefaultUserAccess(ctx, tx, user.UserID, claims.Issuer, claims.Subject); err != nil {
		return model.Tenant{}, model.User{}, err
	}
	return tenant, user, nil
}

func (r *IdentityRepository) bootstrapFirstTenantUser(ctx context.Context, tenant model.Tenant, claims security.Claims) (model.User, error) {
	var user model.User

	err := r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var err error
		user, err = r.insertFirstTenantUser(ctx, tx, tenant, claims)
		return err
	})
	if err != nil {
		return model.User{}, err
	}
	log.Printf("erun api identity enrolled first user tenant=%q tenantName=%q tenantType=%q user=%q issuer=%q subject=%q username=%q", tenant.TenantID, tenant.Name, tenant.Type, user.UserID, claims.Issuer, claims.Subject, user.Username)
	return user, nil
}

// insertFirstTenantUser enrols the subject as the tenant's first user with the
// predefined roles. A tenant that already has a user gets no implicit enrolment,
// which is ErrNotFound so the caller rejects the unknown subject.
func (r *IdentityRepository) insertFirstTenantUser(ctx context.Context, tx bun.Tx, tenant model.Tenant, claims security.Claims) (model.User, error) {
	var userCount int
	if err := tx.NewRaw(`SELECT COUNT(*) FROM users WHERE tenant_id = ?`, tenant.TenantID).Scan(ctx, &userCount); err != nil {
		return model.User{}, err
	}
	if userCount != 0 {
		return model.User{}, ErrNotFound
	}

	if err := r.bindSecurityContext(ctx, tx, security.Context{
		TenantID:   tenant.TenantID,
		TenantType: string(tenant.Type),
	}); err != nil {
		return model.User{}, err
	}

	user, err := r.insertUser(ctx, tx, bootstrapUsername(claims))
	if err != nil {
		return model.User{}, err
	}
	if err := r.bindSecurityContext(ctx, tx, security.Context{
		TenantID:   tenant.TenantID,
		TenantType: string(tenant.Type),
		ErunUserID: user.UserID,
	}); err != nil {
		return model.User{}, err
	}
	if err := r.insertDefaultUserAccess(ctx, tx, user.UserID, claims.Issuer, claims.Subject); err != nil {
		return model.User{}, err
	}
	return user, nil
}

// bindSecurityContext binds the transaction's RLS context. Only PostgreSQL has
// transaction-local security settings to bind.
func (r *IdentityRepository) bindSecurityContext(ctx context.Context, tx bun.Tx, securityContext security.Context) error {
	if r.dialect != DialectPostgres {
		return nil
	}
	return r.setPostgresSecurityContext(ctx, tx, securityContext)
}

// insertBootstrapIssuer registers the issuer before tenant_issuers references it
// (tenant_issuers.issuer foreign-keys issuers.issuer). The bootstrap issuer is
// single-tenant — org_field_key stays NULL — so the token's iss alone resolves
// the tenant.
func insertBootstrapIssuer(ctx context.Context, tx bun.Tx, issuer string) error {
	if _, err := tx.NewRaw(`INSERT INTO issuers (issuer) VALUES (?) ON CONFLICT (issuer) DO NOTHING`, issuer).Exec(ctx); err != nil {
		return err
	}
	_, err := tx.NewRaw(`INSERT INTO tenant_issuers (issuer, name) VALUES (?, ?)`, issuer, defaultTenantIssuerName(issuer)).Exec(ctx)
	return err
}

// bootstrapUsername prefers the token's username claim and falls back to its
// subject, so an enrolled user always has a display name.
func bootstrapUsername(claims security.Claims) string {
	username := strings.TrimSpace(claims.Username)
	if username == "" {
		return claims.Subject
	}
	return username
}

func (r *IdentityRepository) insertTenant(ctx context.Context, tx bun.Tx, name string, tenantType model.TenantType) (model.Tenant, error) {
	tenant := model.Tenant{Name: name, Type: tenantType}
	err := tx.NewInsert().
		Model(&tenant).
		Column("name", "type").
		Returning("*").
		Scan(ctx)
	if err != nil {
		return model.Tenant{}, normalizeNoRows(err)
	}
	return tenant, nil
}

func defaultTenantIssuerName(issuer string) string {
	issuer = strings.TrimSpace(issuer)
	if issuer == "" {
		return "OIDC issuer"
	}
	return issuer
}

func (r *IdentityRepository) insertUser(ctx context.Context, tx bun.Tx, username string) (model.User, error) {
	user := model.User{Username: username}
	err := tx.NewInsert().
		Model(&user).
		Column("username").
		Returning("*").
		Scan(ctx)
	if err != nil {
		return model.User{}, normalizeNoRows(err)
	}
	return user, nil
}

// insertDefaultUserAccess links the bootstrapped user's external identity and
// grants it the predefined roles. It only ever runs against a tenant with no
// prior users (first-identity/first-tenant-user bootstrap), so grantPredefinedRoles
// always takes its create-fresh-roles path here.
func (r *IdentityRepository) insertDefaultUserAccess(ctx context.Context, tx bun.Tx, userID string, issuer string, subject string) error {
	if _, err := tx.NewRaw(`INSERT INTO user_external_ids (user_id, issuer, external_id) VALUES (?, ?, ?)`, userID, issuer, subject).Exec(ctx); err != nil {
		return err
	}
	return grantPredefinedRoles(ctx, tx, "", userID)
}

// grantPredefinedRoles grants a user the two predefined roles every enrolled
// user gets today (there is no finer-grained role assignment surface yet).
// tenantID, when non-empty, targets a tenant other than the caller's own
// session tenant (an operations-scoped enrollment): every row this writes then
// needs the target tenant set explicitly, because relying on the tenant-owned
// default would write the caller's own tenant instead. findOrCreateRole makes
// this safe to call for a tenant's second and later users too, since
// ReadAll/WriteAll are created once per tenant and reused after that.
func grantPredefinedRoles(ctx context.Context, tx bun.Tx, tenantID string, userID string) error {
	specs := []struct {
		name          string
		methodPattern string
		pathPattern   string
	}{
		{name: "ReadAll", methodPattern: "^(GET|HEAD|OPTIONS)$", pathPattern: "^/.*$"},
		{name: "WriteAll", methodPattern: "^(POST|PUT|PATCH|DELETE)$", pathPattern: "^/.*$"},
	}
	for _, spec := range specs {
		roleID, err := findOrCreateRole(ctx, tx, tenantID, spec.name)
		if err != nil {
			return err
		}
		if err := grantRolePermissionPattern(ctx, tx, tenantID, roleID, spec.methodPattern, spec.pathPattern); err != nil {
			return err
		}
		if err := grantUserRole(ctx, tx, tenantID, userID, roleID); err != nil {
			return err
		}
	}
	return nil
}

// findOrCreateRole returns the named role's id, creating it if this is the
// tenant's first caller to need it.
func findOrCreateRole(ctx context.Context, tx bun.Tx, tenantID string, name string) (string, error) {
	var roleID string
	var err error
	if tenantID != "" {
		err = tx.NewRaw(`SELECT role_id FROM roles WHERE tenant_id = ? AND name = ?`, tenantID, name).Scan(ctx, &roleID)
	} else {
		err = tx.NewRaw(`SELECT role_id FROM roles WHERE name = ?`, name).Scan(ctx, &roleID)
	}
	if err == nil {
		return roleID, nil
	}
	if !errors.Is(normalizeNoRows(err), ErrNotFound) {
		return "", err
	}
	if tenantID != "" {
		err = tx.NewRaw(`INSERT INTO roles (tenant_id, name) VALUES (?, ?) RETURNING role_id`, tenantID, name).Scan(ctx, &roleID)
	} else {
		err = tx.NewRaw(`INSERT INTO roles (name) VALUES (?) RETURNING role_id`, name).Scan(ctx, &roleID)
	}
	return roleID, err
}

// grantRolePermissionPattern is idempotent (ON CONFLICT DO NOTHING) because
// findOrCreateRole may hand back a role that already carries this permission.
func grantRolePermissionPattern(ctx context.Context, tx bun.Tx, tenantID string, roleID string, methodPattern string, pathPattern string) error {
	var err error
	if tenantID != "" {
		_, err = tx.NewRaw(
			`INSERT INTO role_permissions (tenant_id, role_id, api_method_pattern, api_path_pattern) VALUES (?, ?, ?, ?)
			 ON CONFLICT (tenant_id, role_id, api_method_pattern, api_path_pattern) DO NOTHING`,
			tenantID, roleID, methodPattern, pathPattern,
		).Exec(ctx)
	} else {
		_, err = tx.NewRaw(
			`INSERT INTO role_permissions (role_id, api_method_pattern, api_path_pattern) VALUES (?, ?, ?)
			 ON CONFLICT (tenant_id, role_id, api_method_pattern, api_path_pattern) DO NOTHING`,
			roleID, methodPattern, pathPattern,
		).Exec(ctx)
	}
	return err
}

func grantUserRole(ctx context.Context, tx bun.Tx, tenantID string, userID string, roleID string) error {
	var err error
	if tenantID != "" {
		_, err = tx.NewRaw(
			`INSERT INTO user_roles (tenant_id, user_id, role_id) VALUES (?, ?, ?) ON CONFLICT (tenant_id, user_id, role_id) DO NOTHING`,
			tenantID, userID, roleID,
		).Exec(ctx)
	} else {
		_, err = tx.NewRaw(
			`INSERT INTO user_roles (user_id, role_id) VALUES (?, ?) ON CONFLICT (tenant_id, user_id, role_id) DO NOTHING`,
			userID, roleID,
		).Exec(ctx)
	}
	return err
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
