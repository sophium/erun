package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// bootstrapOperationsTenant enrols a platform's own OPERATIONS tenant exactly
// the way empty-database bootstrap does, and returns the security context an
// authenticated caller from that tenant would carry.
func bootstrapOperationsTenant(t *testing.T, db *sql.DB, platformTenant, issuer string) (model.Tenant, security.Context) {
	t.Helper()
	identity := NewIdentityRepository(db, DialectPostgres, platformTenant)
	tenant, user, err := identity.ResolveIdentity(context.Background(), security.Claims{
		Issuer:  issuer,
		Subject: "operator-subject",
	})
	mustNoErr(t, err, "bootstrap operations tenant")
	return tenant, security.Context{TenantID: tenant.TenantID, TenantType: string(model.TenantTypeOperations), ErunUserID: user.UserID}
}

func tenantName(t *testing.T, db *sql.DB, tenantID string) string {
	t.Helper()
	var name string
	mustNoErr(t, db.QueryRow(`SELECT name FROM tenants WHERE tenant_id = $1`, tenantID).Scan(&name), "read tenant name")
	return name
}

// TestReconcileSelfNameRenamesLegacyPlatformTenant is the migration case this
// feature exists for: a platform bootstrapped before ERUN_TENANT was read
// still carries the placeholder "operations" name; reconciling against the
// platform's now-declared identity renames it, and every row that referenced
// the tenant by its id (never its name) still resolves afterward.
func TestReconcileSelfNameRenamesLegacyPlatformTenant(t *testing.T) {
	db := identityBootstrapDatabase(t)
	t.Cleanup(func() { clearIdentityBootstrap(t, db) })

	issuer := "https://issuer.example/legacy-platform"
	legacy, securityContext := bootstrapOperationsTenant(t, db, "", issuer)
	if legacy.Name != defaultBootstrapTenantName {
		t.Fatalf("expected the legacy placeholder name %q, got %q", defaultBootstrapTenantName, legacy.Name)
	}

	tenants := NewTenantRepository(NewTxManager(db, DialectPostgres), "frs")
	ctx := security.WithContext(context.Background(), securityContext)

	reconciled, renamed, err := tenants.ReconcileSelfName(ctx)
	mustNoErr(t, err, "reconcile legacy tenant name")
	if !renamed {
		t.Fatal("expected the legacy tenant to be renamed")
	}
	if reconciled.Name != "frs" {
		t.Fatalf("expected the tenant renamed to %q, got %q", "frs", reconciled.Name)
	}
	if reconciled.TenantID != legacy.TenantID {
		t.Fatalf("reconcile must rename in place, not mint a new tenant: got %q, want %q", reconciled.TenantID, legacy.TenantID)
	}

	// Nothing orphaned: the issuer mapping and the bootstrapped user are keyed
	// by tenant_id, never by name, so the exact same identity still resolves
	// to the exact same tenant and user after the rename.
	identity := NewIdentityRepository(db, DialectPostgres, "frs")
	resolvedTenant, resolvedUser, err := identity.ResolveIdentity(context.Background(), security.Claims{
		Issuer:  issuer,
		Subject: "operator-subject",
	})
	mustNoErr(t, err, "resolve identity after rename")
	if resolvedTenant.TenantID != legacy.TenantID || resolvedTenant.Name != "frs" {
		t.Fatalf("expected the same tenant now named %q, got %+v", "frs", resolvedTenant)
	}
	if resolvedUser.UserID == "" {
		t.Fatal("expected the previously bootstrapped user to still resolve, not be recreated")
	}
	assertHasReadAllAndWriteAll(t, db, resolvedUser.UserID)
}

