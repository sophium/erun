package backendapi

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// The release queue's correctness lives in SQL — a conditional claim, a partial
// unique index, a cooldown window, a per-commit uniqueness contract — so it is
// exercised against a real migrated PostgreSQL rather than a fake that agrees
// with itself. Needs only the database, no cluster.
func releaseQueueDatabase(t *testing.T) (*repository.ReleaseRepository, context.Context, *sql.DB) {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_RELEASE_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_RELEASE_DATABASE_URL to a migrated PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	tenantID := seedQueueTenant(t, db)
	ctx := security.WithContext(context.Background(), security.Context{TenantID: tenantID, TenantType: "COMPANY"})
	repo := repository.NewReleaseRepository(repository.NewTxManager(db, repository.DialectPostgres))
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM releases WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing the test tenant's releases: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM tenants WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing the test tenant: %v", err)
		}
	})
	return repo, ctx, db
}

// seedQueueTenant creates a tenant of its own for the scenario, so a run never
// disturbs rows another tenant owns and row-level security is actually exercised.
func seedQueueTenant(t *testing.T, db *sql.DB) string {
	t.Helper()
	var tenantID string
	err := db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`,
		"release-queue-e2e-"+time.Now().Format("20060102150405.000000"),
	).Scan(&tenantID)
	mustNoErr(t, err, "seed tenant")
	return tenantID
}

func enqueueRelease(t *testing.T, repo *repository.ReleaseRepository, ctx context.Context, commit string) model.Release {
	t.Helper()
	release, err := repo.Create(ctx, model.Release{TargetBranch: "main", CommitID: commit})
	mustNoErr(t, err, "enqueue "+commit)
	return release
}

// TestReleaseQueueClaimIsSerialPerTenant is the invariant the whole feature rests
// on: two claims cannot both hand out a release for one tenant, and the second
// only becomes claimable once the first is terminal.
func TestReleaseQueueClaimIsSerialPerTenant(t *testing.T) {
	repo, ctx, _ := releaseQueueDatabase(t)
	first := enqueueRelease(t, repo, ctx, "commit-serial-a")
	second := enqueueRelease(t, repo, ctx, "commit-serial-b")

	noCooldown := repository.ClaimWindow{}
	claimed, ok, err := repo.ClaimNext(ctx, noCooldown)
	mustNoErr(t, err, "first claim")
	if !ok {
		t.Fatal("nothing was claimable with two releases queued")
	}
	// FIFO by the UUIDv7 release id: the release enqueued first runs first.
	if claimed.ReleaseID != first.ReleaseID {
		t.Fatalf("claimed %s, want the head of the queue %s", claimed.ReleaseID, first.ReleaseID)
	}
	if claimed.Status != model.ReleaseStatusRunning {
		t.Fatalf("claimed status = %q, want running", claimed.Status)
	}

	_, ok, err = repo.ClaimNext(ctx, noCooldown)
	mustNoErr(t, err, "second claim")
	if ok {
		t.Fatal("a second release was claimed while one was in flight, so two releases could run on one version line")
	}

	mustNoErr(t, repo.RecordOutcome(ctx, first.ReleaseID, repository.ReleaseOutcome{
		Status: model.ReleaseStatusReleased, Version: "1.0.150",
	}), "record the first outcome")

	claimed, ok, err = repo.ClaimNext(ctx, noCooldown)
	mustNoErr(t, err, "third claim")
	if !ok || claimed.ReleaseID != second.ReleaseID {
		t.Fatalf("after the first finished, claimed %+v (ok=%v), want %s", claimed, ok, second.ReleaseID)
	}
}

// TestReleaseQueueRefusesTwoRunningRows proves the invariant is the database's,
// not the query's: a claim that raced past the predicate still cannot leave two
// running rows behind.
func TestReleaseQueueRefusesTwoRunningRows(t *testing.T) {
	repo, ctx, db := releaseQueueDatabase(t)
	first := enqueueRelease(t, repo, ctx, "commit-index-a")
	second := enqueueRelease(t, repo, ctx, "commit-index-b")

	_, ok, err := repo.ClaimNext(ctx, repository.ClaimWindow{})
	mustNoErr(t, err, "claim")
	if !ok {
		t.Fatal("nothing was claimable")
	}
	// Forcing the second row to running behind the query's back is what a lost
	// race would look like; the partial unique index has to reject it.
	_, err = db.Exec(`UPDATE releases SET status = 'running' WHERE release_id = $1`, second.ReleaseID)
	if err == nil {
		t.Fatalf("a second running release was accepted for one tenant (first=%s second=%s)", first.ReleaseID, second.ReleaseID)
	}
	t.Logf("the database refused the second running release: %v", err)
}

// TestReleaseQueueHoldsOneReleasePerCommit is the idempotency contract at the
// storage layer: a second row for one merge commit cannot exist, so a second
// version for one commit cannot be minted.
func TestReleaseQueueHoldsOneReleasePerCommit(t *testing.T) {
	repo, ctx, _ := releaseQueueDatabase(t)
	enqueueRelease(t, repo, ctx, "commit-unique")

	if _, err := repo.Create(ctx, model.Release{TargetBranch: "main", CommitID: "commit-unique"}); err == nil {
		t.Fatal("a second release row was created for one merge commit")
	}
	found, err := repo.FindByCommit(ctx, "commit-unique")
	mustNoErr(t, err, "find by commit")
	if found.CommitID != "commit-unique" {
		t.Fatalf("found %+v, want the release already recorded for the commit", found)
	}
}

// TestReleaseQueueCooldownSpacesConsecutiveReleases: a trigger stuck in a loop
// must not spend the tenant's capacity on back-to-back runs.
func TestReleaseQueueCooldownSpacesConsecutiveReleases(t *testing.T) {
	repo, ctx, _ := releaseQueueDatabase(t)
	first := enqueueRelease(t, repo, ctx, "commit-cooldown-a")
	enqueueRelease(t, repo, ctx, "commit-cooldown-b")

	_, ok, err := repo.ClaimNext(ctx, repository.ClaimWindow{})
	mustNoErr(t, err, "claim the first")
	if !ok {
		t.Fatal("nothing was claimable")
	}
	mustNoErr(t, repo.RecordOutcome(ctx, first.ReleaseID, repository.ReleaseOutcome{
		Status: model.ReleaseStatusReleased, Version: "1.0.150",
	}), "finish the first")

	if _, ok, err = repo.ClaimNext(ctx, repository.ClaimWindow{Cooldown: time.Hour}); err != nil {
		t.Fatalf("claim inside the cooldown: %v", err)
	} else if ok {
		t.Fatal("the next release started immediately, so a runaway trigger has no brake")
	}
	if _, ok, err = repo.ClaimNext(ctx, repository.ClaimWindow{}); err != nil {
		t.Fatalf("claim past the cooldown: %v", err)
	} else if !ok {
		t.Fatal("the next release stayed blocked once the cooldown was over")
	}
}

// TestReleaseQueueRequeueBumpsTheAttempt: the attempt is what keys the retry's
// Job and workflow, so a retry that reused it would replay instead of running.
func TestReleaseQueueRequeueBumpsTheAttempt(t *testing.T) {
	repo, ctx, _ := releaseQueueDatabase(t)
	release := enqueueRelease(t, repo, ctx, "commit-requeue")
	mustNoErr(t, repo.RecordOutcome(ctx, release.ReleaseID, repository.ReleaseOutcome{
		Status: model.ReleaseStatusFailed, FailureReason: "the registry rejected the push",
	}), "fail the release")

	requeued, err := repo.Requeue(ctx, release.ReleaseID)
	mustNoErr(t, err, "requeue")
	if requeued.Attempt != release.Attempt+1 {
		t.Fatalf("attempt = %d, want %d", requeued.Attempt, release.Attempt+1)
	}
	if requeued.Status != model.ReleaseStatusQueued || requeued.FailureReason != "" {
		t.Fatalf("requeued = %+v, want a clean queued row", requeued)
	}

	// A published release is never requeued: its version is public, and a second
	// one for the same commit is the failure this queue exists to prevent.
	mustNoErr(t, repo.RecordOutcome(ctx, release.ReleaseID, repository.ReleaseOutcome{
		Status: model.ReleaseStatusReleased, Version: "1.0.150",
	}), "publish the release")
	if _, err := repo.Requeue(ctx, release.ReleaseID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("requeue of a released commit error = %v, want it refused", err)
	}
}

// TestReleaseQueueExpiresAnAbandonedRelease: a control plane that died
// mid-release must not hold its tenant's only in-flight slot forever.
func TestReleaseQueueExpiresAnAbandonedRelease(t *testing.T) {
	repo, ctx, _ := releaseQueueDatabase(t)
	abandoned := enqueueRelease(t, repo, ctx, "commit-stale")
	next := enqueueRelease(t, repo, ctx, "commit-after-stale")

	_, ok, err := repo.ClaimNext(ctx, repository.ClaimWindow{})
	mustNoErr(t, err, "claim")
	if !ok {
		t.Fatal("nothing was claimable")
	}

	// A zero window makes every in-flight release stale, which is the
	// crashed-control-plane recovery.
	expired, err := repo.ExpireStale(ctx, 0, "the control plane stopped reporting")
	mustNoErr(t, err, "expire stale")
	if expired != 1 {
		t.Fatalf("expired %d releases, want the one in flight", expired)
	}
	stale, err := repo.Get(ctx, abandoned.ReleaseID)
	mustNoErr(t, err, "read the expired release")
	if stale.Status != model.ReleaseStatusFailed || stale.FailureReason == "" {
		t.Fatalf("expired release = %+v, want a failed row naming why", stale)
	}

	claimed, ok, err := repo.ClaimNext(ctx, repository.ClaimWindow{})
	mustNoErr(t, err, "claim after expiry")
	if !ok || claimed.ReleaseID != next.ReleaseID {
		t.Fatalf("claimed %+v (ok=%v), want the queue to have moved on to %s", claimed, ok, next.ReleaseID)
	}
}

// TestReleaseQueueIsScopedByTenant: one tenant's in-flight release must not stop
// another tenant's queue, and must not be visible to it.
func TestReleaseQueueIsScopedByTenant(t *testing.T) {
	repo, ctx, db := releaseQueueDatabase(t)
	enqueueRelease(t, repo, ctx, "commit-tenant-a")
	if _, ok, err := repo.ClaimNext(ctx, repository.ClaimWindow{}); err != nil || !ok {
		t.Fatalf("claim for the first tenant: ok=%v err=%v", ok, err)
	}

	otherTenant := seedQueueTenant(t, db)
	otherCtx := security.WithContext(context.Background(), security.Context{TenantID: otherTenant, TenantType: "COMPANY"})
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM releases WHERE tenant_id = $1`, otherTenant); err != nil {
			t.Logf("clearing the other tenant's releases: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM tenants WHERE tenant_id = $1`, otherTenant); err != nil {
			t.Logf("clearing the other tenant: %v", err)
		}
	})
	other := enqueueRelease(t, repo, otherCtx, "commit-tenant-b")

	claimed, ok, err := repo.ClaimNext(otherCtx, repository.ClaimWindow{})
	mustNoErr(t, err, "claim for the other tenant")
	if !ok || claimed.ReleaseID != other.ReleaseID {
		t.Fatalf("claimed %+v (ok=%v), want the other tenant's own release %s", claimed, ok, other.ReleaseID)
	}

	// Row-level security keeps each tenant's queue its own.
	listed, err := repo.List(otherCtx, repository.ReleaseFilter{})
	mustNoErr(t, err, "list the other tenant's releases")
	if len(listed) != 1 || listed[0].ReleaseID != other.ReleaseID {
		t.Fatalf("the other tenant sees %d releases, want only its own", len(listed))
	}
}
