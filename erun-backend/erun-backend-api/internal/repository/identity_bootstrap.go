package repository

import (
	"context"
	"errors"
	"log"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/uptrace/bun"
)

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
	log.Printf("erun api identity enrolled first tenant/user tenant=%q tenantName=%q tenantNameSource=%q tenantType=%q user=%q issuer=%q subject=%q username=%q", tenant.TenantID, tenant.Name, bootstrapTenantNameSource(r.platformTenant), tenant.Type, user.UserID, claims.Issuer, claims.Subject, user.Username)
	return tenant, user, nil
}

// defaultBootstrapTenantName is used when ERUN_TENANT is not configured.
const defaultBootstrapTenantName = "operations"

// bootstrapTenantName prefers ERUN_TENANT so the first provision resolves the
// <tenant>-devops image the platform actually publishes.
func bootstrapTenantName(platformTenant string) string {
	if platformTenant != "" {
		return platformTenant
	}
	return defaultBootstrapTenantName
}

// bootstrapTenantNameSource names which branch chose the name, for logging.
func bootstrapTenantNameSource(platformTenant string) string {
	if platformTenant != "" {
		return "ERUN_TENANT"
	}
	return "fallback"
}

// insertFirstIdentity atomically creates the first OPERATIONS tenant, issuer and
// user. A non-empty tenants table yields ErrNotFound (another caller won).
func (r *IdentityRepository) insertFirstIdentity(ctx context.Context, tx bun.Tx, claims security.Claims) (model.Tenant, model.User, error) {
	var tenantCount int
	if err := tx.NewRaw(`SELECT COUNT(*) FROM tenants`).Scan(ctx, &tenantCount); err != nil {
		return model.Tenant{}, model.User{}, err
	}
	if tenantCount != 0 {
		return model.Tenant{}, model.User{}, ErrNotFound
	}

	tenant, err := r.insertTenant(ctx, tx, bootstrapTenantName(r.platformTenant), model.TenantTypeOperations)
	if err != nil {
		return model.Tenant{}, model.User{}, err
	}
	if err := r.bindSecurityContext(ctx, tx, security.Context{
		TenantID:   tenant.TenantID,
		TenantType: string(model.TenantTypeOperations),
	}); err != nil {
		return model.Tenant{}, model.User{}, err
	}

	orgFieldKey, orgFieldValue := bootstrapOrgScope(claims)
	if err := insertBootstrapIssuer(ctx, tx, claims.Issuer, orgFieldKey, orgFieldValue); err != nil {
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
	if err := r.insertDefaultUserAccess(ctx, tx, tenant.TenantID, user.UserID, claims.Issuer, claims.Subject); err != nil {
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

// insertFirstTenantUser enrols the subject as the tenant's first user; a tenant
// that already has users yields ErrNotFound.
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
	if err := r.insertTenantFirstUserAccess(ctx, tx, tenant, user.UserID, claims.Issuer, claims.Subject); err != nil {
		return model.User{}, err
	}
	return user, nil
}

// bindSecurityContext binds the transaction's RLS context (PostgreSQL only).
func (r *IdentityRepository) bindSecurityContext(ctx context.Context, tx bun.Tx, securityContext security.Context) error {
	if r.dialect != DialectPostgres {
		return nil
	}
	return r.setPostgresSecurityContext(ctx, tx, securityContext)
}

// bootstrapOrgScopeClaims are org claims of IdPs erun ships. Recording one at
// bootstrap keeps the shared issuer org-scoped; otherwise later tenants on it
// are refused with no API to undo it.
var bootstrapOrgScopeClaims = []string{
	// Zitadel; requires the urn:zitadel:iam:user:resourceowner scope.
	"urn:zitadel:iam:user:resourceowner:id",
}

// bootstrapOrgScope returns the org claim and value, or empty for single-tenant.
func bootstrapOrgScope(claims security.Claims) (string, string) {
	for _, key := range bootstrapOrgScopeClaims {
		value, ok := claims.Raw[key].(string)
		if ok && strings.TrimSpace(value) != "" {
			return key, strings.TrimSpace(value)
		}
	}
	return "", ""
}

// insertBootstrapIssuer registers the issuer, org-scoped when orgFieldKey is set.
func insertBootstrapIssuer(ctx context.Context, tx bun.Tx, issuer, orgFieldKey, orgFieldValue string) error {
	if _, err := tx.NewRaw(`INSERT INTO issuers (issuer, org_field_key) VALUES (?, NULLIF(?, '')) ON CONFLICT (issuer) DO NOTHING`, issuer, orgFieldKey).Exec(ctx); err != nil {
		return err
	}
	_, err := tx.NewRaw(`INSERT INTO tenant_issuers (issuer, name, org_field_value) VALUES (?, ?, NULLIF(?, ''))`, issuer, defaultTenantIssuerName(issuer), orgFieldValue).Exec(ctx)
	return err
}

// bootstrapUsername prefers the username claim and falls back to the subject.
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

// insertDefaultUserAccess links the external identity and grants ReadAll/WriteAll
// for an OPERATIONS tenant's first user. tenantID must be explicit: under
// erun_operations RLS is cross-tenant, so an untenanted role lookup could match
// another tenant's role.
func (r *IdentityRepository) insertDefaultUserAccess(ctx context.Context, tx bun.Tx, tenantID string, userID string, issuer string, subject string) error {
	if err := insertUserExternalIdentity(ctx, tx, userID, issuer, subject); err != nil {
		return err
	}
	if err := grantPredefinedRoles(ctx, tx, tenantID, userID); err != nil {
		return err
	}
	return ensureNarrowerRolesExist(ctx, tx, tenantID)
}

// insertTenantFirstUserAccess grants a COMPANY tenant's first user TenantAdmin;
// an OPERATIONS tenant's first user gets insertDefaultUserAccess's wildcard roles.
func (r *IdentityRepository) insertTenantFirstUserAccess(ctx context.Context, tx bun.Tx, tenant model.Tenant, userID string, issuer string, subject string) error {
	if tenant.Type == model.TenantTypeOperations {
		return r.insertDefaultUserAccess(ctx, tx, tenant.TenantID, userID, issuer, subject)
	}
	if err := insertUserExternalIdentity(ctx, tx, userID, issuer, subject); err != nil {
		return err
	}
	return grantFirstTenantUserRole(ctx, tx, tenant.TenantID, userID)
}

// insertUserExternalIdentity links the external identity; tenant_id comes from
// the column default.
func insertUserExternalIdentity(ctx context.Context, tx bun.Tx, userID string, issuer string, subject string) error {
	_, err := tx.NewRaw(`INSERT INTO user_external_ids (user_id, issuer, external_id) VALUES (?, ?, ?)`, userID, issuer, subject).Exec(ctx)
	return err
}

// grantPredefinedRoles grants ReadAll/WriteAll under an explicit tenantID (see
// insertDefaultUserAccess).
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

// findOrCreateRole returns the named role's id, creating it if needed.
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

// grantRolePermissionPattern is idempotent because the role may already carry it.
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
