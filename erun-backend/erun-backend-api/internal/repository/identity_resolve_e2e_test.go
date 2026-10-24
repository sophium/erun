package repository

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

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
		// No org claim, as a client that never requested the org scope presents.
	})
	if !errors.Is(err, security.ErrTenantUnresolved) {
		t.Fatalf("err = %v, want security.ErrTenantUnresolved", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, must not also satisfy plain ErrNotFound -- ResolveIdentity must not attempt empty-database bootstrap for an issuer it already knows", err)
	}
}

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
	})
	if unresolvedErr == nil {
		t.Fatal("expected a missing org claim to be rejected")
	}
	if !errors.Is(unresolvedErr, security.ErrTenantUnresolved) {
		t.Fatalf("unresolved err = %v, want security.ErrTenantUnresolved even though this exact subject is enrolled", unresolvedErr)
	}
}

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

// rihards@frs.lv is seeded directly because bootstrap only creates a tenant's first user.
func TestResolveIdentityRepeatedWhoamiReadsWithExistingLinkNeverFail(t *testing.T) {
	db := identityBootstrapDatabase(t)
	t.Cleanup(func() { clearIdentityBootstrap(t, db) })
	repo := NewIdentityRepository(db, DialectPostgres, "frs")

	const issuer = "https://auth.erunpaas.com"

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

// The race is forced deterministically: a second connection holds an
// uncommitted colliding insert until pg_stat_activity shows the bootstrap
// insert blocked on it.
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