// TestReconcileSelfNameIsNoOpOnAnAlreadyCorrectPlatform proves a platform
// bootstrapped after #1066 (whose tenant is already named after ERUN_TENANT)
// is left completely untouched, not merely renamed to the same value.
func TestReconcileSelfNameIsNoOpOnAnAlreadyCorrectPlatform(t *testing.T) {
	db := identityBootstrapDatabase(t)
	t.Cleanup(func() { clearIdentityBootstrap(t, db) })

	correct, securityContext := bootstrapOperationsTenant(t, db, "frs", "https://issuer.example/already-correct")
	if correct.Name != "frs" {
		t.Fatalf("expected bootstrap to enrol the declared name %q, got %q", "frs", correct.Name)
	}
	var updatedAtBefore string
	mustNoErr(t, db.QueryRow(`SELECT updated_at::text FROM tenants WHERE tenant_id = $1`, correct.TenantID).Scan(&updatedAtBefore), "read updated_at before reconcile")

	tenants := NewTenantRepository(NewTxManager(db, DialectPostgres), "frs")
	ctx := security.WithContext(context.Background(), securityContext)

	reconciled, renamed, err := tenants.ReconcileSelfName(ctx)
	mustNoErr(t, err, "reconcile an already-correct tenant")
	if renamed {
		t.Fatal("expected no rename on an already-correct platform")
	}
	if reconciled.Name != "frs" {
		t.Fatalf("expected the name unchanged at %q, got %q", "frs", reconciled.Name)
	}

	var updatedAtAfter string
	mustNoErr(t, db.QueryRow(`SELECT updated_at::text FROM tenants WHERE tenant_id = $1`, correct.TenantID).Scan(&updatedAtAfter), "read updated_at after reconcile")
	if updatedAtBefore != updatedAtAfter {
		t.Fatalf("expected no write at all on a no-op reconcile: updated_at moved from %q to %q", updatedAtBefore, updatedAtAfter)
	}
}

// TestReconcileSelfNameIsSafeToRunTwice locks in idempotency: a second call
// after a successful rename converges (already matches) rather than erroring
// or renaming again.
func TestReconcileSelfNameIsSafeToRunTwice(t *testing.T) {
	db := identityBootstrapDatabase(t)
	t.Cleanup(func() { clearIdentityBootstrap(t, db) })

	legacy, securityContext := bootstrapOperationsTenant(t, db, "", "https://issuer.example/run-twice")
	tenants := NewTenantRepository(NewTxManager(db, DialectPostgres), "frs")
	ctx := security.WithContext(context.Background(), securityContext)

	first, firstRenamed, err := tenants.ReconcileSelfName(ctx)
	mustNoErr(t, err, "first reconcile")
	if !firstRenamed || first.Name != "frs" {
		t.Fatalf("expected the first call to rename to %q, got renamed=%v name=%q", "frs", firstRenamed, first.Name)
	}

	second, secondRenamed, err := tenants.ReconcileSelfName(ctx)
	mustNoErr(t, err, "second reconcile")
	if secondRenamed {
		t.Fatal("expected the second call to be a no-op, not a repeat rename")
	}
	if second.Name != "frs" || second.TenantID != legacy.TenantID {
		t.Fatalf("expected the second call to report the same converged tenant, got %+v", second)
	}
}

// TestReconcileSelfNameRefusesWhenTenantHasEnvironments guards the
// <tenant>-<env> runtime namespace invariant: a tenant that already has
// environments must not be renamed out from under them.
func TestReconcileSelfNameRefusesWhenTenantHasEnvironments(t *testing.T) {
	db := identityBootstrapDatabase(t)
	t.Cleanup(func() { clearIdentityBootstrap(t, db) })
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM environments`); err != nil {
			t.Logf("clearing environments: %v", err)
		}
	})

	legacy, securityContext := bootstrapOperationsTenant(t, db, "", "https://issuer.example/has-environments")
	environments := NewEnvironmentRepository(NewTxManager(db, DialectPostgres))
	ctx := security.WithContext(context.Background(), securityContext)
	_, err := environments.Create(ctx, model.Environment{Name: "prod", Type: model.EnvironmentTypeRuntime, RuntimeVersion: "1.0.0"})
	mustNoErr(t, err, "seed an environment for the legacy tenant")

	tenants := NewTenantRepository(NewTxManager(db, DialectPostgres), "frs")
	_, _, err = tenants.ReconcileSelfName(ctx)
	if !errors.Is(err, ErrTenantHasEnvironments) {
		t.Fatalf("expected ErrTenantHasEnvironments, got %v", err)
	}

	if name := tenantName(t, db, legacy.TenantID); name != defaultBootstrapTenantName {
		t.Fatalf("expected the refused rename to leave the name at %q, got %q", defaultBootstrapTenantName, name)
	}
}
