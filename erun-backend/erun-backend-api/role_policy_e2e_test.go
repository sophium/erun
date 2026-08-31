package backendapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/routeroles"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	eruncommon "github.com/sophium/erun/erun-common"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// This file drives erun#1684's role model end to end against a real,
// migrated PostgreSQL and the real handler's own registered route catalog —
// the same "real database, real authorizer, real routes" standard
// capabilities_e2e_test.go already holds the whoami capability contract to.

func rolePolicyDatabase(t *testing.T) *sql.DB {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_PERMISSIONS_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_PERMISSIONS_DATABASE_URL to a migrated PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seedTenantWithIssuer registers a tenant and a single-tenant issuer pointing
// at it, so IdentityRepository.ResolveIdentity can bootstrap the tenant's
// first user the same way a real sign-in would.
func seedTenantWithIssuer(t *testing.T, db *sql.DB, tenantType model.TenantType, namePrefix string) (tenantID string, issuer string) {
	t.Helper()
	stamp := time.Now().Format("20060102150405.000000")
	name := namePrefix + "-" + stamp
	issuer = "https://issuer.example/" + name
	mustNoErr(t, db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, $2) RETURNING tenant_id`,
		name, string(tenantType),
	).Scan(&tenantID), "seed tenant")
	_, err := db.Exec(`INSERT INTO issuers (issuer) VALUES ($1)`, issuer)
	mustNoErr(t, err, "seed issuer")
	_, err = db.Exec(`INSERT INTO tenant_issuers (issuer, tenant_id, name) VALUES ($1, $2, $3)`, issuer, tenantID, name)
	mustNoErr(t, err, "seed tenant_issuer")
	t.Cleanup(func() {
		for _, table := range []string{"user_external_ids", "user_roles", "role_permissions", "roles", "users", "tenant_issuers"} {
			if _, err := db.Exec(`DELETE FROM `+table+` WHERE tenant_id = $1`, tenantID); err != nil {
				t.Logf("clearing %s for tenant %s: %v", table, tenantID, err)
			}
		}
		if _, err := db.Exec(`DELETE FROM issuers WHERE issuer = $1`, issuer); err != nil {
			t.Logf("clearing issuers for %s: %v", issuer, err)
		}
		if _, err := db.Exec(`DELETE FROM tenants WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing tenants for %s: %v", tenantID, err)
		}
	})
	return tenantID, issuer
}

// bootstrapTenantFirstUser drives the real per-tenant first-user sign-in path
// (IdentityRepository.ResolveIdentity -> insertFirstTenantUser ->
// insertTenantFirstUserAccess), the same code a real token verification
// triggers on a tenant's first sign-in.
func bootstrapTenantFirstUser(t *testing.T, db *sql.DB, issuer string, subject string) model.User {
	t.Helper()
	identities := repository.NewIdentityRepository(db, repository.DialectPostgres, "")
	_, user, err := identities.ResolveIdentity(context.Background(), security.Claims{Issuer: issuer, Subject: subject, Username: subject})
	mustNoErr(t, err, "bootstrap first user")
	return user
}

// enrollUser drives the real ordinary-enrollment path
// (UserRepository.Create -> assignEnrollmentRoles), as an authenticated
// caller already resolved to tenantID/tenantType would.
func enrollUser(t *testing.T, txManager *repository.TxManager, tenantID string, tenantType model.TenantType, params repository.CreateUserParams) model.User {
	t.Helper()
	users := repository.NewUserRepository(txManager)
	ctx := security.WithContext(context.Background(), security.Context{TenantID: tenantID, TenantType: string(tenantType)})
	user, _, err := users.Create(ctx, params)
	mustNoErr(t, err, "enroll user")
	return user
}

