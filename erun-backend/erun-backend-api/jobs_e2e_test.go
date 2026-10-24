package backendapi

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/testpg"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

// The jobs table's contracts (status/ended_at CHECK, RLS, timestamp trigger) live
// in SQL, so these tests run against a real migrated PostgreSQL.
func jobsDatabase(t *testing.T) (*repository.JobRepository, *sql.DB, string) {
	t.Helper()
	databaseURL := testpg.URL(t)
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

func jobsTenantContext(tenantID string) context.Context {
	return security.WithContext(context.Background(), security.Context{TenantID: tenantID, TenantType: "COMPANY"})
}

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

// sweepPastEveryStamp ages the threshold, not the row: jobs_set_timestamps
// refreshes updated_at on every UPDATE.
func sweepPastEveryStamp(t *testing.T, repo *repository.JobRepository, ctx context.Context) []model.Job {
	t.Helper()
	abandoned, err := repo.AbandonStale(ctx, time.Now().UTC().Add(time.Hour))
	mustNoErr(t, err, "sweep past every stamp")
	return abandoned
}

func assertAbandoned(t *testing.T, closed map[string]model.Job, jobID string) {
	t.Helper()
	job, ok := closed[jobID]
	if !ok {
		t.Fatalf("abandoned = %+v, want job %s closed", closed, jobID)
	}
	if job.Status != model.JobStatusAbandoned {
		t.Errorf("job %s status = %q, want %q", jobID, job.Status, model.JobStatusAbandoned)
	}
	if job.EndedAt == nil {
		t.Errorf("endedAt = nil on abandoned job %s", jobID)
	}
}

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

	// Being young enough protects a job, not its status.
	abandoned, err := svc.SweepAbandoned(ctx, service.DefaultJobAbandonTTL)
	mustNoErr(t, err, "sweep inside the TTL")
	if len(abandoned) != 0 {
		t.Fatalf("abandoned = %+v, want nothing: every claim here is inside the TTL", abandoned)
	}

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

func TestJobsAreTenantIsolated(t *testing.T) {
	repo, db, tenantID := jobsDatabase(t)
	otherTenantID := seedJobsTestTenant(t, db)

	ownCtx := jobsTenantContext(tenantID)
	otherCtx := jobsTenantContext(otherTenantID)
	svc := service.NewJobService(repo)

	theirs := claimJobsTestJob(t, svc, otherCtx, "scope:theirs", "erun/code9", "work in the other tenant")

	// An OPERATIONS caller's policy is unconditional, so Get's explicit tenant
	// predicate is what isolates it.
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

func TestJobsSweepIsCrossTenant(t *testing.T) {
	repo, _, tenantID := jobsDatabase(t)
	ownCtx := jobsTenantContext(tenantID)
	svc := service.NewJobService(repo)

	job := claimJobsTestJob(t, svc, ownCtx, "scope:cross-tenant", "erun/code4", "work in a tenant that never sweeps")

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

func plannedJobsTestJob(t *testing.T, svc *service.JobService, ctx context.Context, scope, issueRef, actorID, summary string) model.Job {
	t.Helper()
	job, err := svc.Claim(ctx, model.Job{
		JobType:   model.JobTypeTriage,
		IssueRef:  issueRef,
		Summary:   summary,
		Status:    model.JobStatusPlanned,
		ActorKind: model.ActorKindOrchestrator,
		ActorID:   actorID,
		Scope:     scope,
	})
	mustNoErr(t, err, "claim planned job")
	return job
}

// A parked plan is never updated, so including PLANNED in the sweep would
// abandon every plan within one TTL.
func TestJobsSweepNeverAbandonsAPlannedJob(t *testing.T) {
	repo, _, tenantID := jobsDatabase(t)
	ctx := jobsTenantContext(tenantID)
	svc := service.NewJobService(repo)

	planned := plannedJobsTestJob(t, svc, ctx, "sophium/erun#2683", "sophium/erun#2683", "erun/ideas", "plan the pipeline view")
	if planned.Status != model.JobStatusPlanned {
		t.Fatalf("claimed status = %q, want %q", planned.Status, model.JobStatusPlanned)
	}
	if planned.EndedAt != nil {
		t.Errorf("endedAt = %v on a PLANNED job, want none: a plan has not stopped", planned.EndedAt)
	}

	running := claimJobsTestJob(t, svc, ctx, "scope:underway", "erun/code3", "work that is actually underway")

	closed := map[string]model.Job{}
	for _, job := range sweepPastEveryStamp(t, repo, ctx) {
		closed[job.JobID] = job
	}
	if job, ok := closed[planned.JobID]; ok {
		t.Fatalf("the sweep closed a PLANNED job: %+v -- a parked plan has no actor to give up on", job)
	}
	assertAbandoned(t, closed, running.JobID)

	stillPlanned, err := repo.Get(ctx, tenantID, planned.JobID)
	mustNoErr(t, err, "get planned job after the sweep")
	if stillPlanned.Status != model.JobStatusPlanned {
		t.Errorf("status after the sweep = %q, want PLANNED preserved", stillPlanned.Status)
	}
	if stillPlanned.EndedAt != nil {
		t.Errorf("endedAt after the sweep = %v, want none", stillPlanned.EndedAt)
	}
}

func TestJobsPlannedJobHoldsItsScopeAndOpensAgainstRealSQL(t *testing.T) {
	repo, _, tenantID := jobsDatabase(t)
	ctx := jobsTenantContext(tenantID)
	svc := service.NewJobService(repo)

	planned := plannedJobsTestJob(t, svc, ctx, "scope:planned", "sophium/erun#2683", "erun/ideas", "plan the pipeline view")

	_, err := svc.Claim(ctx, model.Job{
		JobType:   model.JobTypeFix,
		Summary:   "start coding the pipeline view",
		ActorKind: model.ActorKindAgent,
		ActorID:   "erun/code3",
		Scope:     "scope:planned",
	})
	var held *service.JobScopeHeldError
	if !errors.As(err, &held) {
		t.Fatalf("Claim() error = %v, want the planned holder named", err)
	}
	if held.Holder.JobID != planned.JobID {
		t.Errorf("holder = %s, want the planned job %s", held.Holder.JobID, planned.JobID)
	}

	opened, err := svc.Update(ctx, tenantID, planned.JobID, model.JobStatusRunning, "coding the pipeline view", "")
	mustNoErr(t, err, "open the plan")
	if opened.Status != model.JobStatusRunning {
		t.Errorf("status = %q, want RUNNING", opened.Status)
	}
	if opened.EndedAt != nil {
		t.Errorf("endedAt = %v, want none after opening a plan", opened.EndedAt)
	}
}
