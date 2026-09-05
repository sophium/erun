package repository

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// UpdateOrgScope's whole point is a two-table write (issuers.org_field_key
// plus the existing tenant's tenant_issuers.org_field_value) that must commit
// or fail together, which only means something proven against a real
// migrated PostgreSQL — a fake repository would just agree with itself about
// what it decided to persist.
func tenantIssuersDatabase(t *testing.T) *sql.DB {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_TENANT_ISSUERS_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_TENANT_ISSUERS_DATABASE_URL to a migrated PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestTenantIssuerRepositoryUpdateOrgScopeConvertsAndBackfills proves the
// migration path for erun#1605's second defect: an issuer already registered
// single-tenant (org_field_key NULL, the state first-identity bootstrap
// leaves an already-deployed platform in) can be converted to org-scoped, and
// the existing tenant's own mapping is backfilled with an org value in the
// same operation — not left NULL, which would stop that tenant resolving as
// soon as the issuer stops being single-tenant.
func TestTenantIssuerRepositoryUpdateOrgScopeConvertsAndBackfills(t *testing.T) {
	db := tenantIssuersDatabase(t)
	const issuer = "https://auth.erunpaas.example/migrate-e2e"
	const orgFieldKey = "urn:zitadel:iam:user:resourceowner:id"
	const orgFieldValue = "platform-org-999"

	var tenantID string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'OPERATIONS') RETURNING tenant_id`,
		"migrate-e2e-tenant",
	).Scan(&tenantID), "seed tenant")
	t.Cleanup(func() {
		for _, table := range []string{"tenant_issuers", "tenants"} {
			if _, err := db.Exec(`DELETE FROM `+table+` WHERE tenant_id = $1`, tenantID); err != nil {
				t.Logf("clearing %s for tenant %s: %v", table, tenantID, err)
			}
		}
		if _, err := db.Exec(`DELETE FROM issuers WHERE issuer = $1`, issuer); err != nil {
			t.Logf("clearing issuers %s: %v", issuer, err)
		}
	})

	// Seed the single-tenant registration first-identity bootstrap would have
	// left behind: org_field_key NULL on issuers, org_field_value NULL on the
	// existing tenant's own mapping.
	_, err := db.Exec(`INSERT INTO issuers (issuer) VALUES ($1)`, issuer)
	mustNoErr(t, err, "seed single-tenant issuer")
	_, err = db.Exec(
		`INSERT INTO tenant_issuers (tenant_id, issuer, name) VALUES ($1, $2, $3)`,
		tenantID, issuer, "migrate-e2e issuer",
	)
	mustNoErr(t, err, "seed existing single-tenant mapping")

	repo := NewTenantIssuerRepository(NewTxManager(db, DialectPostgres))
	ctx := security.WithContext(context.Background(), security.Context{TenantID: tenantID, TenantType: "OPERATIONS"})
	updated, err := repo.UpdateOrgScope(ctx, tenantID, issuer, orgFieldKey, orgFieldValue)
	mustNoErr(t, err, "UpdateOrgScope")
	if updated.OrgFieldKey != orgFieldKey || updated.OrgFieldValue != orgFieldValue {
		t.Fatalf("returned tenant_issuer = %+v, want key=%q value=%q", updated, orgFieldKey, orgFieldValue)
	}

	var storedKey sql.NullString
	mustNoErr(t, db.QueryRow(`SELECT org_field_key FROM issuers WHERE issuer = $1`, issuer).Scan(&storedKey), "read issuers row")
	if !storedKey.Valid || storedKey.String != orgFieldKey {
		t.Fatalf("issuers.org_field_key = %+v, want %q", storedKey, orgFieldKey)
	}

	var storedValue sql.NullString
	mustNoErr(t, db.QueryRow(
		`SELECT org_field_value FROM tenant_issuers WHERE tenant_id = $1 AND issuer = $2`,
		tenantID, issuer,
	).Scan(&storedValue), "read tenant_issuers row")
	if !storedValue.Valid || storedValue.String != orgFieldValue {
		t.Fatalf("tenant_issuers.org_field_value = %+v, want the existing tenant's mapping backfilled to %q, not left NULL", storedValue, orgFieldValue)
	}
}

// TestTenantIssuersUniqueConstraintRejectsAmbiguousMapping proves the
// erun#1721 acceptance criterion that ambiguity between two candidate
// tenant_issuers rows for the same (issuer, org) is impossible by
// construction, not a runtime coin flip: the UNIQUE NULLS NOT DISTINCT
// (issuer, org_field_value) constraint refuses a second row before
// ResolveTenantByIssuer's exact-match query could ever see two candidates for
// the same resolution key.
func TestTenantIssuersUniqueConstraintRejectsAmbiguousMapping(t *testing.T) {
	db := tenantIssuersDatabase(t)
	const issuer = "https://auth.erunpaas.example/1721-ambiguity"
	const org = "999999999999999999"

	var firstTenantID, secondTenantID string
	mustNoErr(t, db.QueryRow(`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`, "ambiguity-first").Scan(&firstTenantID), "seed first tenant")
	mustNoErr(t, db.QueryRow(`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`, "ambiguity-second").Scan(&secondTenantID), "seed second tenant")
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM tenant_issuers WHERE tenant_id IN ($1, $2)`, firstTenantID, secondTenantID)
		_, _ = db.Exec(`DELETE FROM tenants WHERE tenant_id IN ($1, $2)`, firstTenantID, secondTenantID)
		_, _ = db.Exec(`DELETE FROM issuers WHERE issuer = $1`, issuer)
	})

	_, err := db.Exec(`INSERT INTO issuers (issuer, org_field_key) VALUES ($1, $2)`, issuer, "urn:zitadel:iam:user:resourceowner:id")
	mustNoErr(t, err, "seed an org-scoped issuer")
	_, err = db.Exec(`INSERT INTO tenant_issuers (tenant_id, issuer, org_field_value, name) VALUES ($1, $2, $3, $4)`, firstTenantID, issuer, org, "first")
	mustNoErr(t, err, "seed the first mapping")

	_, err = db.Exec(`INSERT INTO tenant_issuers (tenant_id, issuer, org_field_value, name) VALUES ($1, $2, $3, $4)`, secondTenantID, issuer, org, "second")
	if err == nil {
		t.Fatal("expected a second tenant_issuers row for the same (issuer, org_field_value) to be rejected by the unique constraint")
	}

	var rowCount int
	mustNoErr(t, db.QueryRow(`SELECT COUNT(*) FROM tenant_issuers WHERE issuer = $1 AND org_field_value = $2`, issuer, org).Scan(&rowCount), "count mappings")
	if rowCount != 1 {
		t.Fatalf("expected exactly one mapping to survive the rejected duplicate insert, got %d", rowCount)
	}
}
