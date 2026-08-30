package main

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// startupIdentityDatabase reuses the same migrated-PostgreSQL resource
// internal/repository's identity bootstrap e2e tests use: this instance's
// startup status reads the same tenants table their bootstrap writes.
func startupIdentityDatabase(t *testing.T) *sql.DB {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_IDENTITY_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_IDENTITY_DATABASE_URL to a migrated, empty-of-tenants PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedOperationsTenant(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	ctx := context.Background()
	var tenantID string
	if err := db.QueryRowContext(ctx, `INSERT INTO tenants (name, type) VALUES ($1, 'OPERATIONS') RETURNING tenant_id`, name).Scan(&tenantID); err != nil {
		t.Fatalf("seed operations tenant %q: %v", name, err)
	}
	t.Cleanup(func() {
		if _, err := db.ExecContext(ctx, `DELETE FROM tenants WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("cleanup tenant %s: %v", tenantID, err)
		}
	})
	return tenantID
}

// TestIdentityBootstrapStatusReportsPlatformTenantNameMismatch is the
// startup report for erun#1480: a platform whose declared ERUN_TENANT
// disagrees with the name its own OPERATIONS tenant actually bootstrapped
// under says so plainly in the identity status line, rather than leaving the
// mismatch discoverable only by querying the database directly.
func TestIdentityBootstrapStatusReportsPlatformTenantNameMismatch(t *testing.T) {
	db := startupIdentityDatabase(t)
	seedOperationsTenant(t, db, "operations-legacy-test")

	status := identityBootstrapStatus(context.Background(), db, "frs-test")
	want := `tenant name mismatch: declared tenant is "frs-test", OPERATIONS tenant is "operations-legacy-test"; reconcile via PATCH /v1/tenants/reconcile-bootstrap-name`
	if !strings.Contains(status, want) {
		t.Fatalf("identityBootstrapStatus = %q, want it to contain %q", status, want)
	}
}

// TestIdentityBootstrapStatusSilentWhenNamesAgree proves the report fires on
// a real disagreement, not on ERUN_TENANT simply being set.
func TestIdentityBootstrapStatusSilentWhenNamesAgree(t *testing.T) {
	db := startupIdentityDatabase(t)
	seedOperationsTenant(t, db, "frs-agree-test")

	status := identityBootstrapStatus(context.Background(), db, "frs-agree-test")
	if strings.Contains(status, "mismatch") {
		t.Fatalf("identityBootstrapStatus = %q, want no mismatch reported when the names agree", status)
	}
}

// TestIdentityBootstrapStatusSilentWithNoDeclaredTenant proves the report
// never fires with ERUN_TENANT unset, matching every platform before this
// existed.
func TestIdentityBootstrapStatusSilentWithNoDeclaredTenant(t *testing.T) {
	db := startupIdentityDatabase(t)
	seedOperationsTenant(t, db, "operations-no-declared-test")

	status := identityBootstrapStatus(context.Background(), db, "")
	if strings.Contains(status, "mismatch") {
		t.Fatalf("identityBootstrapStatus = %q, want no mismatch reported with no ERUN_TENANT configured", status)
	}
}
