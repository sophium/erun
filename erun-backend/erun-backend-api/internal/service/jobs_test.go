package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// fakeJobRepo resolves a missing scope to repository.ErrNotFound, as the SQL repository does.
type fakeJobRepo struct {
	jobs      map[string]*model.Job
	ordered   []string
	nextID    int
	scopeErr  error
	createErr error
}

func newFakeJobRepo() *fakeJobRepo {
	return &fakeJobRepo{jobs: map[string]*model.Job{}}
}

func (f *fakeJobRepo) Create(_ context.Context, job model.Job) (model.Job, error) {
	if f.createErr != nil {
		return model.Job{}, f.createErr
	}
	f.nextID++
	created := job
	created.JobID = "job-" + string(rune('0'+f.nextID))
	// Stands in for the database's updated_at trigger.
	created.UpdatedAt = time.Now().UTC()
	stored := created
	f.jobs[created.JobID] = &stored
	f.ordered = append(f.ordered, created.JobID)
	return created, nil
}

func (f *fakeJobRepo) AbandonStale(_ context.Context, staleBefore time.Time) ([]model.Job, error) {
	abandoned := []model.Job{}
	for _, id := range f.ordered {
		job := f.jobs[id]
		if job.Status != model.JobStatusRunning || !job.UpdatedAt.Before(staleBefore) {
			continue
		}
		ended := time.Now().UTC()
		job.Status = model.JobStatusAbandoned
		job.EndedAt = &ended
		job.UpdatedAt = ended
		abandoned = append(abandoned, *job)
	}
	return abandoned, nil
}

func (f *fakeJobRepo) backdate(t *testing.T, jobID string, ago time.Duration) {
	t.Helper()
	job, ok := f.jobs[jobID]
	if !ok {
		t.Fatalf("no stored job %q", jobID)
	}
	job.UpdatedAt = time.Now().UTC().Add(-ago)
}

func (f *fakeJobRepo) Get(_ context.Context, _ string, jobID string) (model.Job, error) {
	job, ok := f.jobs[jobID]
	if !ok {
		return model.Job{}, repository.ErrNotFound
	}
	return *job, nil
}

func (f *fakeJobRepo) FindOpenByScope(_ context.Context, scope string) (model.Job, error) {
	if f.scopeErr != nil {
		return model.Job{}, f.scopeErr
	}
	// Latest open holder wins, matching the SQL repository's ORDER BY.
	var found *model.Job
	for _, id := range f.ordered {
		job := f.jobs[id]
		if job.Scope == scope && job.IsOpen() {
			found = job
		}
	}
	if found == nil {
		return model.Job{}, repository.ErrNotFound
	}
	return *found, nil
}

func (f *fakeJobRepo) Update(_ context.Context, _ string, job model.Job) (model.Job, error) {
	if _, ok := f.jobs[job.JobID]; !ok {
		return model.Job{}, repository.ErrNotFound
	}
	stored := job
	f.jobs[job.JobID] = &stored
	return stored, nil
}

func claimFor(actorID, scope, summary string) model.Job {
	return model.Job{
		JobType:   model.JobTypeFix,
		Summary:   summary,
		ActorKind: model.ActorKindAgent,
		ActorID:   actorID,
		Scope:     scope,
	}
}