// realProtectedRouteCatalog builds the handler's actual registered protected
// route set the same way TestCapabilityCandidatesAreTheHandlersOwnRoutes
// does: every RegisterXRoutes call this binary really wires, not a hand
// -typed candidate list that could drift from it.
func realProtectedRouteCatalog(t *testing.T, db *sql.DB) []eruncommon.PlatformCapability {
	t.Helper()
	options := HandlerOptions{DB: db, DBDialect: repository.DialectPostgres}
	auth, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier:  TokenVerifierFunc(func(context.Context, string) (Claims, error) { return Claims{}, nil }),
		TenantResolver: TenantResolverFunc(func(context.Context, Claims) (Tenant, error) { return Tenant{}, nil }),
		UserResolver:   UserResolverFunc(func(context.Context, string, string, string) (User, error) { return User{}, nil }),
	})
	mustNoErr(t, err, "build auth middleware")
	mux := http.NewServeMux()
	catalog := registerProtectedRoutes(mux, auth, options, repository.NewTxManager(options.DB, options.DBDialect), AuthorizerFunc(func(context.Context, string, string) error { return nil }))
	candidates := catalog.sorted()
	if len(candidates) == 0 {
		t.Fatal("expected the handler's real protected routes, got none")
	}
	return candidates
}

// assertEffectiveAccessMatches drives the real, database-backed authorizer
// against every real registered route and fails when its decision disagrees
// with expectAllowed(routeroles' classification of that route) -- proof
// against the actual routes and the actual enforcement code, not by
// inspecting routeroles.Routes' patterns.
func assertEffectiveAccessMatches(t *testing.T, authorizer *repository.PermissionAuthorizer, ctx context.Context, candidates []eruncommon.PlatformCapability, expectAllowed func(routeroles.Class) bool) {
	t.Helper()
	for _, route := range candidates {
		class, ok := routeroles.Routes[route.Method+" "+route.Path]
		if !ok {
			t.Fatalf("%s %s has no routeroles classification -- fix routeroles.Routes before trusting this assertion", route.Method, route.Path)
		}
		err := authorizer.Authorize(ctx, route.Method, route.Path)
		switch wantAllowed := expectAllowed(class); {
		case wantAllowed && err != nil:
			t.Errorf("%s %s: expected to be permitted, was refused: %v", route.Method, route.Path, err)
		case !wantAllowed && err == nil:
			t.Errorf("%s %s: expected to be refused, was permitted", route.Method, route.Path)
		case !wantAllowed && !errors.Is(err, repository.ErrForbidden):
			t.Errorf("%s %s: expected a forbidden refusal, got %v", route.Method, route.Path, err)
		}
	}
}

func securityContextFor(tenantID string, tenantType model.TenantType, userID string) security.Context {
	return security.Context{TenantID: tenantID, TenantType: string(tenantType), ErunUserID: userID}
}

// TestTenantUserCannotAdministerTheTenant is erun#1684's first mandatory
// test: TenantUser cannot create or delete environments, register contexts,
// or manage users, invites, roles or org settings -- driven through every
// real registered route, not by inspecting routeroles' patterns. It also
// pins the positive half: TenantUser really is permitted everything
// routeroles classifies for it (reading the tenant, driving reviews/
// comments/builds/the merge queue, operating existing environments).
func TestTenantUserCannotAdministerTheTenant(t *testing.T) {
	db := rolePolicyDatabase(t)
	tenantID, issuer := seedTenantWithIssuer(t, db, model.TenantTypeCompany, "tenant-user")
	admin := bootstrapTenantFirstUser(t, db, issuer, "admin-subject")

	txManager := repository.NewTxManager(db, repository.DialectPostgres)
	member := enrollUser(t, txManager, tenantID, model.TenantTypeCompany, repository.CreateUserParams{
		Username: "member", Issuer: issuer, Subject: "member-subject",
	})

	authorizer := repository.NewPermissionAuthorizerForDialect(db, repository.DialectPostgres)
	ctx := security.WithContext(context.Background(), securityContextFor(tenantID, model.TenantTypeCompany, member.UserID))
	candidates := realProtectedRouteCatalog(t, db)

	assertEffectiveAccessMatches(t, authorizer, ctx, candidates, func(class routeroles.Class) bool {
		return class == routeroles.TenantUserClass
	})

	// admin exists only to make member a non-first enrollment; silence unused warnings if reordered.
	_ = admin
}

