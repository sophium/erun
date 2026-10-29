package backendapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/routeroles"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/testpg"
	modelpkg "github.com/sophium/erun/erun-common/base/model"
)

func rolePolicyDatabase(t *testing.T) *sql.DB {
	t.Helper()
	databaseURL := testpg.URL(t)
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })
	return db
}

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

func bootstrapTenantFirstUser(t *testing.T, db *sql.DB, issuer string, subject string) model.User {
	t.Helper()
	identities := repository.NewIdentityRepository(db, repository.DialectPostgres, "")
	_, user, err := identities.ResolveIdentity(context.Background(), security.Claims{Issuer: issuer, Subject: subject, Username: subject})
	mustNoErr(t, err, "bootstrap first user")
	return user
}

func enrollUser(t *testing.T, txManager *repository.TxManager, tenantID string, tenantType model.TenantType, params repository.CreateUserParams) model.User {
	t.Helper()
	users := repository.NewUserRepository(txManager)
	ctx := security.WithContext(context.Background(), security.Context{TenantID: tenantID, TenantType: string(tenantType)})
	user, _, err := users.Create(ctx, params)
	mustNoErr(t, err, "enroll user")
	return user
}

// realProtectedRouteCatalog uses the routes this binary really wires, so it cannot drift.
func realProtectedRouteCatalog(t *testing.T, db *sql.DB) []modelpkg.PlatformCapability {
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

func assertEffectiveAccessMatches(t *testing.T, authorizer *repository.PermissionAuthorizer, ctx context.Context, candidates []modelpkg.PlatformCapability, expectAllowed func(routeroles.Roles) bool) {
	t.Helper()
	for _, route := range candidates {
		granted, ok := routeroles.Routes[route.Method+" "+route.Path]
		if !ok {
			t.Fatalf("%s %s has no routeroles classification -- fix routeroles.Routes before trusting this assertion", route.Method, route.Path)
		}
		err := authorizer.Authorize(ctx, route.Method, route.Path)
		switch wantAllowed := expectAllowed(granted); {
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

	assertEffectiveAccessMatches(t, authorizer, ctx, candidates, func(granted routeroles.Roles) bool {
		return granted&routeroles.TenantUserRole != 0
	})

	// admin only makes member a non-first enrollment.
	_ = admin
}

// TestTenantAdminCannotReachOperationsRoutesEvenInsideOperationsTenant: the OPERATIONS tenant's
// first user keeps ReadAll/WriteAll; TenantAdmin granted to a colleague must stay below platform
// operator.
func TestTenantAdminCannotReachOperationsRoutesEvenInsideOperationsTenant(t *testing.T) {
	db := rolePolicyDatabase(t)
	tenantID, issuer := seedTenantWithIssuer(t, db, model.TenantTypeOperations, "tenant-admin-ops")
	operator := bootstrapTenantFirstUser(t, db, issuer, "ops-operator-subject")

	txManager := repository.NewTxManager(db, repository.DialectPostgres)
	operatorCtx := security.WithContext(context.Background(), securityContextFor(tenantID, model.TenantTypeOperations, operator.UserID))
	roles := repository.NewRoleRepository(txManager)
	// List has no tenant filter for erun_operations; it is called only to create the
	// narrower roles, and the role id is read back with an explicit tenant_id filter.
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

	assertEffectiveAccessMatches(t, authorizer, ctx, candidates, func(granted routeroles.Roles) bool {
		return granted&routeroles.TenantAdminRole != 0
	})
}

// TestTenantAgentDrivesTheMergeQueueAndNothingElse: TenantAgent may self-report POST /v1/builds and
// report merges, but not read build history or promote/override the queue.
func TestTenantAgentDrivesTheMergeQueueAndNothingElse(t *testing.T) {
	db := rolePolicyDatabase(t)
	tenantID, issuer := seedTenantWithIssuer(t, db, model.TenantTypeCompany, "tenant-agent")
	admin := bootstrapTenantFirstUser(t, db, issuer, "admin-subject")

	txManager := repository.NewTxManager(db, repository.DialectPostgres)
	roles := repository.NewRoleRepository(txManager)
	adminCtx := security.WithContext(context.Background(), securityContextFor(tenantID, model.TenantTypeCompany, admin.UserID))
	// List creates TenantAgent lazily; the id is read back with an explicit tenant_id filter.
	_, err := roles.List(adminCtx)
	mustNoErr(t, err, "list roles (ensures TenantAgent exists)")
	var agentRoleID string
	mustNoErr(t, db.QueryRow(`SELECT role_id FROM roles WHERE tenant_id = $1 AND name = 'TenantAgent'`, tenantID).Scan(&agentRoleID), "read this tenant's TenantAgent role id")

	agent := enrollUser(t, txManager, tenantID, model.TenantTypeCompany, repository.CreateUserParams{
		Username: "env-identity", Issuer: issuer, Subject: "env-machine-subject", RoleIDs: []string{agentRoleID},
	})

	authorizer := repository.NewPermissionAuthorizerForDialect(db, repository.DialectPostgres)
	ctx := security.WithContext(context.Background(), securityContextFor(tenantID, model.TenantTypeCompany, agent.UserID))
	candidates := realProtectedRouteCatalog(t, db)

	assertEffectiveAccessMatches(t, authorizer, ctx, candidates, func(granted routeroles.Roles) bool {
		return granted&routeroles.TenantAgentRole != 0
	})

	// Proves TenantAgent is narrower than TenantUser.
	if err := authorizer.Authorize(ctx, "GET", "/v1/builds"); !errors.Is(err, repository.ErrForbidden) {
		t.Fatalf("expected the machine role to be refused the tenant-wide build history, got %v", err)
	}
	if err := authorizer.Authorize(ctx, "POST", "/v1/builds"); err != nil {
		t.Fatalf("expected the machine role to be permitted its own build self-report, got %v", err)
	}
}

// TestWriteAllHolderRetainsAccessAfterRolloutOfNarrowerRoles: introducing the narrower roles must
// not revoke or shadow a legacy ReadAll/WriteAll grant.
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
	allowEverything := func(routeroles.Roles) bool { return true }

	assertEffectiveAccessMatches(t, authorizer, ctx, candidates, allowEverything)

	// List triggers the lazy role backfill for this tenant.
	roles := repository.NewRoleRepository(repository.NewTxManager(db, repository.DialectPostgres))
	_, err := roles.List(ctx)
	mustNoErr(t, err, "list roles (triggers ensureNarrowerRolesExist)")

	assertEffectiveAccessMatches(t, authorizer, ctx, candidates, allowEverything)
}

// TestNewlyEnrolledUserDefaultsToTenantUserAndTenantStaysUsable: TenantUser is not grant-capable,
// yet the tenant must never lose its last grant-capable holder (ErrLastGrantCapableRole).
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
	assertEffectiveAccessMatches(t, authorizer, memberCtx, candidates, func(granted routeroles.Roles) bool {
		return granted&routeroles.TenantUserRole != 0
	})

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
	if err := roles.Revoke(adminCtx, admin.UserID, adminRoleID, ""); !errors.Is(err, repository.ErrLastGrantCapableRole) {
		t.Fatalf("expected revoking the tenant's only grant-capable role to be refused, got %v", err)
	}
}
