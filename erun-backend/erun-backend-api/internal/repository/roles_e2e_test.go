package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/testpg"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/routeroles"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func rolesDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	databaseURL := testpg.URL(t)
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

// erun_operations bypasses RLS, so Revoke must filter by the target tenant.
func TestRoleRepositoryRevokeGuardCountsOnlyTheTargetTenant(t *testing.T) {
	db, tenantTarget := rolesDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	roles := &RoleRepository{txs: txs}

	soleAdmin := seedPermissionsUser(t, db, tenantTarget, "sole-admin")
	grantRole(t, db, tenantTarget, soleAdmin, "WriteAll", []seededPermission{
		{methodPattern: "^(POST|PUT|PATCH|DELETE)$", pathPattern: "^/.*$"},
	})
	var writeAllRoleID string
	mustNoErr(t, db.QueryRow(
		`SELECT role_id FROM roles WHERE tenant_id = $1 AND name = 'WriteAll'`, tenantTarget,
	).Scan(&writeAllRoleID), "look up the target tenant's WriteAll role id")

	// A decoy tenant with its own grant-capable user.
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

func TestRoleRepositoryRevokeFromOperationsUndoesACrossTenantGrant(t *testing.T) {
	db, tenantTarget := rolesDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	roles := &RoleRepository{txs: txs}
	adminTarget := seedPermissionsUser(t, db, tenantTarget, "admin-target")
	member := seedPermissionsUser(t, db, tenantTarget, "member")

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

// The stale grant is inserted by hand; the production set is a compile-time map.
func TestRoleReconciliationRemovesAGrantNoLongerInTheDerivedSet(t *testing.T) {
	db, tenantID := rolesDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	roles := &RoleRepository{txs: txs}
	admin := seedPermissionsUser(t, db, tenantID, "admin")
	ctx := rolesContext(tenantID, admin)

	// The first read creates TenantUser and seeds its grants.
	if _, err := roles.List(ctx); err != nil {
		t.Fatalf("seed narrower roles: %v", err)
	}

	var userRoleID string
	mustNoErr(t, db.QueryRow(
		`SELECT role_id FROM roles WHERE tenant_id = $1 AND name = $2`, tenantID, tenantUserRoleName,
	).Scan(&userRoleID), "find TenantUser role")

	// DELETE /v1/tenants is never in TenantUser's derived set.
	mustNoErr(t, func() error {
		_, err := db.Exec(
			`INSERT INTO role_permissions (tenant_id, role_id, api_method, api_path) VALUES ($1, $2, 'DELETE', '/v1/tenants')`,
			tenantID, userRoleID,
		)
		return err
	}(), "seed a grant outside the derived set")

	if _, err := roles.List(ctx); err != nil {
		t.Fatalf("reconcile narrower roles: %v", err)
	}

	var stale int
	mustNoErr(t, db.QueryRow(
		`SELECT count(*) FROM role_permissions WHERE tenant_id = $1 AND role_id = $2 AND api_method = 'DELETE' AND api_path = '/v1/tenants'`,
		tenantID, userRoleID,
	).Scan(&stale), "count the stale grant")
	if stale != 0 {
		t.Fatalf("a grant outside TenantUser's derived set survived reconciliation: %d row(s) left", stale)
	}

	// Reconciliation must narrow, not empty.
	var granted int
	mustNoErr(t, db.QueryRow(
		`SELECT count(*) FROM role_permissions WHERE tenant_id = $1 AND role_id = $2`, tenantID, userRoleID,
	).Scan(&granted), "count the surviving grants")
	if want := len(routeroles.TenantUserPermissions()); granted != want {
		t.Fatalf("TenantUser holds %d grants after reconciliation, want the derived set's %d", granted, want)
	}
}

// A pattern row's NULL method/path defeats a bare NOT IN comparison.
func TestRoleReconciliationRemovesAPatternGrantTheDerivedSetCannotName(t *testing.T) {
	db, tenantID := rolesDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	roles := &RoleRepository{txs: txs}
	admin := seedPermissionsUser(t, db, tenantID, "admin")
	ctx := rolesContext(tenantID, admin)

	if _, err := roles.List(ctx); err != nil {
		t.Fatalf("seed narrower roles: %v", err)
	}

	var userRoleID string
	mustNoErr(t, db.QueryRow(
		`SELECT role_id FROM roles WHERE tenant_id = $1 AND name = $2`, tenantID, tenantUserRoleName,
	).Scan(&userRoleID), "find TenantUser role")

	mustNoErr(t, func() error {
		_, err := db.Exec(
			`INSERT INTO role_permissions (tenant_id, role_id, api_method_pattern, api_path_pattern) VALUES ($1, $2, '.*', '^/v1/.*$')`,
			tenantID, userRoleID,
		)
		return err
	}(), "seed a pattern grant outside the derived set")

	var seeded int
	mustNoErr(t, db.QueryRow(
		`SELECT count(*) FROM role_permissions WHERE tenant_id = $1 AND role_id = $2 AND api_method IS NULL`,
		tenantID, userRoleID,
	).Scan(&seeded), "count the seeded pattern grant")
	if seeded != 1 {
		t.Fatalf("expected the seeded pattern grant to be present before reconciliation, found %d", seeded)
	}

	if _, err := roles.List(ctx); err != nil {
		t.Fatalf("reconcile narrower roles: %v", err)
	}

	var stale int
	mustNoErr(t, db.QueryRow(
		`SELECT count(*) FROM role_permissions WHERE tenant_id = $1 AND role_id = $2 AND api_method IS NULL`,
		tenantID, userRoleID,
	).Scan(&stale), "count the surviving pattern grants")
	if stale != 0 {
		t.Fatalf("a pattern grant outside TenantUser's derived set survived reconciliation: %d row(s) left", stale)
	}

	var granted int
	mustNoErr(t, db.QueryRow(
		`SELECT count(*) FROM role_permissions WHERE tenant_id = $1 AND role_id = $2`, tenantID, userRoleID,
	).Scan(&granted), "count the surviving grants")
	if want := len(routeroles.TenantUserPermissions()); granted != want {
		t.Fatalf("TenantUser holds %d grants after reconciliation, want the derived set's %d", granted, want)
	}
}

func TestSeededTenantAgentGrantsAreTheDerivedSubsetOfTenantUser(t *testing.T) {
	db, tenantID := rolesDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	roles := &RoleRepository{txs: txs}
	admin := seedPermissionsUser(t, db, tenantID, "admin")
	ctx := rolesContext(tenantID, admin)

	if _, err := roles.List(ctx); err != nil {
		t.Fatalf("seed narrower roles: %v", err)
	}

	var agentRoleID, userRoleID string
	mustNoErr(t, db.QueryRow(
		`SELECT role_id FROM roles WHERE tenant_id = $1 AND name = $2`, tenantID, TenantAgentRoleName,
	).Scan(&agentRoleID), "find TenantAgent role")
	mustNoErr(t, db.QueryRow(
		`SELECT role_id FROM roles WHERE tenant_id = $1 AND name = $2`, tenantID, tenantUserRoleName,
	).Scan(&userRoleID), "find TenantUser role")

	agentGrants := rolePermissionPairs(t, db, tenantID, agentRoleID)
	userGrants := rolePermissionPairs(t, db, tenantID, userRoleID)

	for _, permission := range routeroles.TenantAgentPermissions() {
		key := permission.Method + " " + permission.Path
		if !agentGrants[key] {
			t.Errorf("TenantAgent is missing %s, which the derived set grants it", key)
		}
		if !userGrants[key] {
			t.Errorf("%s is granted to TenantAgent but not to the seeded TenantUser -- the machine role must stay inside TenantUser's reach", key)
		}
	}
	if len(agentGrants) != len(routeroles.TenantAgentPermissions()) {
		t.Errorf("TenantAgent holds %d grants, want the derived set's %d", len(agentGrants), len(routeroles.TenantAgentPermissions()))
	}
	// The containment must be strict.
	if agentGrants["GET /v1/builds"] {
		t.Error("TenantAgent holds GET /v1/builds -- the machine role is meant to be strictly narrower than TenantUser")
	}
	if !userGrants["GET /v1/builds"] {
		t.Error("TenantUser is missing GET /v1/builds -- the containment check above proved nothing")
	}
}

func rolePermissionPairs(t *testing.T, db *sql.DB, tenantID, roleID string) map[string]bool {
	t.Helper()
	rows, err := db.Query(
		`SELECT api_method, api_path FROM role_permissions WHERE tenant_id = $1 AND role_id = $2 AND api_method_pattern IS NULL`,
		tenantID, roleID,
	)
	mustNoErr(t, err, "read role permissions")
	defer func() { _ = rows.Close() }()
	grants := map[string]bool{}
	for rows.Next() {
		var method, path string
		mustNoErr(t, rows.Scan(&method, &path), "scan role permission")
		grants[method+" "+path] = true
	}
	mustNoErr(t, rows.Err(), "iterate role permissions")
	return grants
}