func TestJobServiceClaimDefaultsToRunningAndStampsStartTime(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	before := time.Now().UTC()

	job, err := svc.Claim(context.Background(), claimFor("erun/code4", "", "fix the jobs claim race"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if job.Status != model.JobStatusRunning {
		t.Errorf("status = %q, want RUNNING", job.Status)
	}
	if job.StartedAt.Before(before.Add(-time.Second)) {
		t.Errorf("startedAt = %v, want a server-stamped time at or after %v", job.StartedAt, before)
	}
	if job.EndedAt != nil {
		t.Errorf("endedAt = %v, want nil while RUNNING", job.EndedAt)
	}
}

func TestJobServiceClaimRefusesASecondHolderOfTheSameScope(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	ctx := context.Background()

	first, err := svc.Claim(ctx, claimFor("erun/code4", "sophium/erun#2109", "fixing the jobs claim race"))
	if err != nil {
		t.Fatalf("first Claim() error = %v", err)
	}

	_, err = svc.Claim(ctx, claimFor("erun/code5", "sophium/erun#2109", "also fixing the jobs claim race"))

	var held *JobScopeHeldError
	if !errors.As(err, &held) {
		t.Fatalf("second Claim() error = %v, want *JobScopeHeldError", err)
	}
	if held.Scope != "sophium/erun#2109" {
		t.Errorf("scope = %q, want sophium/erun#2109", held.Scope)
	}
	if held.Holder.JobID != first.JobID {
		t.Errorf("holder jobId = %q, want the first claimant %q", held.Holder.JobID, first.JobID)
	}
	if held.Holder.ActorID != "erun/code4" {
		t.Errorf("holder actorId = %q, want erun/code4", held.Holder.ActorID)
	}
	if held.Holder.Summary != "fixing the jobs claim race" {
		t.Errorf("holder summary = %q, want the first claimant's prose", held.Holder.Summary)
	}
	if held.Holder.StartedAt.IsZero() {
		t.Error("holder startedAt is zero; a refusal naming no start time is unactionable")
	}
	if !errors.Is(err, repository.ErrConflict) {
		t.Errorf("error does not unwrap to ErrConflict: %v", err)
	}
}

func TestJobServiceClaimAllowsASecondClaimOnceTheHolderFinishes(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	ctx := context.Background()

	first, err := svc.Claim(ctx, claimFor("erun/code4", "sophium/erun#2109", "fixing the jobs claim race"))
	if err != nil {
		t.Fatalf("first Claim() error = %v", err)
	}
	if _, err := svc.Update(ctx, first.TenantID, first.JobID, model.JobStatusSucceeded, "", ""); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if _, err := svc.Claim(ctx, claimFor("erun/code5", "sophium/erun#2109", "picking the issue up after all")); err != nil {
		t.Fatalf("Claim() after the holder finished error = %v, want nil", err)
	}
}

func TestJobServiceClaimOfNoScopeNeverCollides(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	ctx := context.Background()

	if _, err := svc.Claim(ctx, claimFor("erun/code4", "", "planning the jobs API")); err != nil {
		t.Fatalf("first unscoped Claim() error = %v", err)
	}
	if _, err := svc.Claim(ctx, claimFor("erun/code5", "", "planning the jobs API too")); err != nil {
		t.Fatalf("second unscoped Claim() error = %v", err)
	}
}

func TestJobServiceClaimPropagatesALookupFailure(t *testing.T) {
	repo := newFakeJobRepo()
	repo.scopeErr = errors.New("connection reset")
	svc := NewJobService(repo)

	_, err := svc.Claim(context.Background(), claimFor("erun/code4", "sophium/erun#2109", "fixing the jobs claim race"))

	var held *JobScopeHeldError
	if errors.As(err, &held) {
		t.Fatal("a failed scope lookup was reported as a held scope")
	}
	if err == nil {
		t.Fatal("claim succeeded despite the scope lookup failing")
	}
}

func TestJobServiceClaimRejectsUnknownJobType(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	job := claimFor("erun/code4", "", "tidy the jobs routes")
	job.JobType = model.JobType("refactor")

	_, err := svc.Claim(context.Background(), job)

	var invalid *InvalidJobInputError
	if !errors.As(err, &invalid) || invalid.Field != "jobType" {
		t.Fatalf("error = %v, want InvalidJobInputError on jobType", err)
	}
}

func TestJobServiceClaimRejectsUnknownActorKind(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	job := claimFor("erun/code4", "", "tidy the jobs routes")
	job.ActorKind = model.ActorKind("robot")

	_, err := svc.Claim(context.Background(), job)

	var invalid *InvalidJobInputError
	if !errors.As(err, &invalid) || invalid.Field != "actorKind" {
		t.Fatalf("error = %v, want InvalidJobInputError on actorKind", err)
	}
}

func TestJobServiceClaimRejectsAMissingActorID(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	job := claimFor("   ", "", "tidy the jobs routes")

	_, err := svc.Claim(context.Background(), job)

	var invalid *InvalidJobInputError
	if !errors.As(err, &invalid) || invalid.Field != "actorId" {
		t.Fatalf("error = %v, want InvalidJobInputError on actorId", err)
	}
}

func TestJobServiceClaimRejectsASummaryThatIsOnlyAShellCommand(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())

	for _, summary := range []string{
		"git -C /home/erun/git/erun rebase --onto main~1",
		"erun build --release",
		"make check",
		"sh -c 'go test ./...'",
	} {
		job := claimFor("erun/code4", "", summary)
		_, err := svc.Claim(context.Background(), job)

		var invalid *InvalidJobInputError
		if !errors.As(err, &invalid) || invalid.Field != "summary" {
			t.Errorf("summary %q: error = %v, want InvalidJobInputError on summary", summary, err)
		}
	}
}

func TestJobServiceClaimAcceptsProseThatNamesACommand(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())

	for _, summary := range []string{
		"erun build is failing on main",
		"git rebase is dropping the jobs migration",
		"make check is red on the console tests",
		"debugging the dtach attach race",
	} {
		job := claimFor("erun/code4", "", summary)
		if _, err := svc.Claim(context.Background(), job); err != nil {
			t.Errorf("summary %q: Claim() error = %v, want nil", summary, err)
		}
	}
}

