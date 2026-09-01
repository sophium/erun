package repository

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Role assignment writes real transaction-local RLS role switches and reads
// role_permissions through the same matcher Authorize enforces with, so it is
// exercised against a real migrated PostgreSQL rather than a fake that agrees
// with itself.
func rolesDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_ROLES_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_ROLES_DATABASE_URL to a migrated PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	var tenantID string
	err = db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`,
		"roles-e2e-"+time.Now().Format("20060102150405.000000"),
	).Scan(&tenantID)
	mustNoErr(t, err, "seed tenant")
	t.Cleanup(func() { clearPermissionsTenant(t, db, tenantID) })
	return db, tenantID
}

func rolesContext(tenantID, userID string) context.Context {
	return security.WithContext(context.Background(), security.Context{
		TenantID: tenantID, TenantType: "COMPANY", ErunUserID: userID,
	})
}

// TestRoleRepositoryCreateGrantAndListRoundTrip proves the whole assignment
// path end to end: a role narrowed to one path can be created, granted to a
// user, and read back both from the tenant's role list and from that user's
// own roles.
func TestRoleRepositoryCreateGrantAndListRoundTrip(t *testing.T) {
	db, tenantID := rolesDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	roles := &RoleRepository{txs: txs}
	admin := seedPermissionsUser(t, db, tenantID, "admin")
	reviewer := seedPermissionsUser(t, db, tenantID, "reviewer")
	ctx := rolesContext(tenantID, admin)

	role, err := roles.Create(ctx, "ReviewsReader", []RolePermissionInput{
		{APIMethod: "GET", APIPath: "/v1/reviews"},
	})
	mustNoErr(t, err, "create role")
	if role.RoleID == "" || len(role.Permissions) != 1 {
		t.Fatalf("expected a created role with one permission, got %+v", role)
	}

	if _, err := roles.Grant(ctx, reviewer, role.RoleID, ""); err != nil {
		t.Fatalf("grant: %v", err)
	}

	tenantRoles, err := roles.List(ctx)
	mustNoErr(t, err, "list tenant roles")
	assertRoleListedWithPermission(t, tenantRoles, role.RoleID, "GET", "/v1/reviews")

	userRoles, err := roles.ForUser(ctx, reviewer)
	mustNoErr(t, err, "list user roles")
	if len(userRoles) != 1 || userRoles[0].RoleID != role.RoleID {
		t.Fatalf("expected the reviewer to hold exactly the granted role, got %+v", userRoles)
	}
}

// assertRoleListedWithPermission asserts roleID appears in roles with exactly
// one permission matching the given exact method/path.
func assertRoleListedWithPermission(t *testing.T, roles []model.Role, roleID, method, path string) {
	t.Helper()
	for _, r := range roles {
		if r.RoleID != roleID {
			continue
		}
		if len(r.Permissions) != 1 || r.Permissions[0].APIMethod != method || r.Permissions[0].APIPath != path {
			t.Fatalf("expected the role's permission to round-trip, got %+v", r.Permissions)
		}
		return
	}
	t.Fatal("expected the created role in the tenant's role list")
}

// TestRoleRepositoryCustomRoleIsPermittedItsOwnPathAndRefusedEveryOther is the
// permit-and-refuse property the feature exists to enable: a role narrowed to
// one exact method/path must let a caller through to that path and refuse
// every other path, checked against the real authorizer.
func TestRoleRepositoryCustomRoleIsPermittedItsOwnPathAndRefusedEveryOther(t *testing.T) {
	db, tenantID := rolesDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	roles := &RoleRepository{txs: txs}
	authorizer := &PermissionAuthorizer{txs: txs}
	admin := seedPermissionsUser(t, db, tenantID, "admin")
	narrow := seedPermissionsUser(t, db, tenantID, "narrow")
	ctx := rolesContext(tenantID, admin)

	role, err := roles.Create(ctx, "ReviewsReader", []RolePermissionInput{
		{APIMethod: "GET", APIPath: "/v1/reviews"},
	})
	mustNoErr(t, err, "create role")
	if _, err := roles.Grant(ctx, narrow, role.RoleID, ""); err != nil {
		t.Fatalf("grant: %v", err)
	}

	narrowCtx := rolesContext(tenantID, narrow)
	if err := authorizer.Authorize(narrowCtx, "GET", "/v1/reviews"); err != nil {
		t.Fatalf("expected the narrow role's own path to be permitted, got %v", err)
	}
	for _, refused := range []struct{ method, path string }{
		{"GET", "/v1/whoami"},
		{"POST", "/v1/reviews"},
		{"GET", "/v1/reviews/{review_id}"},
	} {
		err := authorizer.Authorize(narrowCtx, refused.method, refused.path)
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s %s: expected forbidden, got %v", refused.method, refused.path, err)
		}
	}
}

// TestRoleRepositoryExistingReadAllWriteAllUserIsUnaffected proves the
// assignment layer is purely additive: a user holding the predefined
// ReadAll+WriteAll roles (the bootstrap shape) keeps exactly that access after
// this feature is exercised alongside it.
func TestRoleRepositoryExistingReadAllWriteAllUserIsUnaffected(t *testing.T) {
	db, tenantID := rolesDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	authorizer := &PermissionAuthorizer{txs: txs}
	admin := seedPermissionsUser(t, db, tenantID, "admin")
	grantRole(t, db, tenantID, admin, "ReadAll", []seededPermission{
		{methodPattern: "^(GET|HEAD|OPTIONS)$", pathPattern: "^/.*$"},
	})
	grantRole(t, db, tenantID, admin, "WriteAll", []seededPermission{
		{methodPattern: "^(POST|PUT|PATCH|DELETE)$", pathPattern: "^/.*$"},
	})

	ctx := rolesContext(tenantID, admin)
	for _, permitted := range []struct{ method, path string }{
		{"GET", "/v1/reviews"},
		{"POST", "/v1/reviews"},
		{"DELETE", "/v1/environments/{environment_id}"},
	} {
		if err := authorizer.Authorize(ctx, permitted.method, permitted.path); err != nil {
			t.Fatalf("%s %s: expected the untouched ReadAll+WriteAll user to still be permitted, got %v", permitted.method, permitted.path, err)
		}
	}
}

// TestRoleRepositoryRevokeRefusesLastGrantCapableRole is the lockout guard
// the feature must make impossible: revoking the tenant's only role that can
// grant roles is refused, and the assignment is left exactly as it was.
func TestRoleRepositoryRevokeRefusesLastGrantCapableRole(t *testing.T) {
	db, tenantID := rolesDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	roles := &RoleRepository{txs: txs}
	admin := seedPermissionsUser(t, db, tenantID, "sole-admin")
	grantRole(t, db, tenantID, admin, "WriteAll", []seededPermission{
		{methodPattern: "^(POST|PUT|PATCH|DELETE)$", pathPattern: "^/.*$"},
	})

	var writeAllRoleID string
	err := db.QueryRow(`SELECT role_id FROM roles WHERE tenant_id = $1 AND name = 'WriteAll'`, tenantID).Scan(&writeAllRoleID)
	mustNoErr(t, err, "look up seeded WriteAll role id")

	ctx := rolesContext(tenantID, admin)
	err = roles.Revoke(ctx, admin, writeAllRoleID, "")
	if !errors.Is(err, ErrLastGrantCapableRole) {
		t.Fatalf("expected ErrLastGrantCapableRole, got %v", err)
	}

	var stillAssigned int
	mustNoErr(t, db.QueryRow(
		`SELECT COUNT(*) FROM user_roles WHERE tenant_id = $1 AND user_id = $2 AND role_id = $3`,
		tenantID, admin, writeAllRoleID,
	).Scan(&stillAssigned), "count assignment after refused revoke")
	if stillAssigned != 1 {
		t.Fatalf("expected the refused revoke to leave the assignment in place, found %d rows", stillAssigned)
	}
}

// TestRoleRepositoryRevokeSucceedsWhenAnotherGrantCapableUserRemains is the
// other side of the same guard: revoking is fine as long as the tenant is not
// left with zero grant-capable users.
func TestRoleRepositoryRevokeSucceedsWhenAnotherGrantCapableUserRemains(t *testing.T) {
	db, tenantID := rolesDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	roles := &RoleRepository{txs: txs}
	first := seedPermissionsUser(t, db, tenantID, "first-admin")
	second := seedPermissionsUser(t, db, tenantID, "second-admin")
	grantRole(t, db, tenantID, first, "WriteAll", []seededPermission{
		{methodPattern: "^(POST|PUT|PATCH|DELETE)$", pathPattern: "^/.*$"},
	})

	var writeAllRoleID string
	err := db.QueryRow(`SELECT role_id FROM roles WHERE tenant_id = $1 AND name = 'WriteAll'`, tenantID).Scan(&writeAllRoleID)
	mustNoErr(t, err, "look up seeded WriteAll role id")
	_, err = db.Exec(`INSERT INTO user_roles (tenant_id, user_id, role_id) VALUES ($1, $2, $3)`, tenantID, second, writeAllRoleID)
	mustNoErr(t, err, "assign WriteAll to second admin")

	ctx := rolesContext(tenantID, first)
	if err := roles.Revoke(ctx, first, writeAllRoleID, ""); err != nil {
		t.Fatalf("expected revoke to succeed while another grant-capable user remains, got %v", err)
	}
}

// TestRoleRepositoryRevokeGuardCountsOnlyTheTargetTenant is the guard's
// cross-tenant half. erun_operations bypasses RLS, so before the tenant filter
// went in, both of Revoke's statements saw the whole platform: the delete would
// remove a matching (user_id, role_id) row in whatever tenant it lived in, and
// the lockout guard would pass as long as *some other* tenant still had a
// grant-capable user. An operations caller could therefore take the last admin
// role off a tenant and brick it — the exact state this guard exists to make
// impossible, silently not held for the only caller who can reach across
// tenants.
func TestRoleRepositoryRevokeGuardCountsOnlyTheTargetTenant(t *testing.T) {
	db, tenantTarget := rolesDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	roles := &RoleRepository{txs: txs}

	// The target tenant's sole grant-capable user.
	soleAdmin := seedPermissionsUser(t, db, tenantTarget, "sole-admin")
	grantRole(t, db, tenantTarget, soleAdmin, "WriteAll", []seededPermission{
		{methodPattern: "^(POST|PUT|PATCH|DELETE)$", pathPattern: "^/.*$"},
	})
	var writeAllRoleID string
	mustNoErr(t, db.QueryRow(
		`SELECT role_id FROM roles WHERE tenant_id = $1 AND name = 'WriteAll'`, tenantTarget,
	).Scan(&writeAllRoleID), "look up the target tenant's WriteAll role id")

	// A second, unrelated tenant that also has one. This is the decoy the
	// unfiltered guard counted.
	var tenantOther string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`,
		"roles-e2e-other-"+time.Now().Format("20060102150405.000000"),
	).Scan(&tenantOther), "seed the decoy tenant")
	t.Cleanup(func() { clearPermissionsTenant(t, db, tenantOther) })
	otherAdmin := seedPermissionsUser(t, db, tenantOther, "other-admin")
	grantRole(t, db, tenantOther, otherAdmin, "WriteAll", []seededPermission{
		{methodPattern: "^(POST|PUT|PATCH|DELETE)$", pathPattern: "^/.*$"},
	})

	var tenantOps string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'OPERATIONS') RETURNING tenant_id`,
		"roles-e2e-ops-revoke-"+time.Now().Format("20060102150405.000000"),
	).Scan(&tenantOps), "seed the operations tenant")
	t.Cleanup(func() { clearPermissionsTenant(t, db, tenantOps) })
	adminOps := seedPermissionsUser(t, db, tenantOps, "admin-ops")
	ctxOps := security.WithContext(context.Background(), security.Context{
		TenantID:   tenantOps,
		TenantType: string(model.TenantTypeOperations),
		ErunUserID: adminOps,
	})

	err := roles.Revoke(ctxOps, soleAdmin, writeAllRoleID, tenantTarget)
	if !errors.Is(err, ErrLastGrantCapableRole) {
		t.Fatalf("expected ErrLastGrantCapableRole for the target tenant's last grant-capable role, got %v", err)
	}

	var stillAssigned int
	mustNoErr(t, db.QueryRow(
		`SELECT COUNT(*) FROM user_roles WHERE tenant_id = $1 AND user_id = $2 AND role_id = $3`,
		tenantTarget, soleAdmin, writeAllRoleID,
	).Scan(&stillAssigned), "count the assignment after the refused revoke")
	if stillAssigned != 1 {
		t.Fatalf("expected the refused revoke to roll back, found %d rows", stillAssigned)
	}
}

// TestRoleRepositoryRevokeFromOperationsUndoesACrossTenantGrant closes the loop
// on the grant: whatever an operations caller can do to another tenant's roles,
// it must be able to undo, or the recovery path creates its own dead end.
func TestRoleRepositoryRevokeFromOperationsUndoesACrossTenantGrant(t *testing.T) {
	db, tenantTarget := rolesDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	roles := &RoleRepository{txs: txs}
	adminTarget := seedPermissionsUser(t, db, tenantTarget, "admin-target")
	member := seedPermissionsUser(t, db, tenantTarget, "member")

	// The target tenant keeps a grant-capable user of its own, so the lockout
	// guard has no reason to refuse the revoke below.
	grantRole(t, db, tenantTarget, adminTarget, "WriteAll", []seededPermission{
		{methodPattern: "^(POST|PUT|PATCH|DELETE)$", pathPattern: "^/.*$"},
	})
	role, err := roles.Create(rolesContext(tenantTarget, adminTarget), "ReviewsReader", []RolePermissionInput{
		{APIMethod: "GET", APIPath: "/v1/reviews"},
	})
	mustNoErr(t, err, "create a role in the target tenant")

	var tenantOps string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'OPERATIONS') RETURNING tenant_id`,
		"roles-e2e-ops-roundtrip-"+time.Now().Format("20060102150405.000000"),
	).Scan(&tenantOps), "seed the operations tenant")
	t.Cleanup(func() { clearPermissionsTenant(t, db, tenantOps) })
	adminOps := seedPermissionsUser(t, db, tenantOps, "admin-ops")
	ctxOps := security.WithContext(context.Background(), security.Context{
		TenantID:   tenantOps,
		TenantType: string(model.TenantTypeOperations),
		ErunUserID: adminOps,
	})

	_, err = roles.Grant(ctxOps, member, role.RoleID, tenantTarget)
	mustNoErr(t, err, "operations grant into the target tenant")
	mustNoErr(t, roles.Revoke(ctxOps, member, role.RoleID, tenantTarget), "operations revoke in the target tenant")

	assigned, err := roles.ForUser(rolesContext(tenantTarget, adminTarget), member)
	mustNoErr(t, err, "list the member's roles as the target tenant")
	for _, r := range assigned {
		if r.RoleID == role.RoleID {
			t.Fatalf("expected the revoke to remove the grant, target tenant still sees %+v", assigned)
		}
	}
}

