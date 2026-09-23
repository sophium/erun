package backendapi

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

// The jobs table's own contracts live in SQL — a CHECK that pairs status with
// ended_at, a tenant-scoped RLS policy, a timestamp trigger that maintains the
// updated_at the sweep compares against — so they are exercised against a real
// migrated PostgreSQL rather than a fake that agrees with itself. Needs only
// the database, no cluster. Mirrors environmentDeleteDatabase's shape
// (environment_delete_e2e_test.go).
func jobsDatabase(t *testing.T) (*repository.JobRepository, *sql.DB, string) {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_JOBS_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_JOBS_DATABASE_URL to a migrated PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	tenantID := seedJobsTestTenant(t, db)
	repo := repository.NewJobRepository(repository.NewTxManager(db, repository.DialectPostgres))
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM jobs WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing the test tenant's jobs: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM tenants WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing the test tenant: %v", err)
		}
	})
	return repo, db, tenantID
}

func seedJobsTestTenant(t *testing.T, db *sql.DB) string {
	t.Helper()
	var tenantID string
	err := db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`,
		"jobs-e2e-"+time.Now().Format("20060102150405.000000"),
	).Scan(&tenantID)
	mustNoErr(t, err, "seed tenant")
	return tenantID
}

// jobsTenantContext is the ordinary tenant-scoped session: the erun_tenant
// role, isolated to one tenant by the jobs RLS policy.
func jobsTenantContext(tenantID string) context.Context {
	return security.WithContext(context.Background(), security.Context{TenantID: tenantID, TenantType: "COMPANY"})
}

// claimJobsTestJob claims one job through the service, so the row under test
// is the one the API would actually have written.
func claimJobsTestJob(t *testing.T, svc *service.JobService, ctx context.Context, scope, actorID, summary string) model.Job {
	t.Helper()
	job, err := svc.Claim(ctx, model.Job{
		JobType:   model.JobTypeFix,
		Summary:   summary,
		ActorKind: model.ActorKindAgent,
		ActorID:   actorID,
		Scope:     scope,
	})
	mustNoErr(t, err, "claim job")
	return job
}

// sweepPastEveryStamp closes every RUNNING job in the caller's tenant, by
// asking the sweep for everything older than a threshold an hour from now.
//
// The threshold is what moves, never the row. jobs_set_timestamps
// (erun-backend-db/schema/triggers/timestamps.sql) stamps updated_at = NOW()
// on every UPDATE, so a job cannot be aged by writing to it — the write that
// would age it is the same write that refreshes it. This test used to
// back-date the row directly, which against a real migrated PostgreSQL set
// updated_at to NOW() and left the sweep with nothing to find.
func sweepPastEveryStamp(t *testing.T, repo *repository.JobRepository, ctx context.Context) []model.Job {
	t.Helper()
	abandoned, err := repo.AbandonStale(ctx, time.Now().UTC().Add(time.Hour))
	mustNoErr(t, err, "sweep past every stamp")
	return abandoned
}

// assertAbandoned holds one swept row to what the sweep promises about it.
func assertAbandoned(t *testing.T, closed map[string]model.Job, jobID string) {
	t.Helper()
	job, ok := closed[jobID]
	if !ok {
		t.Fatalf("abandoned = %+v, want job %s closed", closed, jobID)
	}
	if job.Status != model.JobStatusAbandoned {
		t.Errorf("job %s status = %q, want %q", jobID, job.Status, model.JobStatusAbandoned)
	}
	// The table's CHECK enforces the pairing, so a row that came back without
	// an ended_at could not have been written at all.
	if job.EndedAt == nil {
		t.Errorf("endedAt = nil on abandoned job %s", jobID)
	}
}

// TestJobsSweepAbandonsOnlyStaleRunningJobs is the sweep's SQL contract: it
// closes a RUNNING job whose last update predates the threshold, pairs it with
// the ended_at the table's CHECK requires, and touches neither a job inside the
// threshold nor one that already reached an outcome.
//
// Both sides of that boundary are pinned by moving the threshold, because the
// row cannot be moved: the first sweep runs at the production TTL every claim
// is inside, and the second runs past every stamp there is.
func TestJobsSweepAbandonsOnlyStaleRunningJobs(t *testing.T) {
	repo, _, tenantID := jobsDatabase(t)
	ctx := jobsTenantContext(tenantID)
	svc := service.NewJobService(repo)

	stale := claimJobsTestJob(t, svc, ctx, "scope:stale", "erun/code4", "a job whose actor went quiet")
	fresh := claimJobsTestJob(t, svc, ctx, "scope:fresh", "erun/code4", "a job still being worked")
	finished := claimJobsTestJob(t, svc, ctx, "scope:finished", "erun/code4", "a job that already ended")
	if _, err := svc.Update(ctx, tenantID, finished.JobID, model.JobStatusSucceeded, "", ""); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Every claim is well inside the TTL, so the production sweep closes
	// nothing: being young enough is what protects a job, not its status.
	abandoned, err := svc.SweepAbandoned(ctx, service.DefaultJobAbandonTTL)
	mustNoErr(t, err, "sweep inside the TTL")
	if len(abandoned) != 0 {
		t.Fatalf("abandoned = %+v, want nothing: every claim here is inside the TTL", abandoned)
	}

	// Past every stamp, both RUNNING jobs are stale. The finished one is not
	// RUNNING, so the same sweep must leave it alone — which is the "only" in
	// this test's name, observable in one call.
	closed := map[string]model.Job{}
	for _, job := range sweepPastEveryStamp(t, repo, ctx) {
		closed[job.JobID] = job
	}
	if len(closed) != 2 {
		t.Fatalf("abandoned = %+v, want exactly %s and %s", closed, stale.JobID, fresh.JobID)
	}
	for _, want := range []string{stale.JobID, fresh.JobID} {
		assertAbandoned(t, closed, want)
	}
	if job, ok := closed[finished.JobID]; ok {
		t.Fatalf("the sweep closed a job that already reached an outcome: %+v", job)
	}

	stillFinished, err := repo.Get(ctx, tenantID, finished.JobID)
	mustNoErr(t, err, "get finished job")
	if stillFinished.Status != model.JobStatusSucceeded {
		t.Errorf("finished job status = %q, want SUCCEEDED preserved", stillFinished.Status)
	}
}

// TestJobsSweepReleasesTheAbandonedScope is the payoff: the scope a vanished
// actor held becomes claimable again, so an abandoned job cannot wedge an
// issue forever. Everything here is real SQL — the partial read the claim
// makes, the status the sweep writes, and the read that follows it.
func TestJobsSweepReleasesTheAbandonedScope(t *testing.T) {
	repo, _, tenantID := jobsDatabase(t)
	ctx := jobsTenantContext(tenantID)
	svc := service.NewJobService(repo)

	first := claimJobsTestJob(t, svc, ctx, "sophium/erun#2109", "erun/code4", "fixing the jobs claim race")

	abandoned := sweepPastEveryStamp(t, repo, ctx)
	if len(abandoned) != 1 || abandoned[0].JobID != first.JobID {
		t.Fatalf("abandoned = %+v, want exactly job %s", abandoned, first.JobID)
	}

	second := claimJobsTestJob(t, svc, ctx, "sophium/erun#2109", "erun/code5", "picking the issue up after the sweep")
	if second.JobID == "" {
		t.Fatal("second claim returned no job id")
	}
}

// TestJobsAreTenantIsolated pins the RLS policy the issue's validation names:
// an ordinary tenant session cannot read another tenant's jobs, whether it
// lists or asks for one by id.
func TestJobsAreTenantIsolated(t *testing.T) {
	repo, db, tenantID := jobsDatabase(t)
	otherTenantID := seedJobsTestTenant(t, db)

	ownCtx := jobsTenantContext(tenantID)
	otherCtx := jobsTenantContext(otherTenantID)
	svc := service.NewJobService(repo)

	theirs := claimJobsTestJob(t, svc, otherCtx, "scope:theirs", "erun/code9", "work in the other tenant")

	// By id: another tenant's job resolves to the same not-found a genuinely
	// missing row does. This holds for a COMPANY caller through RLS and for an
	// OPERATIONS caller, whose policy is unconditional, through the explicit
	// tenant predicate Get now carries — so the caller's own tenant is what is
	// named here, not the row's.
	if _, err := repo.Get(ownCtx, tenantID, theirs.JobID); err == nil {
		t.Fatal("Get() of another tenant's job succeeded; tenant isolation is not holding")
	}

	listed, err := repo.List(ownCtx, repository.JobFilter{})
	mustNoErr(t, err, "list own jobs")
	for _, job := range listed {
		if job.JobID == theirs.JobID {
			t.Fatal("List() returned another tenant's job")
		}
	}
}

// TestJobsSweepIsCrossTenant: the sweep runs under the operations role and
// closes stale jobs in every tenant at once, so an abandoned job in a tenant
// that never asks for it is still cleared. Without this the orphaned running
// record the design exists to prevent would simply outlive its tenant's
// attention.
func TestJobsSweepIsCrossTenant(t *testing.T) {
	repo, _, tenantID := jobsDatabase(t)
	ownCtx := jobsTenantContext(tenantID)
	svc := service.NewJobService(repo)

	job := claimJobsTestJob(t, svc, ownCtx, "scope:cross-tenant", "erun/code4", "work in a tenant that never sweeps")

	// The sweep itself runs under the operations role, which is the thing
	// under test: with no tenant of its own it still reaches this one's rows.
	opsCtx := security.WithContext(context.Background(), security.Context{TenantID: tenantID, TenantType: "OPERATIONS"})
	abandoned := sweepPastEveryStamp(t, repo, opsCtx)

	var found bool
	for _, candidate := range abandoned {
		if candidate.JobID == job.JobID {
			found = true
		}
	}
	if !found {
		t.Fatalf("the cross-tenant sweep did not close job %s (closed %d jobs)", job.JobID, len(abandoned))
	}
}
