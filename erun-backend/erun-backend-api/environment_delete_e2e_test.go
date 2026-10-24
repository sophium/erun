package backendapi

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/testpg"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// The delete state machine lives in SQL, so it runs against real PostgreSQL (no cluster).
func environmentDeleteDatabase(t *testing.T) (*repository.EnvironmentRepository, context.Context, *sql.DB) {
	t.Helper()
	databaseURL := testpg.URL(t)
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

// environments_tenant_name_key is unique per tenant.
var deleteTestEnvironmentSeq int

func seedDeleteTestEnvironment(t *testing.T, ctx context.Context, repo *repository.EnvironmentRepository, status model.EnvironmentStatus) model.Environment {
	t.Helper()
	deleteTestEnvironmentSeq++
	name := fmt.Sprintf("prod-%d", deleteTestEnvironmentSeq)
	created, err := repo.Create(ctx, model.Environment{Name: name, Type: model.EnvironmentTypeRuntime, RuntimeVersion: "1.0.0"})
	mustNoErr(t, err, "create environment")
	if status != model.EnvironmentStatusRegistered {
		mustNoErr(t, repo.UpdateProvisioningStatus(ctx, created.TenantID, created.EnvironmentID, repository.EnvironmentStatusUpdate{Status: string(status)}), "seed status")
	}
	return created
}

func TestClaimDeleteTakesExclusiveOwnership(t *testing.T) {
	repo, ctx, _ := environmentDeleteDatabase(t)
	env := seedDeleteTestEnvironment(t, ctx, repo, model.EnvironmentStatusRunning)

	claimed, err := repo.ClaimDelete(ctx, env.TenantID, env.EnvironmentID, time.Hour)
	mustNoErr(t, err, "first claim")
	if !claimed {
		t.Fatal("first ClaimDelete on a running environment should succeed")
	}

	got, err := repo.Get(ctx, env.EnvironmentID)
	mustNoErr(t, err, "get after claim")
	if got.Status != model.EnvironmentStatusDeleting {
		t.Fatalf("status after claim = %q, want %q", got.Status, model.EnvironmentStatusDeleting)
	}

	claimedAgain, err := repo.ClaimDelete(ctx, env.TenantID, env.EnvironmentID, time.Hour)
	mustNoErr(t, err, "second claim")
	if claimedAgain {
		t.Fatal("a fresh in-flight delete must not be reclaimable")
	}
}

// The timestamps trigger forces updated_at = NOW(), so a negative staleAfter
// moves the threshold into the future instead of back-dating the claim.
func TestClaimDeleteReclaimsAStaleAttempt(t *testing.T) {
	repo, ctx, _ := environmentDeleteDatabase(t)
	env := seedDeleteTestEnvironment(t, ctx, repo, model.EnvironmentStatusRunning)

	claimed, err := repo.ClaimDelete(ctx, env.TenantID, env.EnvironmentID, time.Hour)
	mustNoErr(t, err, "first claim")
	if !claimed {
		t.Fatal("first claim should succeed")
	}

	reclaimed, err := repo.ClaimDelete(ctx, env.TenantID, env.EnvironmentID, -time.Hour)
	mustNoErr(t, err, "reclaim")
	if !reclaimed {
		t.Fatal("a claim stale past staleAfter must be reclaimable")
	}
}

// The claim must keep delete_error until the new attempt overwrites it.
func TestClaimDeleteAlwaysReclaimsABlockedAttempt(t *testing.T) {
	repo, ctx, _ := environmentDeleteDatabase(t)
	env := seedDeleteTestEnvironment(t, ctx, repo, model.EnvironmentStatusRunning)

	const blockedReason = "namespace stuck terminating"
	mustNoErr(t, repo.MarkDeleteBlocked(ctx, env.TenantID, env.EnvironmentID, blockedReason), "mark blocked")

	claimed, err := repo.ClaimDelete(ctx, env.TenantID, env.EnvironmentID, time.Hour)
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

func TestMarkDeleteBlockedRecordsTheReason(t *testing.T) {
	repo, ctx, _ := environmentDeleteDatabase(t)
	env := seedDeleteTestEnvironment(t, ctx, repo, model.EnvironmentStatusRunning)

	reason := "NamespaceContentRemaining=True     challenges.acme.cert-manager.io has 1 resource instances"
	mustNoErr(t, repo.MarkDeleteBlocked(ctx, env.TenantID, env.EnvironmentID, reason), "mark blocked")

	got, err := repo.Get(ctx, env.EnvironmentID)
	mustNoErr(t, err, "get")
	if got.Status != model.EnvironmentStatusDeletionBlocked {
		t.Fatalf("status = %q, want %q", got.Status, model.EnvironmentStatusDeletionBlocked)
	}
	if got.DeleteError != reason {
		t.Fatalf("delete_error = %q, want %q", got.DeleteError, reason)
	}
}

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

	claimed, err := repo.ClaimDelete(ctx, deleting.TenantID, deleting.EnvironmentID, time.Hour)
	mustNoErr(t, err, "claim delete")
	if !claimed {
		t.Fatal("claim should succeed")
	}
	mustNoErr(t, repo.MarkDeleteBlocked(ctx, blocked.TenantID, blocked.EnvironmentID, "namespace stuck"), "mark blocked")

	count, err = repo.Count(ctx)
	mustNoErr(t, err, "count after teardown requested")
	if count != 1 {
		t.Fatalf("count = %d, want 1 (deleting and deletion-blocked excluded)", count)
	}
}

func TestListByStatusesFindsMidTeardownEnvironments(t *testing.T) {
	repo, ctx, _ := environmentDeleteDatabase(t)
	running := seedDeleteTestEnvironment(t, ctx, repo, model.EnvironmentStatusRunning)
	deleting := seedDeleteTestEnvironment(t, ctx, repo, model.EnvironmentStatusRunning)
	blocked := seedDeleteTestEnvironment(t, ctx, repo, model.EnvironmentStatusRunning)
	_ = running

	claimed, err := repo.ClaimDelete(ctx, deleting.TenantID, deleting.EnvironmentID, time.Hour)
	mustNoErr(t, err, "claim delete")
	if !claimed {
		t.Fatal("claim should succeed")
	}
	mustNoErr(t, repo.MarkDeleteBlocked(ctx, blocked.TenantID, blocked.EnvironmentID, "namespace stuck"), "mark blocked")

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
