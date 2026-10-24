package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/testpg"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func usersDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	databaseURL := testpg.URL(t)
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

func seedTenantIssuer(t *testing.T, db *sql.DB, tenantID, issuer string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO issuers (issuer) VALUES ($1) ON CONFLICT (issuer) DO NOTHING`, issuer)
	mustNoErr(t, err, "seed issuers")
	_, err = db.Exec(`INSERT INTO tenant_issuers (tenant_id, issuer, name) VALUES ($1, $2, $3)`, tenantID, issuer, issuer)
	mustNoErr(t, err, "seed tenant_issuers")
}

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

// erun_tenant's WITH CHECK refuses the override even below the route's gate.
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

func TestUserRepositoryCreateRefusesEnrollmentUnderADeadIssuerMapping(t *testing.T) {
	db, tenantID := usersDatabase(t)
	const issuer = "https://auth.enroll-refusal-e2e.example"
	// Org-scoped issuer with no org value on this tenant's mapping: never matches.
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
