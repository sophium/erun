package backendapi

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// The delete state machine's correctness lives in SQL — a conditional claim
// with stale reclaim, a status write that must also clear the previous
// error, a quota count that must exclude mid-teardown rows — so it is
// exercised against a real migrated PostgreSQL rather than a fake that
// agrees with itself. Needs only the database, no cluster. Mirrors
// releaseQueueDatabase's shape (release_queue_sql_e2e_test.go).
func environmentDeleteDatabase(t *testing.T) (*repository.EnvironmentRepository, context.Context, *sql.DB) {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_ENVIRONMENT_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_ENVIRONMENT_DATABASE_URL to a migrated PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	tenantID := seedDeleteTestTenant(t, db)
	ctx := security.WithContext(context.Background(), security.Context{TenantID: tenantID, TenantType: "COMPANY"})
	repo := repository.NewEnvironmentRepository(repository.NewTxManager(db, repository.DialectPostgres))
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM environments WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing the test tenant's environments: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM tenants WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing the test tenant: %v", err)
		}
	})
	return repo, ctx, db
}

func seedDeleteTestTenant(t *testing.T, db *sql.DB) string {
	t.Helper()
	var tenantID string
	err := db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`,
		"env-delete-e2e-"+time.Now().Format("20060102150405.000000"),
	).Scan(&tenantID)
	mustNoErr(t, err, "seed tenant")
	return tenantID
}

// deleteTestEnvironmentSeq gives each seeded environment within a test a
// unique name: environments.environments_tenant_name_key is unique per
// tenant, and several scenarios seed more than one row in the same tenant.
var deleteTestEnvironmentSeq int

func seedDeleteTestEnvironment(t *testing.T, ctx context.Context, repo *repository.EnvironmentRepository, status model.EnvironmentStatus) model.Environment {
	t.Helper()
	deleteTestEnvironmentSeq++
	name := fmt.Sprintf("prod-%d", deleteTestEnvironmentSeq)
	created, err := repo.Create(ctx, model.Environment{Name: name, Type: model.EnvironmentTypeRuntime, RuntimeVersion: "1.0.0"})
	mustNoErr(t, err, "create environment")
	if status != model.EnvironmentStatusRegistered {
		mustNoErr(t, repo.UpdateProvisioningStatus(ctx, created.EnvironmentID, repository.EnvironmentStatusUpdate{Status: string(status)}), "seed status")
	}
	return created
}

// TestClaimDeleteTakesExclusiveOwnership pins the concurrency guard #1140
// needs: a second delete request must not restart a teardown that is already
// in flight, or two concurrent deletes could both launch a Job against the
// same namespace.
func TestClaimDeleteTakesExclusiveOwnership(t *testing.T) {
	repo, ctx, _ := environmentDeleteDatabase(t)
	env := seedDeleteTestEnvironment(t, ctx, repo, model.EnvironmentStatusRunning)

	claimed, err := repo.ClaimDelete(ctx, env.EnvironmentID, time.Hour)
	mustNoErr(t, err, "first claim")
	if !claimed {
		t.Fatal("first ClaimDelete on a running environment should succeed")
	}

	got, err := repo.Get(ctx, env.EnvironmentID)
	mustNoErr(t, err, "get after claim")
	if got.Status != model.EnvironmentStatusDeleting {
		t.Fatalf("status after claim = %q, want %q", got.Status, model.EnvironmentStatusDeleting)
	}

	claimedAgain, err := repo.ClaimDelete(ctx, env.EnvironmentID, time.Hour)
	mustNoErr(t, err, "second claim")
	if claimedAgain {
		t.Fatal("a fresh in-flight delete must not be reclaimable")
	}
}

// TestClaimDeleteReclaimsAStaleAttempt pins the recovery half of the same
// guard: a delete whose claim went stale (a control-plane restart, or a Job
// that ran past its own deadline) must not wedge an environment forever. The
// environments_set_timestamps trigger unconditionally sets updated_at = NOW()
// on every UPDATE (erun-backend-db schema/triggers/timestamps.sql), so a claim
// can't be back-dated directly; a negative staleAfter exercises the identical
// "updated_at < NOW() - MAKE_INTERVAL(secs => staleAfter)" branch by moving the
// staleness threshold into the future instead of the claim into the past.
func TestClaimDeleteReclaimsAStaleAttempt(t *testing.T) {
	repo, ctx, _ := environmentDeleteDatabase(t)
	env := seedDeleteTestEnvironment(t, ctx, repo, model.EnvironmentStatusRunning)

	claimed, err := repo.ClaimDelete(ctx, env.EnvironmentID, time.Hour)
	mustNoErr(t, err, "first claim")
	if !claimed {
		t.Fatal("first claim should succeed")
	}

	reclaimed, err := repo.ClaimDelete(ctx, env.EnvironmentID, -time.Hour)
	mustNoErr(t, err, "reclaim")
	if !reclaimed {
		t.Fatal("a claim stale past staleAfter must be reclaimable")
	}
}

// TestClaimDeleteAlwaysReclaimsABlockedAttempt pins the "no operator
// re-issue needed" half: a previously blocked delete reached a terminal
// outcome, so a fresh attempt (from the operator or the reconciler) must not
// wait out any staleness window. The claim itself must not clear
// delete_error: ClaimDelete's own doc comment records that clearing it on
// claim left the row looking uninformative for as long as the new attempt
// took to reach the same conclusion, so the previous blocker's reason must
// still read back until the new attempt's own outcome overwrites it.
func TestClaimDeleteAlwaysReclaimsABlockedAttempt(t *testing.T) {
	repo, ctx, _ := environmentDeleteDatabase(t)
	env := seedDeleteTestEnvironment(t, ctx, repo, model.EnvironmentStatusRunning)

	const blockedReason = "namespace stuck terminating"
	mustNoErr(t, repo.MarkDeleteBlocked(ctx, env.EnvironmentID, blockedReason), "mark blocked")

	claimed, err := repo.ClaimDelete(ctx, env.EnvironmentID, time.Hour)
	mustNoErr(t, err, "claim after blocked")
	if !claimed {
		t.Fatal("a deletion-blocked environment must always be reclaimable")
	}

	got, err := repo.Get(ctx, env.EnvironmentID)
	mustNoErr(t, err, "get after reclaim")
	if got.Status != model.EnvironmentStatusDeleting {
		t.Fatalf("status after reclaim = %q, want %q", got.Status, model.EnvironmentStatusDeleting)
	}
	if got.DeleteError != blockedReason {
		t.Fatalf("delete_error after reclaim = %q, want %q (unchanged until the new attempt's own outcome)", got.DeleteError, blockedReason)
	}
}

// TestMarkDeleteBlockedRecordsTheReason pins the diagnosis half of #1140: the
// namespace's own blocker must survive as readable state, not just a log line.
func TestMarkDeleteBlockedRecordsTheReason(t *testing.T) {
	repo, ctx, _ := environmentDeleteDatabase(t)
	env := seedDeleteTestEnvironment(t, ctx, repo, model.EnvironmentStatusRunning)

	reason := "NamespaceContentRemaining=True     challenges.acme.cert-manager.io has 1 resource instances"
	mustNoErr(t, repo.MarkDeleteBlocked(ctx, env.EnvironmentID, reason), "mark blocked")

	got, err := repo.Get(ctx, env.EnvironmentID)
	mustNoErr(t, err, "get")
	if got.Status != model.EnvironmentStatusDeletionBlocked {
		t.Fatalf("status = %q, want %q", got.Status, model.EnvironmentStatusDeletionBlocked)
	}
	if got.DeleteError != reason {
		t.Fatalf("delete_error = %q, want %q", got.DeleteError, reason)
	}
}

// TestCountExcludesEnvironmentsMidTeardown pins the quota-accounting half of
// #1140: an environment whose teardown has been requested must not lock the
// tenant out of its own allowance through a delete it cannot complete.
func TestCountExcludesEnvironmentsMidTeardown(t *testing.T) {
	repo, ctx, _ := environmentDeleteDatabase(t)
	_ = seedDeleteTestEnvironment(t, ctx, repo, model.EnvironmentStatusRunning)
	deleting := seedDeleteTestEnvironment(t, ctx, repo, model.EnvironmentStatusRunning)
	blocked := seedDeleteTestEnvironment(t, ctx, repo, model.EnvironmentStatusRunning)

	count, err := repo.Count(ctx)
	mustNoErr(t, err, "count before teardown")
	if count != 3 {
		t.Fatalf("count = %d, want 3 before any teardown", count)
	}

	claimed, err := repo.ClaimDelete(ctx, deleting.EnvironmentID, time.Hour)
	mustNoErr(t, err, "claim delete")
	if !claimed {
		t.Fatal("claim should succeed")
	}
	mustNoErr(t, repo.MarkDeleteBlocked(ctx, blocked.EnvironmentID, "namespace stuck"), "mark blocked")

	count, err = repo.Count(ctx)
	mustNoErr(t, err, "count after teardown requested")
	if count != 1 {
		t.Fatalf("count = %d, want 1 (deleting and deletion-blocked excluded)", count)
	}
}

// TestListByStatusesFindsMidTeardownEnvironments pins the reconciler's own
// read: it must find every environment stuck mid-teardown, regardless of
// which of the two non-terminal statuses it is in.
func TestListByStatusesFindsMidTeardownEnvironments(t *testing.T) {
	repo, ctx, _ := environmentDeleteDatabase(t)
	running := seedDeleteTestEnvironment(t, ctx, repo, model.EnvironmentStatusRunning)
	deleting := seedDeleteTestEnvironment(t, ctx, repo, model.EnvironmentStatusRunning)
	blocked := seedDeleteTestEnvironment(t, ctx, repo, model.EnvironmentStatusRunning)
	_ = running

	claimed, err := repo.ClaimDelete(ctx, deleting.EnvironmentID, time.Hour)
	mustNoErr(t, err, "claim delete")
	if !claimed {
		t.Fatal("claim should succeed")
	}
	mustNoErr(t, repo.MarkDeleteBlocked(ctx, blocked.EnvironmentID, "namespace stuck"), "mark blocked")

	found, err := repo.ListByStatuses(ctx, []model.EnvironmentStatus{model.EnvironmentStatusDeleting, model.EnvironmentStatusDeletionBlocked})
	mustNoErr(t, err, "list by statuses")
	ids := map[string]bool{}
	for _, env := range found {
		ids[env.EnvironmentID] = true
	}
	if !ids[deleting.EnvironmentID] || !ids[blocked.EnvironmentID] {
		t.Fatalf("ListByStatuses = %v, want both %q and %q", ids, deleting.EnvironmentID, blocked.EnvironmentID)
	}
	if ids[running.EnvironmentID] {
		t.Fatalf("ListByStatuses must not include a running environment: %v", ids)
	}
}