// TestTenantAdminCannotReachOperationsRoutesEvenInsideOperationsTenant is
// erun#1684's second mandatory test: TenantAdmin cannot reach any
// operations-gated route, including from inside the OPERATIONS tenant. The
// tenant's own first user still gets ReadAll/WriteAll (insertFirstTenantUser
// keeps the platform-operator grant for an OPERATIONS tenant type, since
// this instance's own root-resolution capabilities live only behind it and
// no other user exists yet to grant them). The real gap the issue
// identifies is the *second* user: today every additional user in the
// OPERATIONS tenant can only ever be granted the same wildcard reach.
// TenantAdmin is what makes "administers this tenant's own users and
// environments, but is not a platform operator" finally expressible, so
// this test grants it to a colleague the same way the operator actually
// would (an explicit roleIds enrollment) and proves it stays a genuinely
// lesser position.
func TestTenantAdminCannotReachOperationsRoutesEvenInsideOperationsTenant(t *testing.T) {
	db := rolePolicyDatabase(t)
	tenantID, issuer := seedTenantWithIssuer(t, db, model.TenantTypeOperations, "tenant-admin-ops")
	operator := bootstrapTenantFirstUser(t, db, issuer, "ops-operator-subject")

	txManager := repository.NewTxManager(db, repository.DialectPostgres)
	operatorCtx := security.WithContext(context.Background(), securityContextFor(tenantID, model.TenantTypeOperations, operator.UserID))
	roles := repository.NewRoleRepository(txManager)
	// RoleRepository.List's query has no tenant_id filter of its own (relying
	// on RLS, which erun_operations deliberately bypasses cross-tenant), so
	// an operations-scoped caller's List sees every tenant's roles -- calling
	// it here only to trigger ensureNarrowerRolesExist's lazy creation for
	// this tenant. The role id itself is read back with an explicit
	// tenant_id filter below so this test cannot accidentally borrow another
	// tenant's TenantAdmin row.
	_, err := roles.List(operatorCtx)
	mustNoErr(t, err, "list roles (ensures TenantAdmin exists in this tenant)")
	var tenantAdminRoleID string
	mustNoErr(t, db.QueryRow(`SELECT role_id FROM roles WHERE tenant_id = $1 AND name = 'TenantAdmin'`, tenantID).Scan(&tenantAdminRoleID), "read this tenant's TenantAdmin role id")

	tenantAdminUser := enrollUser(t, txManager, tenantID, model.TenantTypeOperations, repository.CreateUserParams{
		Username: "ops-tenant-admin", Issuer: issuer, Subject: "ops-tenant-admin-subject", RoleIDs: []string{tenantAdminRoleID},
	})

	authorizer := repository.NewPermissionAuthorizerForDialect(db, repository.DialectPostgres)
	ctx := security.WithContext(context.Background(), securityContextFor(tenantID, model.TenantTypeOperations, tenantAdminUser.UserID))
	candidates := realProtectedRouteCatalog(t, db)

	assertEffectiveAccessMatches(t, authorizer, ctx, candidates, func(class routeroles.Class) bool {
		return class == routeroles.TenantUserClass || class == routeroles.TenantAdminOnly
	})
}

// TestWriteAllHolderRetainsAccessAfterRolloutOfNarrowerRoles is erun#1684's
// migration-safety test: a user who already holds WriteAll (seeded exactly
// the way every enrolled user's WriteAll grant looked before this change)
// keeps full effective access after TenantUser/TenantAdmin exist -- both
// immediately, and after something else (RoleRepository.List) triggers the
// lazy ensureNarrowerRolesExist backfill for the same tenant. Nothing about
// introducing the two narrower roles may revoke or shadow an existing
// ReadAll/WriteAll grant.
func TestWriteAllHolderRetainsAccessAfterRolloutOfNarrowerRoles(t *testing.T) {
	db := rolePolicyDatabase(t)
	tenantID, _ := seedTenantWithIssuer(t, db, model.TenantTypeCompany, "writeall-holder")

	var userID string
	mustNoErr(t, db.QueryRow(`INSERT INTO users (tenant_id, username) VALUES ($1, 'writeall-holder') RETURNING user_id`, tenantID).Scan(&userID), "seed user")
	for _, spec := range []struct {
		name          string
		methodPattern string
	}{
		{"ReadAll", "^(GET|HEAD|OPTIONS)$"},
		{"WriteAll", "^(POST|PUT|PATCH|DELETE)$"},
	} {
		var roleID string
		mustNoErr(t, db.QueryRow(`INSERT INTO roles (tenant_id, name) VALUES ($1, $2) RETURNING role_id`, tenantID, spec.name).Scan(&roleID), "seed role "+spec.name)
		_, err := db.Exec(`INSERT INTO role_permissions (tenant_id, role_id, api_method_pattern, api_path_pattern) VALUES ($1, $2, $3, '^/.*$')`, tenantID, roleID, spec.methodPattern)
		mustNoErr(t, err, "seed role_permissions "+spec.name)
		_, err = db.Exec(`INSERT INTO user_roles (tenant_id, user_id, role_id) VALUES ($1, $2, $3)`, tenantID, userID, roleID)
		mustNoErr(t, err, "grant "+spec.name)
	}

	authorizer := repository.NewPermissionAuthorizerForDialect(db, repository.DialectPostgres)
	ctx := security.WithContext(context.Background(), securityContextFor(tenantID, model.TenantTypeCompany, userID))
	candidates := realProtectedRouteCatalog(t, db)
	allowEverything := func(routeroles.Class) bool { return true }

	assertEffectiveAccessMatches(t, authorizer, ctx, candidates, allowEverything)

	// Trigger the lazy TenantUser/TenantAdmin backfill for this same tenant
	// (RoleRepository.List's own ensureNarrowerRolesExist call) and re-assert:
	// creating and granting the two new roles must not touch this user's
	// pre-existing ReadAll/WriteAll grants.
	roles := repository.NewRoleRepository(repository.NewTxManager(db, repository.DialectPostgres))
	_, err := roles.List(ctx)
	mustNoErr(t, err, "list roles (triggers ensureNarrowerRolesExist)")

	assertEffectiveAccessMatches(t, authorizer, ctx, candidates, allowEverything)
}

