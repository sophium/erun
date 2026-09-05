package repository

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"

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

// seedTenant registers a second tenant beyond usersDatabase's own, so a test
// can act as one tenant while targeting another.
func seedTenant(t *testing.T, db *sql.DB, tenantType, namePrefix string) string {
	t.Helper()
	var tenantID string
	err := db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, $2) RETURNING tenant_id`,
		namePrefix+"-"+time.Now().Format("20060102150405.000000"), tenantType,
	).Scan(&tenantID)
	mustNoErr(t, err, "seed "+tenantType+" tenant")
	t.Cleanup(func() { clearPermissionsTenant(t, db, tenantID) })
	return tenantID
}

// TestUserRepositoryOperationsCallerEnrollsIntoTargetTenant is the
// authorization boundary POST /v1/users' resolveTargetTenant exists for,
// proven end to end rather than against a stub: an operations-scoped
// caller's transaction runs as erun_operations (TxManager's
// setPostgresSecurityContext), which schema/rls/users.sql's
// users_operations_access policy lets write any tenant_id, so the enrolled
// user must actually land in the requested target tenant, not the caller's
// own operations tenant.
func TestUserRepositoryOperationsCallerEnrollsIntoTargetTenant(t *testing.T) {
	db, targetTenantID := usersDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	users := &UserRepository{txs: txs}
	opsTenantID := seedTenant(t, db, "OPERATIONS", "users-e2e-ops")
	opsCtx := security.WithContext(context.Background(), security.Context{
		TenantID: opsTenantID, TenantType: "OPERATIONS", ErunUserID: "ops-caller",
	})

	created, _, err := users.Create(opsCtx, CreateUserParams{Username: "cross-tenant-user", TenantID: targetTenantID})
	mustNoErr(t, err, "operations caller enrolls into the target tenant")
	if created.TenantID != targetTenantID {
		t.Fatalf("created.TenantID = %q, want the target tenant %q, not the operations caller's own", created.TenantID, targetTenantID)
	}

	var count int
	mustNoErr(t, db.QueryRow(`SELECT COUNT(*) FROM users WHERE tenant_id = $1 AND username = $2`, targetTenantID, "cross-tenant-user").Scan(&count), "count rows in the target tenant")
	if count != 1 {
		t.Fatalf("expected exactly one row landed in the target tenant, got %d", count)
	}
	mustNoErr(t, db.QueryRow(`SELECT COUNT(*) FROM users WHERE tenant_id = $1`, opsTenantID).Scan(&count), "count rows in the operations caller's own tenant")
	if count != 0 {
		t.Fatalf("expected no row landed in the operations caller's own tenant, got %d", count)
	}
}

// TestUserRepositoryNonOperationsCallerCannotOverrideTenant proves the
// authorization boundary holds even below the route's resolveTargetTenant
// gate: a non-operations caller's transaction runs as erun_tenant, whose
// users_tenant_isolation policy (schema/rls/users.sql) has WITH CHECK
// (tenant_id = erun_current_tenant_id()), so an insert explicitly naming a
// different tenant_id is refused by PostgreSQL itself — not merely by the
// route's own check — if this method is ever reached with an override from
// a non-operations caller.
func TestUserRepositoryNonOperationsCallerCannotOverrideTenant(t *testing.T) {
	db, callerTenantID := usersDatabase(t)
	txs := NewTxManager(db, DialectPostgres)
	users := &UserRepository{txs: txs}
	targetTenantID := seedTenant(t, db, "COMPANY", "users-e2e-target")
	callerCtx := security.WithContext(context.Background(), security.Context{
		TenantID: callerTenantID, TenantType: "COMPANY", ErunUserID: "caller",
	})

	_, _, err := users.Create(callerCtx, CreateUserParams{Username: "should-not-exist", TenantID: targetTenantID})
	if err == nil {
		t.Fatalf("expected a non-operations caller's cross-tenant override to be refused, got success")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.InsufficientPrivilege {
		t.Fatalf("expected a row-level-security insufficient-privilege error, got %v", err)
	}

	var count int
	mustNoErr(t, db.QueryRow(`SELECT COUNT(*) FROM users WHERE username = $1`, "should-not-exist").Scan(&count), "count rows for the refused username")
	if count != 0 {
		t.Fatalf("expected no row was written anywhere, got %d", count)
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

// TestUserRepositoryCreateRefusesEnrollmentUnderADeadIssuerMapping is the
// enrollment half of the reachability refusal: linking an identity to a
// tenant whose mapping no token can resolve through produces a user who can
// never sign in — a success today, discovered only as a failed sign-in later.
// The refusal must abort the whole enrollment, leaving no orphan users row.
func TestUserRepositoryCreateRefusesEnrollmentUnderADeadIssuerMapping(t *testing.T) {
	db, tenantID := usersDatabase(t)
	const issuer = "https://auth.enroll-refusal-e2e.example"
	// An org-scoped issuer whose mapping for this tenant carries no org value:
	// resolution matches the org by equality, so nothing ever matches here.
	_, err := db.Exec(
		`INSERT INTO issuers (issuer, org_field_key) VALUES ($1, $2) ON CONFLICT (issuer) DO NOTHING`,
		issuer, "urn:zitadel:iam:user:resourceowner:id",
	)
	mustNoErr(t, err, "seed org-scoped issuer")
	_, err = db.Exec(`INSERT INTO tenant_issuers (tenant_id, issuer, name) VALUES ($1, $2, $3)`, tenantID, issuer, issuer)
	mustNoErr(t, err, "seed tenant_issuers with no org value")

	users := &UserRepository{txs: NewTxManager(db, DialectPostgres)}
	_, _, err = users.Create(rolesContext(tenantID, ""), CreateUserParams{
		Username: "unreachable-enrollee",
		Issuer:   issuer,
		Subject:  "enroll-refusal-e2e-subject",
	})
	var unresolvable *UnresolvableIssuerMappingError
	if !errors.As(err, &unresolvable) {
		t.Fatalf("enrolling under a dead issuer mapping: err = %v, want *UnresolvableIssuerMappingError", err)
	}

	var count int
	mustNoErr(t, db.QueryRow(
		`SELECT count(*) FROM users WHERE tenant_id = $1 AND username = $2`, tenantID, "unreachable-enrollee",
	).Scan(&count), "count refused enrollment")
	if count != 0 {
		t.Fatalf("refused enrollment left %d users rows behind", count)
	}
}

// TestUserRepositoryCreateAcceptsEnrollmentUnderAResolvableMapping is the
// positive control: the refusal above must not have made ordinary enrollment
// conditional on anything an operator has to supply.
func TestUserRepositoryCreateAcceptsEnrollmentUnderAResolvableMapping(t *testing.T) {
	db, tenantID := usersDatabase(t)
	const issuer = "https://idp.enroll-accept-e2e.example"
	seedTenantIssuer(t, db, tenantID, issuer)

	users := &UserRepository{txs: NewTxManager(db, DialectPostgres)}
	created, alreadyEnrolled, err := users.Create(rolesContext(tenantID, ""), CreateUserParams{
		Username: "reachable-enrollee",
		Issuer:   issuer,
		Subject:  "enroll-accept-e2e-subject",
	})
	mustNoErr(t, err, "enroll under a single-tenant issuer mapping")
	if alreadyEnrolled || created.UserID == "" {
		t.Fatalf("expected a fresh enrollment, got alreadyEnrolled=%v user=%+v", alreadyEnrolled, created)
	}
}
