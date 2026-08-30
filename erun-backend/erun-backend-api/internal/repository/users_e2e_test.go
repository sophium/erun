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

	created, _, err := users.Create(ctx, CreateUserParams{Username: "first-admin"})
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

	created, _, err := users.Create(rolesContext(tenantID, first), CreateUserParams{Username: "second-user"})
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

	created, _, err := users.Create(adminCtx, CreateUserParams{Username: "narrow-user", RoleIDs: []string{role.RoleID}})
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

	_, _, err := users.Create(rolesContext(tenantID, first), CreateUserParams{
		Username: "someone",
		RoleIDs:  []string{"00000000-0000-0000-0000-000000000000"},
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for an unknown role id, got %v", err)
	}
}

// seedTenantIssuer registers issuer for tenantID so a user_external_ids row
// can foreign-key it — the same registration bootstrap already performs, done
// by hand here since these tests seed enrollment directly.
func seedTenantIssuer(t *testing.T, db *sql.DB, tenantID, issuer string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO issuers (issuer) VALUES ($1) ON CONFLICT (issuer) DO NOTHING`, issuer)
	mustNoErr(t, err, "seed issuers")
	_, err = db.Exec(`INSERT INTO tenant_issuers (tenant_id, issuer, name) VALUES ($1, $2, $3)`, tenantID, issuer, issuer)
	mustNoErr(t, err, "seed tenant_issuers")
}

// TestUserRepositoryCreateReportsUsernameConflictDistinctly proves a plain
// username collision (no external identity involved) surfaces as
// ErrUsernameConflict, not the bare ErrConflict a caller could not tell apart
// from any other uniqueness violation.
func TestUserRepositoryCreateReportsUsernameConflictDistinctly(t *testing.T) {
	db, tenantID := usersDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	users := &UserRepository{txs: txs}
	first := seedPermissionsUser(t, db, tenantID, "first-admin")

	_, _, err := users.Create(rolesContext(tenantID, first), CreateUserParams{Username: "first-admin"})
	if !errors.Is(err, ErrUsernameConflict) {
		t.Fatalf("expected ErrUsernameConflict for a colliding username, got %v", err)
	}
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrUsernameConflict to still satisfy errors.Is(err, ErrConflict), got %v", err)
	}
}

// TestUserRepositoryCreateTreatsReEnrollmentAsNoOp is the regression this fix
// addresses: re-enrolling an identity already linked in the tenant, even
// under a *different* requested username, reports the existing user back
// (alreadyEnrolled=true) instead of the wrong-cause username-collision 409
// that sent an operator hunting for a username that did not exist.
func TestUserRepositoryCreateTreatsReEnrollmentAsNoOp(t *testing.T) {
	db, tenantID := usersDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	users := &UserRepository{txs: txs}
	first := seedPermissionsUser(t, db, tenantID, "first-admin")
	issuer := "https://issuer.example"
	seedTenantIssuer(t, db, tenantID, issuer)

	created, alreadyEnrolled, err := users.Create(rolesContext(tenantID, first), CreateUserParams{
		Username: "rihards-frs-lv",
		Issuer:   issuer,
		Subject:  "subject-1",
	})
	mustNoErr(t, err, "enroll identity first time")
	if alreadyEnrolled {
		t.Fatalf("expected the first enrollment to not be reported as already enrolled")
	}

	reEnrolled, alreadyEnrolled, err := users.Create(rolesContext(tenantID, first), CreateUserParams{
		Username: "rihards", // a different username than the one already on file
		Issuer:   issuer,
		Subject:  "subject-1",
	})
	mustNoErr(t, err, "re-enroll the same identity")
	if !alreadyEnrolled {
		t.Fatalf("expected re-enrolling an already-linked identity to be reported as already enrolled")
	}
	if reEnrolled.UserID != created.UserID || reEnrolled.Username != created.Username {
		t.Fatalf("re-enrollment = %+v, want the original user %+v untouched (no new row, no rename)", reEnrolled, created)
	}

	var count int
	mustNoErr(t, db.QueryRow(`SELECT COUNT(*) FROM users WHERE tenant_id = $1 AND username = $2`, tenantID, "rihards").Scan(&count), "count rows for the requested username")
	if count != 0 {
		t.Fatalf("expected no user row for the requested (unused) username, got %d", count)
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
