package repository

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Reachable's whole point is a query that crosses tenant_id, which only means
// something proven against real user_external_ids rows under a real migrated
// PostgreSQL — a fake repository would just agree with itself about which
// tenant_id values it decided to return.
func tenantsDatabase(t *testing.T) *sql.DB {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_TENANTS_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_TENANTS_DATABASE_URL to a migrated PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seedReachableTenant creates a tenant, its issuer mapping, one user, and that
// user's external identity — the minimum row set Reachable's join walks.
func seedReachableTenant(t *testing.T, db *sql.DB, label, issuer, orgFieldValue, externalID string) string {
	t.Helper()
	var tenantID string
	err := db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`,
		label+"-"+time.Now().Format("20060102150405.000000"),
	).Scan(&tenantID)
	mustNoErr(t, err, "seed tenant "+label)

	_, err = db.Exec(`INSERT INTO issuers (issuer) VALUES ($1) ON CONFLICT (issuer) DO NOTHING`, issuer)
	mustNoErr(t, err, "seed issuer")

	_, err = db.Exec(
		`INSERT INTO tenant_issuers (tenant_id, issuer, org_field_value, name) VALUES ($1, $2, $3, $4)`,
		tenantID, issuer, sqlNullString(orgFieldValue), label,
	)
	mustNoErr(t, err, "seed tenant_issuers for "+label)

	var userID string
	err = db.QueryRow(
		`INSERT INTO users (tenant_id, username) VALUES ($1, $2) RETURNING user_id`,
		tenantID, label+"-user",
	).Scan(&userID)
	mustNoErr(t, err, "seed user for "+label)

	_, err = db.Exec(
		`INSERT INTO user_external_ids (tenant_id, user_id, issuer, external_id) VALUES ($1, $2, $3, $4)`,
		tenantID, userID, issuer, externalID,
	)
	mustNoErr(t, err, "seed user_external_ids for "+label)

	return tenantID
}

func sqlNullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func clearReachableTenants(t *testing.T, db *sql.DB, tenantIDs ...string) {
	t.Helper()
	for _, tenantID := range tenantIDs {
		for _, table := range []string{"user_external_ids", "users", "tenant_issuers", "tenants"} {
			if _, err := db.Exec(`DELETE FROM `+table+` WHERE tenant_id = $1`, tenantID); err != nil {
				t.Logf("clearing %s for tenant %s: %v", table, tenantID, err)
			}
		}
	}
}

// TestReachableOnlyReturnsTenantsMappedToCallersIdentity is the negative case
// that matters: the same human maps to two tenants under one shared,
// org-scoped issuer (same external_id, different org_field_value), and a
// third, unrelated tenant on the same issuer belongs to a different human. A
// caller resolving as the first human must see exactly their own two tenants
// — never the third, and never anything keyed off a value the caller could
// supply (Reachable takes no arguments; it reads only the security context).
func TestReachableOnlyReturnsTenantsMappedToCallersIdentity(t *testing.T) {
	db := tenantsDatabase(t)
	const issuer = "https://idp.reachable-e2e.example"
	const callerExternalID = "reachable-e2e-caller"
	const strangerExternalID = "reachable-e2e-stranger"

	tenantA := seedReachableTenant(t, db, "reachable-e2e-a", issuer, "org-a", callerExternalID)
	tenantB := seedReachableTenant(t, db, "reachable-e2e-b", issuer, "org-b", callerExternalID)
	tenantC := seedReachableTenant(t, db, "reachable-e2e-c", issuer, "org-c", strangerExternalID)
	t.Cleanup(func() { clearReachableTenants(t, db, tenantA, tenantB, tenantC) })

	repo := NewTenantRepository(NewTxManager(db, DialectPostgres))
	ctx := security.WithContext(context.Background(), security.Context{
		// TenantID/TenantType stand in for whichever of the caller's own
		// tenants this token actually resolved to; Reachable must ignore both
		// and key only on ExternalIssuer/ExternalUserID.
		TenantID:       tenantA,
		TenantType:     "COMPANY",
		ErunUserID:     "irrelevant-for-this-query",
		ExternalIssuer: issuer,
		ExternalUserID: callerExternalID,
	})

	reachable, err := repo.Reachable(ctx)
	mustNoErr(t, err, "Reachable")

	got := make(map[string]bool, len(reachable))
	for _, tenant := range reachable {
		got[tenant.TenantID] = true
	}
	if !got[tenantA] || !got[tenantB] {
		t.Fatalf("expected both of the caller's own tenants (%s, %s) in %+v", tenantA, tenantB, reachable)
	}
	if got[tenantC] {
		t.Fatalf("stranger's tenant %s leaked into the caller's reachable list: %+v", tenantC, reachable)
	}
	if len(reachable) != 2 {
		t.Fatalf("expected exactly 2 reachable tenants, got %d: %+v", len(reachable), reachable)
	}
}

// TestReachableReturnsSingleTenantForSingleTenantCaller guards the common
// case the negative test above doesn't cover on its own: a caller who maps to
// only one tenant must see exactly that one, not zero and not more.
func TestReachableReturnsSingleTenantForSingleTenantCaller(t *testing.T) {
	db := tenantsDatabase(t)
	const issuer = "https://idp.reachable-e2e-single.example"
	const externalID = "reachable-e2e-single-caller"

	tenantID := seedReachableTenant(t, db, "reachable-e2e-single", issuer, "", externalID)
	t.Cleanup(func() { clearReachableTenants(t, db, tenantID) })

	repo := NewTenantRepository(NewTxManager(db, DialectPostgres))
	ctx := security.WithContext(context.Background(), security.Context{
		TenantID:       tenantID,
		TenantType:     "COMPANY",
		ErunUserID:     "irrelevant-for-this-query",
		ExternalIssuer: issuer,
		ExternalUserID: externalID,
	})

	reachable, err := repo.Reachable(ctx)
	mustNoErr(t, err, "Reachable")
	if len(reachable) != 1 || reachable[0].TenantID != tenantID {
		t.Fatalf("expected exactly [%s], got %+v", tenantID, reachable)
	}
}

// TestCreateReportsTenantNameConflictAndGetByNameResolvesIt is erun#1722's
// reproduction: a tenant with the requested name was registered out-of-band
// (a different issuer, standing in for "someone else got there first"), so
// Create's own name-taken write must come back as the specific,
// resolvable ErrTenantNameConflict rather than a generic 500, and GetByName
// must resolve to the tenant that actually holds the name.
func TestCreateReportsTenantNameConflictAndGetByNameResolvesIt(t *testing.T) {
	db := tenantsDatabase(t)
	repo := NewTenantRepository(NewTxManager(db, DialectPostgres))
	ctx := security.WithContext(context.Background(), security.Context{TenantType: string(model.TenantTypeOperations)})

	name := "name-conflict-e2e-" + time.Now().Format("20060102150405.000000")
	first, err := repo.Create(ctx, CreateTenantParams{
		Name:   name,
		Type:   model.TenantTypeCompany,
		Issuer: "https://idp.name-conflict-e2e.example/first",
	})
	mustNoErr(t, err, "register the first tenant to hold the name")
	t.Cleanup(func() { clearReachableTenants(t, db, first.TenantID) })

	_, err = repo.Create(ctx, CreateTenantParams{
		Name:   name,
		Type:   model.TenantTypeCompany,
		Issuer: "https://idp.name-conflict-e2e.example/second",
	})
	if !errors.Is(err, ErrTenantNameConflict) {
		t.Fatalf("second Create with the same name: err = %v, want ErrTenantNameConflict", err)
	}

	resolved, err := repo.GetByName(ctx, name)
	mustNoErr(t, err, "GetByName")
	if resolved.TenantID != first.TenantID {
		t.Fatalf("GetByName returned tenant %s, want the first tenant %s that actually holds the name", resolved.TenantID, first.TenantID)
	}
}
