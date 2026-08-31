package repository

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"strings"
	"testing"
	"time"

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

// TestFirstTenantUserBootstrapGrantsTenantAdminForACompanyTenant proves the
// per-tenant-first-user path (a token resolving to an already-registered
// tenant with zero users) grants a COMPANY tenant's first user TenantAdmin --
// full administration of that tenant, without the platform-operator reach
// ReadAll/WriteAll would also carry inside an OPERATIONS tenant (see
// insertTenantFirstUserAccess).
func TestFirstTenantUserBootstrapGrantsTenantAdminForACompanyTenant(t *testing.T) {
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
	assertHasExactlyRoles(t, db, user.UserID, "TenantAdmin")
}

// TestSecondOperationsTenantFirstUserBootstrapGrantsItsOwnRoles is the
// regression test for a bug caught while adding erun#1480's reconcile workflow: a
// second OPERATIONS-type tenant's own per-tenant-first-user bootstrap runs
// as erun_operations, whose RLS policy is cross-tenant (USING (true)) rather
// than scoped like erun_tenant's. insertDefaultUserAccess used to grant
// predefined roles through an untenanted "WHERE name = ?" lookup that relied
// on the active role's RLS scoping to stay tenant-safe -- true for
// erun_tenant, false for erun_operations, so once any tenant anywhere already
// had a "ReadAll" role, this looked it up, found a foreign tenant's row, and
// the ensuing role_permissions insert (tenant_id defaulted to the new
// tenant, role_id pointing at the other tenant's role) violated its
// composite foreign key outright. Registering a second OPERATIONS tenant is
// a legitimate, documented action (POST /v1/tenants with type=OPERATIONS),
// so this must succeed and grant the new tenant's *own* roles.
func TestSecondOperationsTenantFirstUserBootstrapGrantsItsOwnRoles(t *testing.T) {
	db := identityBootstrapDatabase(t)
	t.Cleanup(func() { clearIdentityBootstrap(t, db) })
	repo := NewIdentityRepository(db, DialectPostgres, "frs")

	// Bootstrap the platform's own OPERATIONS tenant first, so a "ReadAll" and
	// "WriteAll" role already exist somewhere in the database before the
	// second OPERATIONS tenant below ever bootstraps its own.
	_, _, err := repo.ResolveIdentity(context.Background(), security.Claims{
		Issuer:  "https://issuer.example/frs-second-ops",
		Subject: "operator-subject",
	})
	mustNoErr(t, err, "bootstrap the platform's own operations tenant")

	tenants := NewTenantRepository(NewTxManager(db, DialectPostgres))
	ctx := security.WithContext(context.Background(), security.Context{TenantType: string(model.TenantTypeOperations)})
	second, err := tenants.Create(ctx, CreateTenantParams{
		Name:   "second-operations-tenant",
		Type:   model.TenantTypeOperations,
		Issuer: "https://issuer.example/second-operations-tenant",
	})
	mustNoErr(t, err, "register a second operations tenant")

	tenant, user, err := repo.ResolveIdentity(context.Background(), security.Claims{
		Issuer:  "https://issuer.example/second-operations-tenant",
		Subject: "second-operations-first-user",
	})
	mustNoErr(t, err, "bootstrap the second operations tenant's first user")
	if tenant.TenantID != second.TenantID {
		t.Fatalf("expected the second operations tenant, got %q", tenant.TenantID)
	}
	assertHasReadAllAndWriteAll(t, db, user.UserID)

	var roleCount int
	mustNoErr(t, db.QueryRow(`SELECT COUNT(*) FROM roles WHERE tenant_id = $1 AND name IN ('ReadAll', 'WriteAll')`, second.TenantID).Scan(&roleCount), "count the second tenant's own roles")
	if roleCount != 2 {
		t.Fatalf("expected the second operations tenant to own its own ReadAll+WriteAll roles, found %d", roleCount)
	}
}

