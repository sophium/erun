package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// fakeJobRepo is the in-memory stand-in for JobRepository. Its FindOpenByScope
// resolves a missing scope to repository.ErrNotFound, exactly as the SQL
// repository's normalizeNoRows does -- the distinction between "nobody holds
// this" and "the lookup failed" is the whole reason that sentinel exists.
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
	// The database's timestamp trigger stamps updated_at on insert; the fake
	// stands in for it so the sweep has a real "last heard from" to compare.
	// Tests that need a stale job backdate this field directly.
	created.UpdatedAt = time.Now().UTC()
	stored := created
	f.jobs[created.JobID] = &stored
	f.ordered = append(f.ordered, created.JobID)
	return created, nil
}

// AbandonStale mirrors the SQL sweep: only RUNNING jobs whose updated_at
// predates staleBefore are closed, and closing them pairs the status with the
// ended_at the table's CHECK requires.
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

// backdate moves a stored job's last-update stamp into the past, the way a
// real actor going quiet would.
func (f *fakeJobRepo) backdate(t *testing.T, jobID string, ago time.Duration) {
	t.Helper()
	job, ok := f.jobs[jobID]
	if !ok {
		t.Fatalf("no stored job %q", jobID)
	}
	job.UpdatedAt = time.Now().UTC().Add(-ago)
}

func (f *fakeJobRepo) Get(_ context.Context, jobID string) (model.Job, error) {
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

func (f *fakeJobRepo) Update(_ context.Context, job model.Job) (model.Job, error) {
	if _, ok := f.jobs[job.JobID]; !ok {
		return model.Job{}, repository.ErrNotFound
	}
	stored := job
	f.jobs[job.JobID] = &stored
	return stored, nil
}

// claimFor builds a valid claim for one actor and scope, so each test below
// varies only the field it is actually about.
func claimFor(actorID, scope, summary string) model.Job {
	return model.Job{
		JobType:   model.JobTypeFix,
		Summary:   summary,
		ActorKind: model.ActorKindAgent,
		ActorID:   actorID,
		Scope:     scope,
	}
}

// TestJobServiceClaimDefaultsToRunningAndStampsStartTime: the caller states
// intent; the server states when. A claim with no status is RUNNING, and
// started_at is stamped rather than left for the caller's clock.
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
	// A running job has not ended, and the table's CHECK enforces the pairing.
	if job.EndedAt != nil {
		t.Errorf("endedAt = %v, want nil while RUNNING", job.EndedAt)
	}
}

// TestJobServiceClaimRefusesASecondHolderOfTheSameScope is the coordination
// primitive. The refusal must name the holder, its prose summary, and when it
// started -- that payload is the entire reason this is better than the local
// activity lease's anonymous refusal.
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

// TestJobServiceClaimAllowsASecondClaimOnceTheHolderFinishes: the scope is
// released by finishing the job, not by waiting. Without this, the first
// abandoned-looking job would wedge an issue forever -- the permanently
// orphaned running record nothing ever clears.
func TestJobServiceClaimAllowsASecondClaimOnceTheHolderFinishes(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	ctx := context.Background()

	first, err := svc.Claim(ctx, claimFor("erun/code4", "sophium/erun#2109", "fixing the jobs claim race"))
	if err != nil {
		t.Fatalf("first Claim() error = %v", err)
	}
	if _, err := svc.Update(ctx, first.JobID, model.JobStatusSucceeded, "", ""); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if _, err := svc.Claim(ctx, claimFor("erun/code5", "sophium/erun#2109", "picking the issue up after all")); err != nil {
		t.Fatalf("Claim() after the holder finished error = %v, want nil", err)
	}
}

// TestJobServiceClaimOfNoScopeNeverCollides: a job that claims nothing can
// never collide with another. Work that is not exclusive -- reading, planning,
// investigating -- must not have to invent a unique scope to be recorded.
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

// TestJobServiceClaimPropagatesALookupFailure: only a genuine "nobody holds
// this" may let a claim through. Treating a failed lookup as free would hand
// out a scope that is actually held, which is the one outcome the primitive
// exists to prevent.
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

	// actor_id is the thing an anonymous lease cannot supply, so a job without
	// one is exactly the record the design refuses to create.
	var invalid *InvalidJobInputError
	if !errors.As(err, &invalid) || invalid.Field != "actorId" {
		t.Fatalf("error = %v, want InvalidJobInputError on actorId", err)
	}
}

// TestJobServiceClaimRejectsASummaryThatIsOnlyAShellCommand: the summary
// column is prose. A row reading "git -C /home/erun/git/erun rebase --onto ..."
// describes nothing a reader can act on -- it is the work's mechanics, and
// EnvironmentJob.Command already keeps that detail in the pod.
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

