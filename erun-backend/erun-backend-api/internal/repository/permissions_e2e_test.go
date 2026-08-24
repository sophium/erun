package repository

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	eruncommon "github.com/sophium/erun/erun-common"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// The capability answer and the enforcement answer must agree exactly: a
// capability set that disagrees with the authorizer is worse than none, because
// it teaches a user to expect the wrong thing. Both read role_permissions
// through RLS, so the property is exercised against a real migrated PostgreSQL
// rather than a fake that agrees with itself.
func permissionsDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_PERMISSIONS_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_PERMISSIONS_DATABASE_URL to a migrated PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	var tenantID string
	err = db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`,
		"permissions-e2e-"+time.Now().Format("20060102150405.000000"),
	).Scan(&tenantID)
	mustNoErr(t, err, "seed tenant")
	t.Cleanup(func() { clearPermissionsTenant(t, db, tenantID) })
	return db, tenantID
}

func clearPermissionsTenant(t *testing.T, db *sql.DB, tenantID string) {
	t.Helper()
	for _, table := range []string{"user_roles", "role_permissions", "roles", "users", "tenants"} {
		if _, err := db.Exec(`DELETE FROM `+table+` WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing %s for tenant %s: %v", table, tenantID, err)
		}
	}
}

func seedPermissionsUser(t *testing.T, db *sql.DB, tenantID, username string) string {
	t.Helper()
	var userID string
	err := db.QueryRow(
		`INSERT INTO users (tenant_id, username) VALUES ($1, $2) RETURNING user_id`,
		tenantID, username,
	).Scan(&userID)
	mustNoErr(t, err, "seed user "+username)
	return userID
}

type seededPermission struct {
	method        string
	path          string
	methodPattern string
	pathPattern   string
}

// grantRole creates a role carrying permissions and assigns it to the user,
// mirroring how the identity bootstrap grants ReadAll/WriteAll.
func grantRole(t *testing.T, db *sql.DB, tenantID, userID, name string, permissions []seededPermission) {
	t.Helper()
	var roleID string
	err := db.QueryRow(
		`INSERT INTO roles (tenant_id, name) VALUES ($1, $2) RETURNING role_id`,
		tenantID, name,
	).Scan(&roleID)
	mustNoErr(t, err, "seed role "+name)
	for _, permission := range permissions {
		_, err := db.Exec(`
			INSERT INTO role_permissions (tenant_id, role_id, api_method, api_path, api_method_pattern, api_path_pattern)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, tenantID, roleID,
			nullablePermissionValue(permission.method), nullablePermissionValue(permission.path),
			nullablePermissionValue(permission.methodPattern), nullablePermissionValue(permission.pathPattern))
		mustNoErr(t, err, "seed permission for role "+name)
	}
	_, err = db.Exec(`INSERT INTO user_roles (tenant_id, user_id, role_id) VALUES ($1, $2, $3)`, tenantID, userID, roleID)
	mustNoErr(t, err, "assign role "+name)
}

func nullablePermissionValue(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// permissionsCandidateRoutes stands in for the handler's route catalog: a
// realistic canonical route set, including path templates and the
// method/path pairs the pattern roles are meant to straddle.
var permissionsCandidateRoutes = []eruncommon.PlatformCapability{
	{Method: "GET", Path: "/v1/audit-events"},
	{Method: "GET", Path: "/v1/environments"},
	{Method: "POST", Path: "/v1/environments"},
	{Method: "DELETE", Path: "/v1/environments/{environment_id}"},
	{Method: "GET", Path: "/v1/reviews"},
	{Method: "POST", Path: "/v1/reviews"},
	{Method: "GET", Path: "/v1/reviews/{review_id}"},
	{Method: "PATCH", Path: "/v1/reviews/{review_id}/status"},
	{Method: "GET", Path: "/v1/reviews/merge-queue"},
	{Method: "GET", Path: "/v1/whoami"},
}

// TestCapabilitySetAgreesWithEnforcement is the property the whole capability
// contract rests on: every route the capability answer claims is permitted is
// one the authorizer lets through, and every route it omits is one the
// authorizer refuses.
func TestCapabilitySetAgreesWithEnforcement(t *testing.T) {
	db, tenantID := permissionsDatabase(t)
	authorizer := &PermissionAuthorizer{txs: NewTxManager(db, DialectPostgres)}

	for _, testCase := range []struct {
		name        string
		permissions []seededPermission
	}{
		{
			name: "pattern rules the predefined roles use",
			permissions: []seededPermission{
				{methodPattern: "^(GET|HEAD|OPTIONS)$", pathPattern: "^/.*$"},
			},
		},
		{
			name: "exact rules only",
			permissions: []seededPermission{
				{method: "GET", path: "/v1/whoami"},
				{method: "GET", path: "/v1/audit-events"},
			},
		},
		{
			name: "a narrow pattern that must not leak past its anchors",
			permissions: []seededPermission{
				{methodPattern: "^GET$", pathPattern: "^/v1/reviews$"},
			},
		},
		{
			name:        "no permissions at all",
			permissions: nil,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			userID := seedPermissionsUser(t, db, tenantID, "user-"+testCase.name)
			if len(testCase.permissions) > 0 {
				grantRole(t, db, tenantID, userID, "role-"+testCase.name, testCase.permissions)
			}
			ctx := security.WithContext(context.Background(), security.Context{
				TenantID: tenantID, TenantType: "COMPANY", ErunUserID: userID,
			})

			permitted, err := authorizer.PermittedRoutes(ctx, permissionsCandidateRoutes)
			mustNoErr(t, err, "resolve capabilities")
			capabilities := eruncommon.PlatformCapabilities(permitted)

			for _, route := range permissionsCandidateRoutes {
				claimed := capabilities.Allows(route.Method, route.Path)
				enforced := authorizer.Authorize(ctx, route.Method, route.Path)
				switch {
				case claimed && enforced != nil:
					t.Errorf("%s %s: capability set claims it is permitted, enforcement refuses it: %v", route.Method, route.Path, enforced)
				case !claimed && enforced == nil:
					t.Errorf("%s %s: capability set omits it, enforcement permits it", route.Method, route.Path)
				case !claimed && !errors.Is(enforced, ErrForbidden):
					t.Errorf("%s %s: expected a forbidden refusal, got %v", route.Method, route.Path, enforced)
				}
			}
		})
	}
}

// TestCapabilitySetIsEmptyRatherThanAbsentForAPermissionlessUser separates the
// two states a client must not conflate: resolving no capabilities is an
// answer, and it is not an error.
func TestCapabilitySetIsEmptyRatherThanAbsentForAPermissionlessUser(t *testing.T) {
	db, tenantID := permissionsDatabase(t)
	authorizer := &PermissionAuthorizer{txs: NewTxManager(db, DialectPostgres)}
	userID := seedPermissionsUser(t, db, tenantID, "permissionless")
	ctx := security.WithContext(context.Background(), security.Context{
		TenantID: tenantID, TenantType: "COMPANY", ErunUserID: userID,
	})

	permitted, err := authorizer.PermittedRoutes(ctx, permissionsCandidateRoutes)
	mustNoErr(t, err, "resolve capabilities")
	if len(permitted) != 0 {
		t.Fatalf("expected no capabilities, got %+v", permitted)
	}
}
