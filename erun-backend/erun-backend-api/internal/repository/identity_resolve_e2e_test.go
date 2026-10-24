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

// TestResolveTenantByIssuerReportsUnresolvedForMissingOrgClaim proves that once
// an issuer is org-scoped, a token that carries no matching org claim is not
// confused with "not enrolled" -- the caller may be a genuine member of a tenant this one token
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

// TestResolveIdentityDistinguishesNotEnrolledFromUnresolved locks in that a
// genuinely-unenrolled identity (a
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

// TestResolveIdentityRepeatedWhoamiReadsWithExistingLinkNeverFail exercises
// the read path GET /v1/whoami drives on every call: a tenant with three
// enrolled users -- "erun" (its bootstrap user), "va-admin", and
// "rihards@frs.lv", the last already holding a user_external_ids link for the
// subject -- and a token whose username claim collides with a different
// enrolled user's username. Enrolment through
// ResolveIdentity's own bootstrap only ever creates a *tenant's first* user
// (insertFirstTenantUser refuses once any user exists), so rihards@frs.lv
// and her link are seeded directly, as the ordinary UserRepository.Create
// path would enrol her.
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

	// The subject's link already exists -- seeded directly, since this is not a tenant's first
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

	// GET /v1/whoami, repeated, as it fires on every call: the token's username claim now collides with va-admin's
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
// covers the other branch that writes users: two callers racing to become a zero-user tenant's first user,
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