// TestJobServiceClaimAcceptsProseThatNamesACommand: the summary rule refuses a
// pasted command line, not the vocabulary of command names. "erun build is
// failing on main" opens with a command's name and is still a description --
// wrongly refusing it would block a legitimate claim.
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

// TestJobServiceClaimMayRecordWorkThatAlreadyFinished: an actor reporting a
// short job it just completed should not have to claim it RUNNING and
// immediately close it. Claiming straight into a terminal status stamps the
// ended_at that status requires, so the caller never pairs the two itself.
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

// TestJobServiceUpdateClosesARunningJob: the closing write sets the status and
// the ended_at together, so a caller cannot leave a finished job without a
// time it finished at.
func TestJobServiceUpdateClosesARunningJob(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	ctx := context.Background()

	created, err := svc.Claim(ctx, claimFor("erun/code4", "", "fix the jobs claim race"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	updated, err := svc.Update(ctx, created.JobID, model.JobStatusFailed, "", "")
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

// TestJobServiceUpdateRefreshesTheSummaryOnly: a progress refresh is the live
// counterpart of the claim's summary. An update naming no status must not
// close the job.
func TestJobServiceUpdateRefreshesTheSummaryOnly(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	ctx := context.Background()

	created, err := svc.Claim(ctx, claimFor("erun/code4", "", "fix the jobs claim race"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	updated, err := svc.Update(ctx, created.JobID, "", "waiting on the gate build", "")
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

// TestJobServiceUpdateRevalidatesARefreshedSummary: the prose contract belongs
// to the column, not to the write that first populated it. A refresh is as
// capable of pasting a command line in as a claim is.
func TestJobServiceUpdateRevalidatesARefreshedSummary(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	ctx := context.Background()

	created, err := svc.Claim(ctx, claimFor("erun/code4", "", "fix the jobs claim race"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	_, err = svc.Update(ctx, created.JobID, "", "git push --force-with-lease", "")

	var invalid *InvalidJobInputError
	if !errors.As(err, &invalid) || invalid.Field != "summary" {
		t.Fatalf("error = %v, want InvalidJobInputError on summary", err)
	}
}

// TestJobServiceUpdateRefusesAFinishedJob: an outcome is immutable once
// reached, the same discipline that keeps a gate run's verdict append-only
// (ReportOutcome refuses a non-RUNNING run).
func TestJobServiceUpdateRefusesAFinishedJob(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	ctx := context.Background()

	created, err := svc.Claim(ctx, claimFor("erun/code4", "", "fix the jobs claim race"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if _, err := svc.Update(ctx, created.JobID, model.JobStatusSucceeded, "", ""); err != nil {
		t.Fatalf("closing Update() error = %v", err)
	}

	_, err = svc.Update(ctx, created.JobID, model.JobStatusFailed, "", "")

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

// TestJobServiceUpdateRefusesAnUnknownStatus: the closed status vocabulary is
// enforced on the update path too, so a caller cannot close a job into a state
// the queue cannot render. RUNNING is not the interesting case here -- a job
// already running is left running, since the status is only written when it
// actually changes.
func TestJobServiceUpdateRefusesAnUnknownStatus(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	ctx := context.Background()

	created, err := svc.Claim(ctx, claimFor("erun/code4", "", "fix the jobs claim race"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	_, err = svc.Update(ctx, created.JobID, model.JobStatus("cancelled"), "", "")

	var invalid *InvalidJobInputError
	if !errors.As(err, &invalid) || invalid.Field != "status" {
		t.Fatalf("error = %v, want InvalidJobInputError on status", err)
	}
}

// TestJobServiceUpdateOfRunningToRunningIsANoOp: a caller re-sending the state
// it already has must not close the job. The status is written only when it
// actually changes, which is what keeps an idempotent retry from stamping an
// ended_at onto work that is still running.
func TestJobServiceUpdateOfRunningToRunningIsANoOp(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())
	ctx := context.Background()

	created, err := svc.Claim(ctx, claimFor("erun/code4", "", "fix the jobs claim race"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	updated, err := svc.Update(ctx, created.JobID, model.JobStatusRunning, "", "")
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

// TestJobServiceSweepAbandonsAStaleRunningJob: an actor that disappears
// without closing its job is the orphaned running record nothing clears.
// The sweep is what clears it -- and it records the transition rather than
// deleting the row, so the queue still shows what was claimed and dropped.
func TestJobServiceSweepAbandonsAStaleRunningJob(t *testing.T) {
	repo := newFakeJobRepo()
	svc := NewJobService(repo)
	ctx := context.Background()

	created, err := svc.Claim(ctx, claimFor("erun/code4", "sophium/erun#2109", "fix the jobs claim race"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	repo.backdate(t, created.JobID, DefaultJobAbandonTTL+time.Minute)

	abandoned, err := svc.SweepAbandoned(ctx, DefaultJobAbandonTTL)
	if err != nil {
		t.Fatalf("SweepAbandoned() error = %v", err)
	}
	if len(abandoned) != 1 || abandoned[0].JobID != created.JobID {
		t.Fatalf("abandoned = %+v, want exactly %s", abandoned, created.JobID)
	}
	if abandoned[0].Status != model.JobStatusAbandoned {
		t.Errorf("status = %q, want ABANDONED", abandoned[0].Status)
	}
	if abandoned[0].EndedAt == nil {
		t.Error("endedAt = nil; closing a job must record when it stopped")
	}
}

// TestJobServiceSweepReleasesTheAbandonedScope is the sweep's whole purpose:
// the scope a vanished actor held must become claimable again. Without this
// the first abandoned job wedges its issue forever, which is worse than the
// duplicate work the claim primitive exists to prevent.
func TestJobServiceSweepReleasesTheAbandonedScope(t *testing.T) {
	repo := newFakeJobRepo()
	svc := NewJobService(repo)
	ctx := context.Background()

	created, err := svc.Claim(ctx, claimFor("erun/code4", "sophium/erun#2109", "fix the jobs claim race"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	repo.backdate(t, created.JobID, DefaultJobAbandonTTL+time.Minute)

	if _, err := svc.SweepAbandoned(ctx, DefaultJobAbandonTTL); err != nil {
		t.Fatalf("SweepAbandoned() error = %v", err)
	}

	if _, err := svc.Claim(ctx, claimFor("erun/code5", "sophium/erun#2109", "picking the issue up after the sweep")); err != nil {
		t.Fatalf("Claim() after the sweep error = %v, want the scope released", err)
	}
}

// TestJobServiceSweepLeavesAFreshJobAlone: the sweep is bounded by a TTL, not
// by a guess that a quiet job is a dead one. An agent mid-task that has not
// needed to update yet must keep its scope.
func TestJobServiceSweepLeavesAFreshJobAlone(t *testing.T) {
	repo := newFakeJobRepo()
	svc := NewJobService(repo)
	ctx := context.Background()

	created, err := svc.Claim(ctx, claimFor("erun/code4", "sophium/erun#2109", "fix the jobs claim race"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	repo.backdate(t, created.JobID, time.Minute)

	abandoned, err := svc.SweepAbandoned(ctx, DefaultJobAbandonTTL)
	if err != nil {
		t.Fatalf("SweepAbandoned() error = %v", err)
	}
	if len(abandoned) != 0 {
		t.Fatalf("abandoned = %+v, want none; the job is inside its TTL", abandoned)
	}

	job, err := svc.jobs.Get(ctx, created.JobID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if job.Status != model.JobStatusRunning {
		t.Errorf("status = %q, want RUNNING", job.Status)
	}
}

// TestJobServiceSweepLeavesAFinishedJobAlone: only RUNNING jobs are swept. A
// job that already reached an outcome keeps it -- a sweep is not a way to
// rewrite history.
func TestJobServiceSweepLeavesAFinishedJobAlone(t *testing.T) {
	repo := newFakeJobRepo()
	svc := NewJobService(repo)
	ctx := context.Background()

	created, err := svc.Claim(ctx, claimFor("erun/code4", "", "fix the jobs claim race"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if _, err := svc.Update(ctx, created.JobID, model.JobStatusFailed, "", ""); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	repo.backdate(t, created.JobID, 30*DefaultJobAbandonTTL)

	abandoned, err := svc.SweepAbandoned(ctx, DefaultJobAbandonTTL)
	if err != nil {
		t.Fatalf("SweepAbandoned() error = %v", err)
	}
	if len(abandoned) != 0 {
		t.Fatalf("abandoned = %+v, want none; the job already finished", abandoned)
	}

	job, err := svc.jobs.Get(ctx, created.JobID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if job.Status != model.JobStatusFailed {
		t.Errorf("status = %q, want FAILED to be preserved", job.Status)
	}
}

// TestJobServiceSweepOfNothingIsAnEmptyList: a quiet platform is the ordinary
// case, not an error, and the answer must still be a definite empty list
// rather than a nil a caller has to guard.
func TestJobServiceSweepOfNothingIsAnEmptyList(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())

	abandoned, err := svc.SweepAbandoned(context.Background(), DefaultJobAbandonTTL)
	if err != nil {
		t.Fatalf("SweepAbandoned() error = %v", err)
	}
	if abandoned == nil {
		t.Fatal("abandoned = nil, want an empty slice")
	}
	if len(abandoned) != 0 {
		t.Fatalf("abandoned = %+v, want none", abandoned)
	}
}
