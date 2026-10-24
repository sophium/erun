package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/testpg"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func tenantsDatabase(t *testing.T) *sql.DB {
	t.Helper()
	databaseURL := testpg.URL(t)
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })
	return db
}

const seedReachableOrgClaim = "urn:zitadel:iam:user:resourceowner:id"

// An org value implies an org-scoped issuer, the only shape that can resolve it.
func seedReachableTenant(t *testing.T, db *sql.DB, label, issuer, orgFieldValue, externalID string) string {
	t.Helper()
	var tenantID string
	err := db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`,
		label+"-"+time.Now().Format("20060102150405.000000"),
	).Scan(&tenantID)
	mustNoErr(t, err, "seed tenant "+label)

	var orgFieldKey any
	if orgFieldValue != "" {
		orgFieldKey = seedReachableOrgClaim
	}
	_, err = db.Exec(
		`INSERT INTO issuers (issuer, org_field_key) VALUES ($1, $2) ON CONFLICT (issuer) DO NOTHING`,
		issuer, orgFieldKey,
	)
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
		// Reachable must ignore TenantID/TenantType and key only on the external identity.
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

func TestListReportsUserCountPerTenant(t *testing.T) {
	db := tenantsDatabase(t)
	stamp := time.Now().Format("20060102150405.000000")

	withUser := seedReachableTenant(t, db, "list-usercount-with-user-"+stamp,
		"https://idp.list-usercount-e2e.example/with-user", "", "list-usercount-e2e-user")

	ctx := security.WithContext(context.Background(), security.Context{TenantType: string(model.TenantTypeOperations)})
	repo := NewTenantRepository(NewTxManager(db, DialectPostgres))
	empty, err := repo.Create(ctx, CreateTenantParams{
		Name:   "list-usercount-empty-" + stamp,
		Type:   model.TenantTypeCompany,
		Issuer: "https://idp.list-usercount-e2e.example/empty",
	})
	mustNoErr(t, err, "register the zero-user tenant")
	t.Cleanup(func() { clearReachableTenants(t, db, withUser, empty.TenantID) })

	tenants, err := repo.List(ctx)
	mustNoErr(t, err, "List")

	counts := make(map[string]*int, len(tenants))
	for i := range tenants {
		counts[tenants[i].TenantID] = tenants[i].UserCount
	}

	withUserCount := counts[withUser]
	if withUserCount == nil || *withUserCount != 1 {
		t.Fatalf("tenant with a seeded user: UserCount = %v, want a pointer to 1", withUserCount)
	}
	emptyCount := counts[empty.TenantID]
	if emptyCount == nil {
		t.Fatalf("tenant with zero users: UserCount = nil, want an explicit pointer to 0, not an unresolved count")
	}
	if *emptyCount != 0 {
		t.Fatalf("tenant with zero users: UserCount = %d, want 0", *emptyCount)
	}
}

func TestReachableReportsWhetherEachMembershipCanActuallyResolve(t *testing.T) {
	db := tenantsDatabase(t)
	const issuer = "https://auth.reachability-e2e.example"
	const callerExternalID = "reachability-e2e-caller"
	const callerOrg = "reachability-e2e-org-home"

	home := seedReachableTenant(t, db, "reachability-e2e-home", issuer, callerOrg, callerExternalID)
	otherOrg := seedReachableTenant(t, db, "reachability-e2e-other-org", issuer, "reachability-e2e-org-other", callerExternalID)
	// No org value under an org-scoped issuer: unreachable for anybody.
	unconfigured := seedReachableTenant(t, db, "reachability-e2e-unconfigured", issuer, "", callerExternalID)
	t.Cleanup(func() { clearReachableTenants(t, db, home, otherOrg, unconfigured) })

	repo := NewTenantRepository(NewTxManager(db, DialectPostgres))
	ctx := security.WithContext(context.Background(), security.Context{
		TenantID:       home,
		TenantType:     "COMPANY",
		ErunUserID:     "irrelevant-for-this-query",
		ExternalIssuer: issuer,
		ExternalUserID: callerExternalID,
		ExternalOrgID:  callerOrg,
	})

	reachable, err := repo.Reachable(ctx)
	mustNoErr(t, err, "Reachable")

	verdicts := make(map[string]model.TenantReachability, len(reachable))
	for _, tenant := range reachable {
		verdicts[tenant.TenantID] = tenant.Reachability
	}
	if len(reachable) != 3 {
		t.Fatalf("expected all 3 memberships reported, got %d: %+v", len(reachable), reachable)
	}
	for _, want := range []struct {
		tenantID string
		verdict  model.TenantReachability
	}{
		{home, model.TenantReachabilityResolvable},
		{otherOrg, model.TenantReachabilityOrgMismatch},
		{unconfigured, model.TenantReachabilityNoOrgMapping},
	} {
		if verdicts[want.tenantID] != want.verdict {
			t.Fatalf("tenant %s: reachability = %q, want %q", want.tenantID, verdicts[want.tenantID], want.verdict)
		}
	}
}

func TestReachableReportsASingleTenantIssuerAsResolvable(t *testing.T) {
	db := tenantsDatabase(t)
	const issuer = "https://idp.reachability-e2e-single.example"
	const externalID = "reachability-e2e-single-caller"

	tenantID := seedReachableTenant(t, db, "reachability-e2e-single", issuer, "", externalID)
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
	if len(reachable) != 1 {
		t.Fatalf("expected exactly one membership, got %+v", reachable)
	}
	if reachable[0].Reachability != model.TenantReachabilityResolvable {
		t.Fatalf("reachability = %q, want %q", reachable[0].Reachability, model.TenantReachabilityResolvable)
	}
}

func TestCreateRefusesATenantNoTokenCanResolveTo(t *testing.T) {
	db := tenantsDatabase(t)
	repo := NewTenantRepository(NewTxManager(db, DialectPostgres))
	ctx := security.WithContext(context.Background(), security.Context{TenantType: string(model.TenantTypeOperations)})

	stamp := time.Now().Format("20060102150405.000000")
	const issuer = "https://auth.create-refusal-e2e.example"
	first, err := repo.Create(ctx, CreateTenantParams{
		Name:          "createrefusalfirst" + stamp,
		Type:          model.TenantTypeCompany,
		Issuer:        issuer,
		OrgFieldKey:   seedReachableOrgClaim,
		OrgFieldValue: "create-refusal-e2e-org-a",
	})
	mustNoErr(t, err, "register the org-scoped issuer's first tenant")
	t.Cleanup(func() { clearReachableTenants(t, db, first.TenantID) })

	_, err = repo.Create(ctx, CreateTenantParams{
		Name:   "createrefusalsecond" + stamp,
		Type:   model.TenantTypeCompany,
		Issuer: issuer,
	})
	var unresolvable *UnresolvableIssuerMappingError
	if !errors.As(err, &unresolvable) {
		t.Fatalf("registering a tenant with no org value on an org-scoped issuer: err = %v, want *UnresolvableIssuerMappingError", err)
	}
	if !strings.Contains(unresolvable.Error(), seedReachableOrgClaim) {
		t.Fatalf("refusal %q does not name the claim the issuer resolves by", unresolvable.Error())
	}

	// The refusal must not leave an orphaned tenants row.
	var count int
	mustNoErr(t, db.QueryRow(`SELECT count(*) FROM tenants WHERE name = $1`, "createrefusalsecond"+stamp).Scan(&count), "count refused tenant")
	if count != 0 {
		t.Fatalf("refused tenant left %d rows behind", count)
	}
}

func TestListReportsWhetherATenantCanBeResolvedAtAll(t *testing.T) {
	db := tenantsDatabase(t)
	const issuer = "https://auth.list-resolvable-e2e.example"

	healthy := seedReachableTenant(t, db, "list-resolvable-e2e-healthy", issuer, "list-resolvable-e2e-org", "list-resolvable-e2e-user")
	// Seeded directly; Create now refuses this shape.
	dead := seedReachableTenant(t, db, "list-resolvable-e2e-dead", issuer, "", "list-resolvable-e2e-user")
	t.Cleanup(func() { clearReachableTenants(t, db, healthy, dead) })

	repo := NewTenantRepository(NewTxManager(db, DialectPostgres))
	ctx := security.WithContext(context.Background(), security.Context{TenantType: string(model.TenantTypeOperations)})
	tenants, err := repo.List(ctx)
	mustNoErr(t, err, "List")

	resolvable := make(map[string]*bool, len(tenants))
	for i := range tenants {
		resolvable[tenants[i].TenantID] = tenants[i].Resolvable
	}
	if got := resolvable[healthy]; got == nil || !*got {
		t.Fatalf("healthy tenant: Resolvable = %v, want a pointer to true", got)
	}
	if got := resolvable[dead]; got == nil || *got {
		t.Fatalf("tenant with no org mapping: Resolvable = %v, want a pointer to false", got)
	}
}