func TestJobServiceClaimRejectsAnEmptySummary(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	job := claimFor("erun/code4", "", "   ")

	_, err := svc.Claim(context.Background(), job)

	var invalid *InvalidJobInputError
	if !errors.As(err, &invalid) || invalid.Field != "summary" {
		t.Fatalf("error = %v, want InvalidJobInputError on summary", err)
	}
}

func TestJobServiceClaimMayRecordWorkThatAlreadyFinished(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	job := claimFor("erun/code4", "", "reporting a build that already finished")
	job.Status = model.JobStatusSucceeded

	created, err := svc.Claim(context.Background(), job)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if created.Status != model.JobStatusSucceeded {
		t.Errorf("status = %q, want SUCCEEDED", created.Status)
	}
	if created.EndedAt == nil {
		t.Fatal("endedAt = nil on a SUCCEEDED job; the ended_at/status pairing is required")
	}
}

func TestJobServiceUpdateClosesARunningJob(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	ctx := context.Background()

	created, err := svc.Claim(ctx, claimFor("erun/code4", "", "fix the jobs claim race"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	updated, err := svc.Update(ctx, created.TenantID, created.JobID, model.JobStatusFailed, "", "")
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Status != model.JobStatusFailed {
		t.Errorf("status = %q, want FAILED", updated.Status)
	}
	if updated.EndedAt == nil {
		t.Error("endedAt = nil after closing the job")
	}
}

func TestJobServiceUpdateRefreshesTheSummaryOnly(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	ctx := context.Background()

	created, err := svc.Claim(ctx, claimFor("erun/code4", "", "fix the jobs claim race"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	updated, err := svc.Update(ctx, created.TenantID, created.JobID, "", "waiting on the gate build", "")
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Summary != "waiting on the gate build" {
		t.Errorf("summary = %q, want the refreshed prose", updated.Summary)
	}
	if updated.Status != model.JobStatusRunning {
		t.Errorf("status = %q, want RUNNING to be preserved", updated.Status)
	}
	if updated.EndedAt != nil {
		t.Errorf("endedAt = %v, want nil; a summary refresh does not close a job", updated.EndedAt)
	}
}

func TestJobServiceUpdateRevalidatesARefreshedSummary(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	ctx := context.Background()

	created, err := svc.Claim(ctx, claimFor("erun/code4", "", "fix the jobs claim race"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	_, err = svc.Update(ctx, created.TenantID, created.JobID, "", "git push --force-with-lease", "")

	var invalid *InvalidJobInputError
	if !errors.As(err, &invalid) || invalid.Field != "summary" {
		t.Fatalf("error = %v, want InvalidJobInputError on summary", err)
	}
}

func TestJobServiceUpdateRefusesAFinishedJob(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	ctx := context.Background()

	created, err := svc.Claim(ctx, claimFor("erun/code4", "", "fix the jobs claim race"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if _, err := svc.Update(ctx, created.TenantID, created.JobID, model.JobStatusSucceeded, "", ""); err != nil {
		t.Fatalf("closing Update() error = %v", err)
	}

	_, err = svc.Update(ctx, created.TenantID, created.JobID, model.JobStatusFailed, "", "")

	var finished *JobAlreadyFinishedError
	if !errors.As(err, &finished) {
		t.Fatalf("error = %v, want *JobAlreadyFinishedError", err)
	}
	if finished.Status != model.JobStatusSucceeded {
		t.Errorf("reported status = %q, want the status it actually finished as", finished.Status)
	}
	if !errors.Is(err, repository.ErrConflict) {
		t.Errorf("error does not unwrap to ErrConflict: %v", err)
	}
}

func TestJobServiceUpdateRefusesAnUnknownStatus(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	ctx := context.Background()

	created, err := svc.Claim(ctx, claimFor("erun/code4", "", "fix the jobs claim race"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	_, err = svc.Update(ctx, created.TenantID, created.JobID, model.JobStatus("cancelled"), "", "")

	var invalid *InvalidJobInputError
	if !errors.As(err, &invalid) || invalid.Field != "status" {
		t.Fatalf("error = %v, want InvalidJobInputError on status", err)
	}
}

func TestJobServiceUpdateOfRunningToRunningIsANoOp(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	ctx := context.Background()

	created, err := svc.Claim(ctx, claimFor("erun/code4", "", "fix the jobs claim race"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	updated, err := svc.Update(ctx, created.TenantID, created.JobID, model.JobStatusRunning, "", "")
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Status != model.JobStatusRunning {
		t.Errorf("status = %q, want RUNNING", updated.Status)
	}
	if updated.EndedAt != nil {
		t.Errorf("endedAt = %v, want nil; a no-op update must not close the job", updated.EndedAt)
	}
}

func plannedFor(actorID, scope, summary string) model.Job {
	job := claimFor(actorID, scope, summary)
	job.JobType = model.JobTypePlan
	job.Status = model.JobStatusPlanned
	return job
}

func TestJobServiceClaimAcceptsAPlannedJob(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())

	job, err := svc.Claim(context.Background(), plannedFor("erun/ideas", "sophium/erun#2683", "plan the pipeline view"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if job.Status != model.JobStatusPlanned {
		t.Errorf("status = %q, want PLANNED", job.Status)
	}
	if job.EndedAt != nil {
		t.Errorf("endedAt = %v, want none", job.EndedAt)
	}
	if !job.IsOpen() {
		t.Error("IsOpen() = false for a PLANNED job; a plan holds its scope")
	}
}

func TestJobServiceClaimRefusesASecondClaimOfAPlannedScope(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	ctx := context.Background()

	if _, err := svc.Claim(ctx, plannedFor("erun/ideas", "sophium/erun#2683", "plan the pipeline view")); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	_, err := svc.Claim(ctx, claimFor("erun/code3", "sophium/erun#2683", "start coding the pipeline view"))
	var held *JobScopeHeldError
	if !errors.As(err, &held) {
		t.Fatalf("Claim() error = %v, want the planned holder named", err)
	}
	if held.Holder.Status != model.JobStatusPlanned {
		t.Errorf("holder status = %q, want the PLANNED job that holds the scope", held.Holder.Status)
	}
}

func TestJobServiceUpdateOpensAPlannedJob(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	ctx := context.Background()

	created, err := svc.Claim(ctx, plannedFor("erun/code3", "sophium/erun#2683", "plan the pipeline view"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	updated, err := svc.Update(ctx, created.TenantID, created.JobID, model.JobStatusRunning, "coding the pipeline view", "")
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Status != model.JobStatusRunning {
		t.Errorf("status = %q, want RUNNING", updated.Status)
	}
	if updated.EndedAt != nil {
		t.Errorf("endedAt = %v, want none; the job has not stopped", updated.EndedAt)
	}
}

func TestJobServiceUpdateRefusesToReopenARunningJob(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	ctx := context.Background()

	created, err := svc.Claim(ctx, claimFor("erun/code3", "", "coding the pipeline view"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	_, err = svc.Update(ctx, created.TenantID, created.JobID, model.JobStatusPlanned, "", "")
	var invalid *InvalidJobInputError
	if !errors.As(err, &invalid) {
		t.Fatalf("Update() error = %v, want a refusal naming the accepted moves", err)
	}
	if invalid.Field != "status" {
		t.Errorf("refused field = %q, want status", invalid.Field)
	}
}

func TestJobServiceUpdateClosesAPlannedJobWithoutEverRunningIt(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	ctx := context.Background()

	created, err := svc.Claim(ctx, plannedFor("erun/ideas", "", "plan the pipeline view"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	updated, err := svc.Update(ctx, created.TenantID, created.JobID, model.JobStatusSuperseded, "", "")
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Status != model.JobStatusSuperseded {
		t.Errorf("status = %q, want SUPERSEDED", updated.Status)
	}
	if updated.EndedAt == nil {
		t.Error("endedAt = nil; closing a job must record when it stopped")
	}
}