// TestRoleRepositoryGrantRejectsCrossTenantRole proves RLS, not application
// code, is what stops a role from another tenant being granted: the role_id
// is invisible to this tenant's transaction, so the insert's foreign key
// fails and Grant reports it as not found.
func TestRoleRepositoryGrantRejectsCrossTenantRole(t *testing.T) {
	db, tenantA := rolesDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	roles := &RoleRepository{txs: txs}
	userA := seedPermissionsUser(t, db, tenantA, "user-a")

	var tenantB string
	err := db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`,
		"roles-e2e-cross-"+time.Now().Format("20060102150405.000000"),
	).Scan(&tenantB)
	mustNoErr(t, err, "seed second tenant")
	t.Cleanup(func() { clearPermissionsTenant(t, db, tenantB) })
	adminB := seedPermissionsUser(t, db, tenantB, "admin-b")
	ctxB := rolesContext(tenantB, adminB)
	roleB, err := roles.Create(ctxB, "ReviewsReader", []RolePermissionInput{
		{APIMethod: "GET", APIPath: "/v1/reviews"},
	})
	mustNoErr(t, err, "create role in tenant B")

	ctxA := rolesContext(tenantA, userA)
	if _, err := roles.Grant(ctxA, userA, roleB.RoleID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound granting a cross-tenant role, got %v", err)
	}
}

// TestRoleRepositoryGrantFromOperationsFilesUnderTargetTenant is the recovery
// path an operations caller uses when a tenant's only grant-capable user cannot
// authenticate. erun_operations bypasses RLS, so without the explicit tenant
// the row's tenant_id default would file the grant under the operations tenant
// and the target tenant would still see nothing.
func TestRoleRepositoryGrantFromOperationsFilesUnderTargetTenant(t *testing.T) {
	db, tenantTarget := rolesDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	roles := &RoleRepository{txs: txs}
	adminTarget := seedPermissionsUser(t, db, tenantTarget, "admin-target")
	member := seedPermissionsUser(t, db, tenantTarget, "member")

	role, err := roles.Create(rolesContext(tenantTarget, adminTarget), "ReviewsReader", []RolePermissionInput{
		{APIMethod: "GET", APIPath: "/v1/reviews"},
	})
	mustNoErr(t, err, "create role in the target tenant")

	var tenantOps string
	err = db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'OPERATIONS') RETURNING tenant_id`,
		"roles-e2e-ops-"+time.Now().Format("20060102150405.000000"),
	).Scan(&tenantOps)
	mustNoErr(t, err, "seed operations tenant")
	t.Cleanup(func() { clearPermissionsTenant(t, db, tenantOps) })
	adminOps := seedPermissionsUser(t, db, tenantOps, "admin-ops")
	ctxOps := security.WithContext(context.Background(), security.Context{
		TenantID:   tenantOps,
		TenantType: string(model.TenantTypeOperations),
		ErunUserID: adminOps,
	})

	grant, err := roles.Grant(ctxOps, member, role.RoleID, tenantTarget)
	mustNoErr(t, err, "operations grant into the target tenant")
	if grant.TenantID != tenantTarget {
		t.Fatalf("grant filed under tenant %q, want the target tenant %q", grant.TenantID, tenantTarget)
	}

	// And the target tenant itself can now see it, which is the whole point:
	// a grant filed under operations would be invisible here.
	assigned, err := roles.ForUser(rolesContext(tenantTarget, adminTarget), member)
	mustNoErr(t, err, "list the member's roles as the target tenant")
	found := false
	for _, r := range assigned {
		if r.RoleID == role.RoleID {
			found = true
		}
	}
	if !found {
		t.Fatalf("target tenant sees roles %+v, want the granted role %q", assigned, role.RoleID)
	}
}