// assertHasExactlyRoles locks in exactly which predefined roles a bootstrap
// path granted a user, so a fine-grained role-assignment API added alongside
// it stays additive rather than changing the default. want must already be in
// the alphabetical order the underlying query sorts by.
func assertHasExactlyRoles(t *testing.T, db *sql.DB, userID string, want ...string) {
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
	if len(names) != len(want) {
		t.Fatalf("expected exactly %v, got %v", want, names)
	}
	for i, name := range names {
		if name != want[i] {
			t.Fatalf("expected exactly %v, got %v", want, names)
		}
	}
}

// assertHasReadAllAndWriteAll locks in that bootstrap keeps granting exactly
// the predefined ReadAll+WriteAll shape, so the fine-grained role-assignment
// API added alongside it stays additive rather than changing the default.
func assertHasReadAllAndWriteAll(t *testing.T, db *sql.DB, userID string) {
	t.Helper()
	assertHasExactlyRoles(t, db, userID, "ReadAll", "WriteAll")
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

// TestSecondTenantOnSharedIssuerDoesNotBreakFirstTenantsResolution is the
// regression test for erun#1721: an OPERATIONS-tenant user (rihards@frs.lv on
// frs-prod) stopped resolving after a second tenant was registered on the
// same issuer, despite tenant_issuers and user_external_ids both being
// intact. The suspected mechanism was that org-scoping an issuer silently
// leaves the first tenant's mapping behind; TenantIssuerRepository.UpdateOrgScope
// instead backfills it atomically as one explicit, operations-only step (see
// TestTenantIssuerRepositoryUpdateOrgScopeConvertsAndBackfills), and this
// proves that backfill is what keeps the first tenant's already-enrolled user
// resolving once a second tenant shares the issuer -- registering tenant B
// must never change how tenant A resolves.
func TestSecondTenantOnSharedIssuerDoesNotBreakFirstTenantsResolution(t *testing.T) {
	db := identityBootstrapDatabase(t)
	t.Cleanup(func() { clearIdentityBootstrap(t, db) })
	repo := NewIdentityRepository(db, DialectPostgres, "frs")

	const issuer = "https://auth.erunpaas.example/1721"
	const orgClaimKey = "urn:zitadel:iam:user:resourceowner:id"
	const firstOrg = "386994597030592700"
	const secondOrg = "388520359030161586"

	// The first tenant bootstraps single-tenant (no org claim on this token) --
	// the state a platform's own OPERATIONS tenant is in before any other
	// tenant ever shares its issuer.
	firstTenant, firstUser, err := repo.ResolveIdentity(context.Background(), security.Claims{
		Issuer:  issuer,
		Subject: "rihards@frs.lv",
	})
	mustNoErr(t, err, "bootstrap the first tenant single-tenant")

	// Convert the issuer to org-scoped, backfilling the first tenant's own
	// mapping to its org value -- the documented, explicit remedy
	// (PATCH /v1/tenant-issuers), never a silent side effect of registering
	// the second tenant below.
	issuers := NewTenantIssuerRepository(NewTxManager(db, DialectPostgres))
	opsCtx := security.WithContext(context.Background(), security.Context{TenantID: firstTenant.TenantID, TenantType: string(model.TenantTypeOperations)})
	_, err = issuers.UpdateOrgScope(opsCtx, issuer, orgClaimKey, firstOrg)
	mustNoErr(t, err, "convert the issuer to org-scoped and backfill the first tenant's mapping")

	// Register a second tenant on the same issuer under a different org --
	// the action the issue reports as breaking the first tenant.
	tenants := NewTenantRepository(NewTxManager(db, DialectPostgres))
	_, err = tenants.Create(opsCtx, CreateTenantParams{
		Name:          "validationagent",
		Type:          model.TenantTypeCompany,
		Issuer:        issuer,
		OrgFieldKey:   orgClaimKey,
		OrgFieldValue: secondOrg,
	})
	mustNoErr(t, err, "register the second tenant on the shared issuer")

	// The first tenant's already-enrolled user must still resolve, presenting
	// the org claim their token actually carries.
	resolvedTenant, resolvedUser, err := repo.ResolveIdentity(context.Background(), security.Claims{
		Issuer:  issuer,
		Subject: "rihards@frs.lv",
		Raw:     map[string]any{orgClaimKey: firstOrg},
	})
	mustNoErr(t, err, "resolve the first tenant's existing user after a second tenant shares the issuer")
	if resolvedTenant.TenantID != firstTenant.TenantID {
		t.Fatalf("resolved tenant %q, want the first tenant %q", resolvedTenant.TenantID, firstTenant.TenantID)
	}
	if resolvedUser.UserID != firstUser.UserID {
		t.Fatalf("resolved user %q, want the first tenant's original user %q", resolvedUser.UserID, firstUser.UserID)
	}
}

// TestResolveTenantByIssuerReportsUnresolvedForMissingOrgClaim proves the
// erun#1721 root cause is reported distinctly: once an issuer is org-scoped,
// a token that simply carries no matching org claim (exactly what erun-console
// presented before requesting the org scope) must not be confused with "not
// enrolled" -- the caller may be a genuine member of a tenant this one token
// cannot resolve to.
func TestResolveTenantByIssuerReportsUnresolvedForMissingOrgClaim(t *testing.T) {
	db := identityBootstrapDatabase(t)
	t.Cleanup(func() { clearIdentityBootstrap(t, db) })
	repo := NewIdentityRepository(db, DialectPostgres, "frs")

	const issuer = "https://auth.erunpaas.example/1721-unresolved"
	const orgClaimKey = "urn:zitadel:iam:user:resourceowner:id"

	_, _, err := repo.ResolveIdentity(context.Background(), security.Claims{
		Issuer:  issuer,
		Subject: "operator-subject",
		Raw:     map[string]any{orgClaimKey: "111111111111111111"},
	})
	mustNoErr(t, err, "bootstrap an org-scoped issuer")

	_, err = repo.ResolveTenantByIssuer(context.Background(), security.Claims{
		Issuer: issuer,
		// No org claim -- exactly what an OIDC client that never requested the
		// org scope presents.
	})
	if !errors.Is(err, security.ErrTenantUnresolved) {
		t.Fatalf("err = %v, want security.ErrTenantUnresolved", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, must not also satisfy plain ErrNotFound -- ResolveIdentity must not attempt empty-database bootstrap for an issuer it already knows", err)
	}
}

// TestResolveIdentityDistinguishesNotEnrolledFromUnresolved locks in the
// erun#1721 acceptance criterion directly: a genuinely-unenrolled identity (a
// real tenant exists, this subject simply isn't one of its users) must report
// differently from an identity whose tenant could not be resolved at all --
// even when, in the second case, the very same already-enrolled subject
// presents a token this one time cannot resolve.
func TestResolveIdentityDistinguishesNotEnrolledFromUnresolved(t *testing.T) {
	db := identityBootstrapDatabase(t)
	t.Cleanup(func() { clearIdentityBootstrap(t, db) })
	repo := NewIdentityRepository(db, DialectPostgres, "frs")

	const singleTenantIssuer = "https://issuer.example/1721-not-enrolled"
	_, _, err := repo.ResolveIdentity(context.Background(), security.Claims{
		Issuer:  singleTenantIssuer,
		Subject: "first-operator",
	})
	mustNoErr(t, err, "bootstrap the tenant's first user")

	_, _, notEnrolledErr := repo.ResolveIdentity(context.Background(), security.Claims{
		Issuer:  singleTenantIssuer,
		Subject: "a-stranger",
	})
	if notEnrolledErr == nil {
		t.Fatal("expected a stranger to an already-populated tenant to be rejected")
	}
	if errors.Is(notEnrolledErr, security.ErrTenantUnresolved) {
		t.Fatalf("not-enrolled err = %v, must not be classified as tenant-unresolved -- the tenant resolved fine", notEnrolledErr)
	}
	if !errors.Is(notEnrolledErr, ErrNotFound) {
		t.Fatalf("not-enrolled err = %v, want ErrNotFound", notEnrolledErr)
	}

	// A tenant already exists at this point (seeded above), so registering the
	// org-scoped issuer's tenant explicitly through TenantRepository.Create --
	// not empty-database bootstrap, which only ever fires once -- mirrors how
	// a second tenant is actually added to a platform.
	const orgScopedIssuer = "https://auth.erunpaas.example/1721-unresolved-vs-enrolled"
	const orgClaimKey = "urn:zitadel:iam:user:resourceowner:id"
	const org = "222222222222222222"
	tenants := NewTenantRepository(NewTxManager(db, DialectPostgres))
	opsCtx := security.WithContext(context.Background(), security.Context{TenantType: string(model.TenantTypeOperations)})
	_, err = tenants.Create(opsCtx, CreateTenantParams{
		Name:          "org-scoped-tenant",
		Type:          model.TenantTypeCompany,
		Issuer:        orgScopedIssuer,
		OrgFieldKey:   orgClaimKey,
		OrgFieldValue: org,
	})
	mustNoErr(t, err, "register the org-scoped tenant")

	_, _, err = repo.ResolveIdentity(context.Background(), security.Claims{
		Issuer:  orgScopedIssuer,
		Subject: "operator-subject",
		Raw:     map[string]any{orgClaimKey: org},
	})
	mustNoErr(t, err, "bootstrap the org-scoped tenant's first user")

	_, _, unresolvedErr := repo.ResolveIdentity(context.Background(), security.Claims{
		Issuer:  orgScopedIssuer,
		Subject: "operator-subject", // the same, already-enrolled subject
		// No org claim this time.
	})
	if unresolvedErr == nil {
		t.Fatal("expected a missing org claim to be rejected")
	}
	if !errors.Is(unresolvedErr, security.ErrTenantUnresolved) {
		t.Fatalf("unresolved err = %v, want security.ErrTenantUnresolved even though this exact subject is enrolled", unresolvedErr)
	}
}

// TestRefreshUserUsernameCollisionSignsInWithoutLeakingRawSQL proves the auth
// path's own instance of this issue: a token's claimed username refresh
// colliding with a different user already enrolled under it in the same
// tenant must not fail authentication, and must never let the raw Postgres
// unique-violation error (constraint name, SQLSTATE) reach the log as the
// rejection reason -- an already-resolved identity signs in under its
// existing username instead.
func TestRefreshUserUsernameCollisionSignsInWithoutLeakingRawSQL(t *testing.T) {
	db := identityBootstrapDatabase(t)
	t.Cleanup(func() { clearIdentityBootstrap(t, db) })
	repo := NewIdentityRepository(db, DialectPostgres, "")

	const issuer = "https://issuer.example/refresh-collision"

	tenant, alice, err := repo.ResolveIdentity(context.Background(), security.Claims{
		Issuer:   issuer,
		Subject:  "subject-alice",
		Username: "alice",
	})
	mustNoErr(t, err, "bootstrap the first tenant and its first user")

	mustNoErr(t, db.QueryRow(
		`INSERT INTO users (tenant_id, username) VALUES ($1, $2) RETURNING user_id`,
		tenant.TenantID, "bob",
	).Err(), "seed a second user holding the username the refresh will collide on")

	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	_, resolved, err := repo.ResolveIdentity(context.Background(), security.Claims{
		Issuer:   issuer,
		Subject:  "subject-alice",
		Username: "bob", // already taken by the seeded second user
	})
	mustNoErr(t, err, "resolving an identity whose claimed username now collides must still authenticate")
	if resolved.UserID != alice.UserID || resolved.Username != "alice" {
		t.Fatalf("resolved = %+v, want alice's original user/username left unchanged", resolved)
	}

	logged := logBuf.String()
	if strings.Contains(logged, "SQLSTATE") || strings.Contains(logged, "constraint") || strings.Contains(logged, "duplicate key") {
		t.Fatalf("log output leaked a raw Postgres error: %q", logged)
	}
	if !strings.Contains(logged, "username refresh skipped") {
		t.Fatalf("log output = %q, want a safe skipped-refresh reason", logged)
	}
}

// TestResolveIdentityRepeatedWhoamiReadsWithExistingLinkNeverFail reproduces
// erun#1752's exact reported shape at the read path GET /v1/whoami drives on
// every call: a tenant with three enrolled users -- "erun" (its bootstrap
// user), "va-admin", and "rihards@frs.lv", the last already holding a
// user_external_ids link for the reported subject, exactly like the real
// erun platform's own tenant frs -- and a token whose username claim
// collides with a different enrolled user's username. Enrolment through
// ResolveIdentity's own bootstrap only ever creates a *tenant's first* user
// (insertFirstTenantUser refuses once any user exists), so rihards@frs.lv
// and her link are seeded directly, matching how she was really enrolled
// (through the ordinary UserRepository.Create path, not bootstrap).
func TestResolveIdentityRepeatedWhoamiReadsWithExistingLinkNeverFail(t *testing.T) {
	db := identityBootstrapDatabase(t)
	t.Cleanup(func() { clearIdentityBootstrap(t, db) })
	repo := NewIdentityRepository(db, DialectPostgres, "frs")

	const issuer = "https://auth.erunpaas.com"

	// Seed the tenant's other enrolled users, the same shape as tenant frs:
	// "erun" bootstraps the tenant itself, "va-admin" is a second, unrelated
	// user already holding the username the collision will target.
	tenant, _, err := repo.ResolveIdentity(context.Background(), security.Claims{
		Issuer:   issuer,
		Subject:  "erun-subject",
		Username: "erun",
	})
	mustNoErr(t, err, "bootstrap the tenant and its first user")
	mustNoErr(t, db.QueryRow(
		`INSERT INTO users (tenant_id, username) VALUES ($1, $2) RETURNING user_id`,
		tenant.TenantID, "va-admin",
	).Err(), "seed a second, unrelated enrolled user")

	// The reported subject's link already exists, exactly per the issue's
	// ground truth -- seeded directly, since this is not a tenant's first
	// user and ResolveIdentity's own bootstrap would refuse to create it.
	const subject = "387534471668170904"
	var rihardsID string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO users (tenant_id, username) VALUES ($1, $2) RETURNING user_id`,
		tenant.TenantID, "rihards@frs.lv",
	).Scan(&rihardsID), "seed rihards@frs.lv")
	mustNoErr(t, db.QueryRow(
		`INSERT INTO user_external_ids (tenant_id, user_id, issuer, external_id) VALUES ($1, $2, $3, $4)`,
		tenant.TenantID, rihardsID, issuer, subject,
	).Err(), "seed her existing external-id link")

	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	// GET /v1/whoami, repeated, exactly as the issue reported it firing on
	// every call: the token's username claim now collides with va-admin's
	// username, so every one of these calls attempts (and must survive) the
	// same refresh collision.
	for i := 0; i < 3; i++ {
		_, resolved, err := repo.ResolveIdentity(context.Background(), security.Claims{
			Issuer:   issuer,
			Subject:  subject,
			Username: "va-admin", // already taken by the seeded second user
		})
		if err != nil {
			t.Fatalf("whoami read #%d failed: %v (this is the collision GET /v1/whoami must never fail on)", i+1, err)
		}
		if resolved.UserID != rihardsID || resolved.Username != "rihards@frs.lv" {
			t.Fatalf("whoami read #%d resolved = %+v, want rihards@frs.lv's original user/username left unchanged", i+1, resolved)
		}
	}

	logged := logBuf.String()
	if strings.Contains(logged, "SQLSTATE") || strings.Contains(logged, "users_tenant_username_key") || strings.Contains(logged, "duplicate key") {
		t.Fatalf("log output leaked a raw Postgres error across the repeated reads: %q", logged)
	}
}

// TestBootstrapFirstTenantUserSanitizesARealRaceOnTheUsernameConstraint
// proves erun#1752 items 2 and 3 for the *other* candidate branch named in
// the issue: two callers racing to become a zero-user tenant's first user,
// both deriving the same username, must not let the loser's raw
// unique-violation (constraint name, SQLSTATE) escape ResolveIdentity -- it
// is sanitized into ErrIdentityResolutionFailed exactly like the
// refresh-collision branch above, but distinguishably logged with step
// "bootstrap first tenant user" rather than "username refresh" (the two
// otherwise violate the identical constraint and would be indistinguishable
// from the auth-rejected log line alone). The race is forced
// deterministically, never by racing two goroutines and hoping for a
// particular interleaving: a second, independent connection holds an
// uncommitted insert of the exact colliding row so the racing
// ResolveIdentity call's own insert genuinely blocks on the real unique
// index, observed through pg_stat_activity, and only then does that
// connection commit -- there is exactly one possible outcome once it does.
func TestBootstrapFirstTenantUserSanitizesARealRaceOnTheUsernameConstraint(t *testing.T) {
	db := identityBootstrapDatabase(t)
	t.Cleanup(func() { clearIdentityBootstrap(t, db) })
	repo := NewIdentityRepository(db, DialectPostgres, "frs")

	_, _, err := repo.ResolveIdentity(context.Background(), security.Claims{
		Issuer:  "https://issuer.example/1752-race",
		Subject: "operator-subject",
	})
	mustNoErr(t, err, "bootstrap the platform tenant")

	var secondTenantID string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`,
		"1752-race-tenant",
	).Scan(&secondTenantID), "seed a second, zero-user tenant")
	const issuer = "https://issuer.example/1752-race-second"
	mustNoErr(t, db.QueryRow(`INSERT INTO issuers (issuer) VALUES ($1)`, issuer).Err(), "register second tenant issuer")
	mustNoErr(t, db.QueryRow(
		`INSERT INTO tenant_issuers (tenant_id, issuer, name) VALUES ($1, $2, $3)`,
		secondTenantID, issuer, "second tenant issuer",
	).Err(), "map second tenant issuer")

	// Hold an uncommitted insert of the exact row the racing bootstrap call
	// will also try to insert, on a second, independent connection.
	ctx := context.Background()
	blocker, err := db.Conn(ctx)
	mustNoErr(t, err, "open blocker connection")
	t.Cleanup(func() { _ = blocker.Close() })
	blockTx, err := blocker.BeginTx(ctx, nil)
	mustNoErr(t, err, "begin blocker tx")
	_, err = blockTx.ExecContext(ctx, `INSERT INTO users (tenant_id, username) VALUES ($1, $2)`, secondTenantID, "racer")
	mustNoErr(t, err, "blocker's own insert of the colliding row")

	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	resultCh := make(chan error, 1)
	go func() {
		_, _, raceErr := repo.ResolveIdentity(context.Background(), security.Claims{
			Issuer:   issuer,
			Subject:  "racer-subject",
			Username: "racer",
		})
		resultCh <- raceErr
	}()

	// Wait until PostgreSQL itself reports the racing insert genuinely
	// blocked on the blocker's uncommitted row -- an observable condition,
	// never a sleep-and-hope interleaving.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var blocked int
		mustNoErr(t, db.QueryRowContext(ctx, `
			SELECT count(*) FROM pg_stat_activity
			 WHERE state = 'active' AND wait_event_type = 'Lock'
		`).Scan(&blocked), "poll pg_stat_activity for the blocked racer")
		if blocked > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the racing insert to block on the unique index")
		}
		time.Sleep(20 * time.Millisecond)
	}

	mustNoErr(t, blockTx.Commit(), "commit the blocker's row, letting the racing insert re-check and fail")

	raceErr := <-resultCh
	if !errors.Is(raceErr, ErrIdentityResolutionFailed) {
		t.Fatalf("err = %v, want ErrIdentityResolutionFailed", raceErr)
	}
	logged := logBuf.String()
	if strings.Contains(logged, "SQLSTATE") || strings.Contains(logged, "users_tenant_username_key") || strings.Contains(logged, "duplicate key") {
		t.Fatalf("log output leaked a raw Postgres error: %q", logged)
	}
	if !strings.Contains(logged, `step="bootstrap first tenant user"`) {
		t.Fatalf("log output = %q, want the bootstrap step label, distinguishing it from a username-refresh collision", logged)
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