// TestNewlyEnrolledUserDefaultsToTenantUserAndTenantStaysUsable is erun#1684's
// last mandatory test: a newly enrolled user (no caller-supplied roleIds)
// gets exactly TenantUser -- not the previous zero-role default -- and the
// tenant is still never left unable to grant roles (routes/users.go's
// existing concern, ErrLastGrantCapableRole), even though TenantUser itself
// is deliberately not grant-capable.
func TestNewlyEnrolledUserDefaultsToTenantUserAndTenantStaysUsable(t *testing.T) {
	db := rolePolicyDatabase(t)
	tenantID, issuer := seedTenantWithIssuer(t, db, model.TenantTypeCompany, "default-enrollment")
	admin := bootstrapTenantFirstUser(t, db, issuer, "admin-subject")

	txManager := repository.NewTxManager(db, repository.DialectPostgres)
	member := enrollUser(t, txManager, tenantID, model.TenantTypeCompany, repository.CreateUserParams{
		Username: "newcomer", Issuer: issuer, Subject: "newcomer-subject",
	})

	roles := repository.NewRoleRepository(txManager)
	memberCtx := security.WithContext(context.Background(), securityContextFor(tenantID, model.TenantTypeCompany, member.UserID))
	assigned, err := roles.ForUser(memberCtx, member.UserID)
	mustNoErr(t, err, "read newcomer's roles")
	if len(assigned) != 1 || assigned[0].Name != "TenantUser" {
		t.Fatalf("expected the newcomer to hold exactly TenantUser, got %+v", assigned)
	}

	authorizer := repository.NewPermissionAuthorizerForDialect(db, repository.DialectPostgres)
	candidates := realProtectedRouteCatalog(t, db)
	assertEffectiveAccessMatches(t, authorizer, memberCtx, candidates, func(class routeroles.Class) bool {
		return class == routeroles.TenantUserClass
	})

	// The tenant must still never be left unable to grant roles: admin is the
	// only TenantAdmin (grant-capable) holder, and member's TenantUser is not
	// grant-capable, so revoking admin's TenantAdmin must be refused.
	adminCtx := security.WithContext(context.Background(), securityContextFor(tenantID, model.TenantTypeCompany, admin.UserID))
	adminRoles, err := roles.ForUser(adminCtx, admin.UserID)
	mustNoErr(t, err, "read admin's roles")
	var adminRoleID string
	for _, role := range adminRoles {
		if role.Name == "TenantAdmin" {
			adminRoleID = role.RoleID
		}
	}
	if adminRoleID == "" {
		t.Fatalf("expected the tenant's first user to hold TenantAdmin, got %+v", adminRoles)
	}
	if err := roles.Revoke(adminCtx, admin.UserID, adminRoleID); !errors.Is(err, repository.ErrLastGrantCapableRole) {
		t.Fatalf("expected revoking the tenant's only grant-capable role to be refused, got %v", err)
	}
}
