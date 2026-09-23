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

// backdateJob moves a job's last-update stamp into the past, the way an actor
// going quiet would. It writes through the same connection pool as the
// repository, so the sweep's own transaction sees it.
//
// The timestamp trigger owns updated_at on UPDATE -- erun_set_timestamps sets
// it to NOW() unconditionally, discarding whatever the statement supplied --
// so a plain backdating UPDATE is overwritten before the sweep can read it and
// the stale job the sweep is supposed to abandon is never stale. The trigger
// is disabled for this one write, because the state being set up is the one
// production reaches by an actor simply not writing again, which no UPDATE of
// this test's can imitate while the trigger owns the column. DISABLE TRIGGER
// is catalog-level, so it applies to the pool's connection for the UPDATE
// whichever one that turns out to be.
func backdateJob(t *testing.T, db *sql.DB, jobID string, ago time.Duration) {
	t.Helper()
	_, err := db.Exec(`ALTER TABLE jobs DISABLE TRIGGER jobs_set_timestamps`)
	mustNoErr(t, err, "disable the jobs timestamp trigger")
	defer func() {
		if _, err := db.Exec(`ALTER TABLE jobs ENABLE TRIGGER jobs_set_timestamps`); err != nil {
			t.Fatalf("re-enable the jobs timestamp trigger: %v", err)
		}
	}()

	_, err = db.Exec(`UPDATE jobs SET updated_at = NOW() - $2::interval WHERE job_id = $1`, jobID, ago.String())
	mustNoErr(t, err, "backdate job")
}

// TestJobsSweepAbandonsOnlyStaleRunningJobs is the sweep's SQL contract: it
// closes a RUNNING job nobody has updated inside the TTL, pairs it with the
// ended_at the table's CHECK requires, and touches neither a job still inside
// its TTL nor one that already reached an outcome.
func TestJobsSweepAbandonsOnlyStaleRunningJobs(t *testing.T) {
	repo, db, tenantID := jobsDatabase(t)
	ctx := jobsTenantContext(tenantID)
	svc := service.NewJobService(repo)

	stale := claimJobsTestJob(t, svc, ctx, "scope:stale", "erun/code4", "a job whose actor went quiet")
	fresh := claimJobsTestJob(t, svc, ctx, "scope:fresh", "erun/code4", "a job still being worked")
	finished := claimJobsTestJob(t, svc, ctx, "scope:finished", "erun/code4", "a job that already ended")
	if _, err := svc.Update(ctx, finished.JobID, model.JobStatusSucceeded, "", ""); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	backdateJob(t, db, stale.JobID, 2*service.DefaultJobAbandonTTL)
	backdateJob(t, db, finished.JobID, 2*service.DefaultJobAbandonTTL)

	abandoned, err := svc.SweepAbandoned(ctx, service.DefaultJobAbandonTTL)
	if err != nil {
		t.Fatalf("SweepAbandoned() error = %v", err)
	}

	if len(abandoned) != 1 || abandoned[0].JobID != stale.JobID {
		t.Fatalf("abandoned = %+v, want exactly job %s", abandoned, stale.JobID)
	}
	// The table's CHECK enforces the pairing, so a row that came back without
	// an ended_at could not have been written at all.
	if abandoned[0].EndedAt == nil {
		t.Error("endedAt = nil on an abandoned job")
	}

	stillRunning, err := repo.Get(ctx, fresh.JobID)
	mustNoErr(t, err, "get fresh job")
	if stillRunning.Status != model.JobStatusRunning {
		t.Errorf("fresh job status = %q, want RUNNING", stillRunning.Status)
	}

	stillFinished, err := repo.Get(ctx, finished.JobID)
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
	repo, db, tenantID := jobsDatabase(t)
	ctx := jobsTenantContext(tenantID)
	svc := service.NewJobService(repo)

	first := claimJobsTestJob(t, svc, ctx, "sophium/erun#2109", "erun/code4", "fixing the jobs claim race")
	backdateJob(t, db, first.JobID, 2*service.DefaultJobAbandonTTL)

	if _, err := svc.SweepAbandoned(ctx, service.DefaultJobAbandonTTL); err != nil {
		t.Fatalf("SweepAbandoned() error = %v", err)
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

	// By id: RLS makes another tenant's job invisible, so it resolves to the
	// same not-found a genuinely missing row does.
	if _, err := repo.Get(ownCtx, theirs.JobID); err == nil {
		t.Fatal("Get() of another tenant's job succeeded; the jobs RLS policy is not isolating")
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
	repo, db, tenantID := jobsDatabase(t)
	ownCtx := jobsTenantContext(tenantID)
	svc := service.NewJobService(repo)

	job := claimJobsTestJob(t, svc, ownCtx, "scope:cross-tenant", "erun/code4", "work in a tenant that never sweeps")
	backdateJob(t, db, job.JobID, 2*service.DefaultJobAbandonTTL)

	opsCtx := security.WithContext(context.Background(), security.Context{TenantID: tenantID, TenantType: "OPERATIONS"})
	abandoned, err := svc.SweepAbandoned(opsCtx, service.DefaultJobAbandonTTL)
	if err != nil {
		t.Fatalf("SweepAbandoned() as operations error = %v", err)
	}

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
