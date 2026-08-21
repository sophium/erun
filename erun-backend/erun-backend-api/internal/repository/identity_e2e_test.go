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
