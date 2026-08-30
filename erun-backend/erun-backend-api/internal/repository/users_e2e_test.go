package repository

import (
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// UserRepository.Create's role-assignment default is exercised against a real
// migrated PostgreSQL because it depends on the same RLS-scoped roles/
// role_permissions/user_roles tables PermissionAuthorizer enforces against —
// a fake that agrees with itself would not catch the two paths disagreeing.
func usersDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_USERS_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_USERS_DATABASE_URL to a migrated PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	var tenantID string
	err = db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`,
		"users-e2e-"+time.Now().Format("20060102150405.000000"),
	).Scan(&tenantID)
	mustNoErr(t, err, "seed tenant")
	t.Cleanup(func() { clearPermissionsTenant(t, db, tenantID) })
	return db, tenantID
}

// TestUserRepositoryFirstUserGetsPredefinedRoles proves the one case that must
// keep full control: a tenant's first enrolled user gets TenantAdmin, since
// granting a role is itself permission-gated and a non-grant-capable first
// user could never be granted anything. TenantAdmin (not ReadAll/WriteAll)
// is deliberate: this is a COMPANY tenant, and the platform-operator reach
// only ever belongs to the OPERATIONS tenant's own first user.
func TestUserRepositoryFirstUserGetsPredefinedRoles(t *testing.T) {
	db, tenantID := usersDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	users := &UserRepository{txs: txs}
	authorizer := &PermissionAuthorizer{txs: txs}
	ctx := rolesContext(tenantID, "")

	created, err := users.Create(ctx, CreateUserParams{Username: "first-admin"})
	mustNoErr(t, err, "create first user")

	names, err := users.RoleNames(rolesContext(tenantID, created.UserID), created.UserID)
	mustNoErr(t, err, "list role names")
	if !containsAll(names, "TenantAdmin") {
		t.Fatalf("expected the first user to hold TenantAdmin, got %v", names)
	}

	userCtx := rolesContext(tenantID, created.UserID)
	for _, permitted := range []struct{ method, path string }{
		{"GET", "/v1/reviews"},
		{"POST", "/v1/reviews"},
		{"POST", "/v1/users/{user_id}/roles"},
	} {
		if err := authorizer.Authorize(userCtx, permitted.method, permitted.path); err != nil {
			t.Fatalf("%s %s: expected the first user to be permitted, got %v", permitted.method, permitted.path, err)
		}
	}
}

// TestUserRepositoryLaterUserGetsTenantUserByDefault is the regression this
// change fixes twice over: every enrolled user used to get ReadAll+WriteAll
// unconditionally, and then briefly nothing at all. A second (non-first)
// user enrolled with no explicit roles must hold exactly TenantUser: able to
// read and drive reviews, refused an administrative write such as creating
// an environment.
func TestUserRepositoryLaterUserGetsTenantUserByDefault(t *testing.T) {
	db, tenantID := usersDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	users := &UserRepository{txs: txs}
	authorizer := &PermissionAuthorizer{txs: txs}
	first := seedPermissionsUser(t, db, tenantID, "first-admin")
	grantRole(t, db, tenantID, first, "WriteAll", []seededPermission{
		{methodPattern: "^(POST|PUT|PATCH|DELETE)$", pathPattern: "^/.*$"},
	})

	created, err := users.Create(rolesContext(tenantID, first), CreateUserParams{Username: "second-user"})
	mustNoErr(t, err, "create second user")

	names, err := users.RoleNames(rolesContext(tenantID, first), created.UserID)
	mustNoErr(t, err, "list role names")
	if len(names) != 1 || names[0] != "TenantUser" {
		t.Fatalf("expected the second enrolled user to hold exactly TenantUser, got %v", names)
	}

	newUserCtx := rolesContext(tenantID, created.UserID)
	for _, permitted := range []struct{ method, path string }{
		{"GET", "/v1/reviews"},
		{"POST", "/v1/reviews"},
	} {
		if err := authorizer.Authorize(newUserCtx, permitted.method, permitted.path); err != nil {
			t.Fatalf("%s %s: expected TenantUser to be permitted, got %v", permitted.method, permitted.path, err)
		}
	}
	if err := authorizer.Authorize(newUserCtx, "POST", "/v1/environments"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("POST /v1/environments: expected TenantUser to be refused, got %v", err)
	}
}

// TestUserRepositoryExplicitRoleIDsAreGranted proves an enroller can hand a
// new (non-first) user exactly the access named in the request, instead of
// either the old all-or-nothing default or the new all-nothing default.
func TestUserRepositoryExplicitRoleIDsAreGranted(t *testing.T) {
	db, tenantID := usersDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	users := &UserRepository{txs: txs}
	roles := &RoleRepository{txs: txs}
	authorizer := &PermissionAuthorizer{txs: txs}
	first := seedPermissionsUser(t, db, tenantID, "first-admin")
	grantRole(t, db, tenantID, first, "WriteAll", []seededPermission{
		{methodPattern: "^(POST|PUT|PATCH|DELETE)$", pathPattern: "^/.*$"},
	})
	adminCtx := rolesContext(tenantID, first)

	role, err := roles.Create(adminCtx, "ReviewsReader", []RolePermissionInput{
		{APIMethod: "GET", APIPath: "/v1/reviews"},
	})
	mustNoErr(t, err, "create narrow role")

	created, err := users.Create(adminCtx, CreateUserParams{Username: "narrow-user", RoleIDs: []string{role.RoleID}})
	mustNoErr(t, err, "create user with explicit roles")

	names, err := users.RoleNames(adminCtx, created.UserID)
	mustNoErr(t, err, "list role names")
	if len(names) != 1 || names[0] != "ReviewsReader" {
		t.Fatalf("expected the user to hold exactly the requested role, got %v", names)
	}

	newUserCtx := rolesContext(tenantID, created.UserID)
	if err := authorizer.Authorize(newUserCtx, "GET", "/v1/reviews"); err != nil {
		t.Fatalf("expected the granted path to be permitted, got %v", err)
	}
	if err := authorizer.Authorize(newUserCtx, "POST", "/v1/reviews"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected an ungranted path to be refused, got %v", err)
	}
}

// TestUserRepositoryCreateRejectsUnknownRoleID proves a role id naming
// nothing in this tenant (typo, or another tenant's role) is refused rather
// than silently enrolling with no roles at all.
func TestUserRepositoryCreateRejectsUnknownRoleID(t *testing.T) {
	db, tenantID := usersDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	users := &UserRepository{txs: txs}
	first := seedPermissionsUser(t, db, tenantID, "first-admin")
	grantRole(t, db, tenantID, first, "WriteAll", []seededPermission{
		{methodPattern: "^(POST|PUT|PATCH|DELETE)$", pathPattern: "^/.*$"},
	})

	_, err := users.Create(rolesContext(tenantID, first), CreateUserParams{
		Username: "someone",
		RoleIDs:  []string{"00000000-0000-0000-0000-000000000000"},
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for an unknown role id, got %v", err)
	}
}

func containsAll(haystack []string, wanted ...string) bool {
	set := make(map[string]bool, len(haystack))
	for _, v := range haystack {
		set[v] = true
	}
	for _, w := range wanted {
		if !set[w] {
			return false
		}
	}
	return true
}
