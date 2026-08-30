package repository

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Empty-database bootstrap runs SQL with real transaction-local RLS role
// switches (SET LOCAL ROLE), so it is exercised against a real migrated
// PostgreSQL rather than a fake that agrees with itself.
func identityBootstrapDatabase(t *testing.T) *sql.DB {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_IDENTITY_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_IDENTITY_DATABASE_URL to a migrated, empty-of-tenants PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func mustNoErr(t *testing.T, err error, msg string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
}

func TestBootstrapFirstIdentityEnrolsPlatformTenantWhenERUNTenantSet(t *testing.T) {
	db := identityBootstrapDatabase(t)
	repo := NewIdentityRepository(db, DialectPostgres, "frs")
	t.Cleanup(func() { clearIdentityBootstrap(t, db) })

	tenant, user, err := repo.ResolveIdentity(context.Background(), security.Claims{
		Issuer:  "https://issuer.example/frs",
		Subject: "operator-subject",
	})
	mustNoErr(t, err, "bootstrap with ERUN_TENANT set")
	if tenant.Name != "frs" {
		t.Fatalf("expected tenant named %q, got %q", "frs", tenant.Name)
	}
	if tenant.Type != model.TenantTypeOperations {
		t.Fatalf("expected OPERATIONS tenant, got %q", tenant.Type)
	}
	if user.UserID == "" {
		t.Fatal("expected a bootstrapped user")
	}
	assertHasReadAllAndWriteAll(t, db, user.UserID)
}

// TestFirstTenantUserBootstrapGrantsBothPredefinedRoles proves the
// per-tenant-first-user path (a token resolving to an already-registered
// tenant with zero users) grants the same ReadAll+WriteAll shape as
// empty-database bootstrap, so the role-assignment API added alongside it
// changes nothing about how a new tenant gets its first admin.
func TestFirstTenantUserBootstrapGrantsBothPredefinedRoles(t *testing.T) {
	db := identityBootstrapDatabase(t)
	t.Cleanup(func() { clearIdentityBootstrap(t, db) })
	repo := NewIdentityRepository(db, DialectPostgres, "frs")

	// Bootstrap the platform's own OPERATIONS tenant first, then register a
	// second, ordinary tenant with zero users of its own.
	_, _, err := repo.ResolveIdentity(context.Background(), security.Claims{
		Issuer:  "https://issuer.example/frs",
		Subject: "operator-subject",
	})
	mustNoErr(t, err, "bootstrap platform tenant")

	var secondTenantID string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`,
		"second-tenant",
	).Scan(&secondTenantID), "seed second tenant")
	_, err = db.Exec(`INSERT INTO issuers (issuer) VALUES ($1)`, "https://issuer.example/second-tenant")
	mustNoErr(t, err, "register second tenant issuer")
	_, err = db.Exec(
		`INSERT INTO tenant_issuers (tenant_id, issuer, name) VALUES ($1, $2, $3)`,
		secondTenantID, "https://issuer.example/second-tenant", "second tenant issuer",
	)
	mustNoErr(t, err, "map second tenant issuer")

	tenant, user, err := repo.ResolveIdentity(context.Background(), security.Claims{
		Issuer:  "https://issuer.example/second-tenant",
		Subject: "second-tenant-first-user",
	})
	mustNoErr(t, err, "bootstrap second tenant's first user")
	if tenant.TenantID != secondTenantID {
		t.Fatalf("expected the second tenant, got %q", tenant.TenantID)
	}
	assertHasReadAllAndWriteAll(t, db, user.UserID)
}

// assertHasReadAllAndWriteAll locks in that bootstrap keeps granting exactly
// the predefined ReadAll+WriteAll shape, so the fine-grained role-assignment
// API added alongside it stays additive rather than changing the default.
func assertHasReadAllAndWriteAll(t *testing.T, db *sql.DB, userID string) {
	t.Helper()
	var names []string
	rows, err := db.Query(`
		SELECT ro.name
		  FROM user_roles ur
		  JOIN roles ro ON ro.tenant_id = ur.tenant_id AND ro.role_id = ur.role_id
		 WHERE ur.user_id = $1
		 ORDER BY ro.name
	`, userID)
	mustNoErr(t, err, "query bootstrapped user's roles")
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name string
		mustNoErr(t, rows.Scan(&name), "scan role name")
		names = append(names, name)
	}
	mustNoErr(t, rows.Err(), "iterate role names")
	if len(names) != 2 || names[0] != "ReadAll" || names[1] != "WriteAll" {
		t.Fatalf("expected exactly [ReadAll WriteAll], got %v", names)
	}
}

// TestBootstrapFirstIdentityRegistersSharedIssuerOrgScopedWithCallersOrgAsFirstMapping
// proves the fix for erun#1605's first defect: bootstrapping against a token
// that carries the shipped Zitadel org claim must register the issuer
// org-scoped (issuers.org_field_key set), with the bootstrap caller's own org
// as the first tenant_issuers mapping's org_field_value — not single-tenant,
// which would permanently block every later tenant on that issuer.
func TestBootstrapFirstIdentityRegistersSharedIssuerOrgScopedWithCallersOrgAsFirstMapping(t *testing.T) {
	db := identityBootstrapDatabase(t)
	repo := NewIdentityRepository(db, DialectPostgres, "frs")
	t.Cleanup(func() { clearIdentityBootstrap(t, db) })

	const issuer = "https://auth.erunpaas.example/shared"
	const orgClaimKey = "urn:zitadel:iam:user:resourceowner:id"
	const callerOrg = "386994597030592700"

	tenant, _, err := repo.ResolveIdentity(context.Background(), security.Claims{
		Issuer:  issuer,
		Subject: "operator-subject",
		Raw:     map[string]any{orgClaimKey: callerOrg},
	})
	mustNoErr(t, err, "bootstrap with a shared-issuer org claim")

	var orgFieldKey sql.NullString
	mustNoErr(t, db.QueryRow(`SELECT org_field_key FROM issuers WHERE issuer = $1`, issuer).Scan(&orgFieldKey), "read issuers row")
	if !orgFieldKey.Valid || orgFieldKey.String != orgClaimKey {
		t.Fatalf("issuers.org_field_key = %+v, want %q (org-scoped, not single-tenant)", orgFieldKey, orgClaimKey)
	}

	var orgFieldValue sql.NullString
	mustNoErr(t, db.QueryRow(
		`SELECT org_field_value FROM tenant_issuers WHERE tenant_id = $1 AND issuer = $2`,
		tenant.TenantID, issuer,
	).Scan(&orgFieldValue), "read tenant_issuers row")
	if !orgFieldValue.Valid || orgFieldValue.String != callerOrg {
		t.Fatalf("tenant_issuers.org_field_value = %+v, want the bootstrap caller's own org %q as the first mapping", orgFieldValue, callerOrg)
	}
}

// TestSecondTenantOnSharedIssuerWithDifferentOrgIsAcceptedNotConflict proves
// the fix for erun#1605's compounding case: once bootstrap has registered a
// shared issuer org-scoped (previous test), a second tenant created on that
// same issuer with a different org value must succeed — the 409 the issue
// reported no longer fires for this legitimate case.
func TestSecondTenantOnSharedIssuerWithDifferentOrgIsAcceptedNotConflict(t *testing.T) {
	db := identityBootstrapDatabase(t)
	repo := NewIdentityRepository(db, DialectPostgres, "frs")
	t.Cleanup(func() { clearIdentityBootstrap(t, db) })

	const issuer = "https://auth.erunpaas.example/shared-second"
	const orgClaimKey = "urn:zitadel:iam:user:resourceowner:id"

	_, _, err := repo.ResolveIdentity(context.Background(), security.Claims{
		Issuer:  issuer,
		Subject: "first-operator-subject",
		Raw:     map[string]any{orgClaimKey: "111111111111111111"},
	})
	mustNoErr(t, err, "bootstrap the first (platform) tenant on the shared issuer")

	tenants := NewTenantRepository(NewTxManager(db, DialectPostgres))
	ctx := security.WithContext(context.Background(), security.Context{TenantType: string(model.TenantTypeOperations)})
	second, err := tenants.Create(ctx, CreateTenantParams{
		Name:          "second-org-tenant",
		Type:          model.TenantTypeCompany,
		Issuer:        issuer,
		OrgFieldKey:   orgClaimKey,
		OrgFieldValue: "222222222222222222",
	})
	mustNoErr(t, err, "create a second tenant on the shared issuer with a different org value")
	if second.TenantID == "" {
		t.Fatal("expected the second tenant to be created")
	}
}

func TestBootstrapFirstIdentityFallsBackWhenERUNTenantAbsent(t *testing.T) {
	db := identityBootstrapDatabase(t)
	repo := NewIdentityRepository(db, DialectPostgres, "")
	t.Cleanup(func() { clearIdentityBootstrap(t, db) })

	tenant, _, err := repo.ResolveIdentity(context.Background(), security.Claims{
		Issuer:  "https://issuer.example/fallback",
		Subject: "operator-subject",
	})
	mustNoErr(t, err, "bootstrap without ERUN_TENANT")
	if tenant.Name != defaultBootstrapTenantName {
		t.Fatalf("expected fallback tenant name %q, got %q", defaultBootstrapTenantName, tenant.Name)
	}
}

func TestBootstrapFirstIdentityLeavesAlreadyBootstrappedDatabaseAlone(t *testing.T) {
	db := identityBootstrapDatabase(t)
	t.Cleanup(func() { clearIdentityBootstrap(t, db) })

	// A platform already bootstrapped under the old fictional name.
	seeded := NewIdentityRepository(db, DialectPostgres, "")
	original, _, err := seeded.ResolveIdentity(context.Background(), security.Claims{
		Issuer:  "https://issuer.example/already-bootstrapped",
		Subject: "first-operator-subject",
	})
	mustNoErr(t, err, "seed an already-bootstrapped tenant")

	// A second caller, from an unregistered issuer, with ERUN_TENANT now set,
	// must not rename the existing tenant or mint a second bootstrap tenant.
	second := NewIdentityRepository(db, DialectPostgres, "frs")
	_, _, err = second.ResolveIdentity(context.Background(), security.Claims{
		Issuer:  "https://issuer.example/unregistered",
		Subject: "second-subject",
	})
	if err == nil {
		t.Fatal("expected the unregistered issuer to be rejected once a tenant already exists")
	}

	var tenantCount int
	mustNoErr(t, db.QueryRow(`SELECT COUNT(*) FROM tenants`).Scan(&tenantCount), "count tenants")
	if tenantCount != 1 {
		t.Fatalf("expected exactly one tenant left alone, got %d", tenantCount)
	}
	var name string
	mustNoErr(t, db.QueryRow(`SELECT name FROM tenants WHERE tenant_id = $1`, original.TenantID).Scan(&name), "read seeded tenant name")
	if name != defaultBootstrapTenantName {
		t.Fatalf("expected the seeded tenant's name untouched at %q, got %q", defaultBootstrapTenantName, name)
	}
}

// clearIdentityBootstrap resets every table empty-database bootstrap writes to,
// so each scenario starts from a genuinely empty tenants table.
func clearIdentityBootstrap(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, stmt := range []string{
		`DELETE FROM user_roles`,
		`DELETE FROM role_permissions`,
		`DELETE FROM roles`,
		`DELETE FROM user_external_ids`,
		`DELETE FROM users`,
		`DELETE FROM tenant_issuers`,
		`DELETE FROM issuers`,
		`DELETE FROM tenants`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Logf("cleanup %q: %v", stmt, err)
		}
	}
}