func TestRoleRepositoryCreateRejectsDuplicateName(t *testing.T) {
	db, tenantID := rolesDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	roles := &RoleRepository{txs: txs}
	admin := seedPermissionsUser(t, db, tenantID, "admin")
	ctx := rolesContext(tenantID, admin)

	_, err := roles.Create(ctx, "ReviewsReader", []RolePermissionInput{{APIMethod: "GET", APIPath: "/v1/reviews"}})
	mustNoErr(t, err, "create role")
	_, err = roles.Create(ctx, "ReviewsReader", []RolePermissionInput{{APIMethod: "GET", APIPath: "/v1/audit-events"}})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict for a duplicate role name, got %v", err)
	}
}

func TestRoleRepositoryGrantRejectsDuplicateAssignment(t *testing.T) {
	db, tenantID := rolesDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	roles := &RoleRepository{txs: txs}
	admin := seedPermissionsUser(t, db, tenantID, "admin")
	reviewer := seedPermissionsUser(t, db, tenantID, "reviewer")
	ctx := rolesContext(tenantID, admin)

	role, err := roles.Create(ctx, "ReviewsReader", []RolePermissionInput{{APIMethod: "GET", APIPath: "/v1/reviews"}})
	mustNoErr(t, err, "create role")
	if _, err := roles.Grant(ctx, reviewer, role.RoleID, ""); err != nil {
		t.Fatalf("first grant: %v", err)
	}
	if _, err := roles.Grant(ctx, reviewer, role.RoleID, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict granting the same role twice, got %v", err)
	}
}
