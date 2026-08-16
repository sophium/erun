package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/releaseexec"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// fakeReleaseQueue models the queue table's contracts in memory: one row per
// commit, at most one running row, and FIFO by insertion. The invariants it
// holds are the ones PostgreSQL enforces, so a service behaviour that depends on
// them is exercised here and the SQL that enforces them is exercised against a
// real database in the release-queue end-to-end gate.
type fakeReleaseQueue struct {
	rows   []*model.Release
	nextID int
}

func (q *fakeReleaseQueue) Create(_ context.Context, release model.Release) (model.Release, error) {
	for _, row := range q.rows {
		if row.CommitID == release.CommitID {
			return model.Release{}, errors.New("duplicate key value violates releases_tenant_commit_key")
		}
	}
	q.nextID++
	row := release
	row.ReleaseID = string(rune('a'+q.nextID-1)) + "-release"
	row.Status = model.ReleaseStatusQueued
	row.Attempt = 1
	q.rows = append(q.rows, &row)
	return row, nil
}

func (q *fakeReleaseQueue) Get(_ context.Context, releaseID string) (model.Release, error) {
	for _, row := range q.rows {
		if row.ReleaseID == releaseID {
			return *row, nil
		}
	}
	return model.Release{}, repository.ErrNotFound
}

func (q *fakeReleaseQueue) FindByCommit(_ context.Context, commitID string) (model.Release, error) {
	for _, row := range q.rows {
		if row.CommitID == commitID {
			return *row, nil
		}
	}
	return model.Release{}, repository.ErrNotFound
}

func (q *fakeReleaseQueue) ClaimNext(_ context.Context, _ repository.ClaimWindow) (model.Release, bool, error) {
	for _, row := range q.rows {
		if row.Status == model.ReleaseStatusRunning {
			return model.Release{}, false, nil
		}
	}
	for _, row := range q.rows {
		if row.Status == model.ReleaseStatusQueued {
			row.Status = model.ReleaseStatusRunning
			row.FailureReason = ""
			return *row, true, nil
		}
	}
	return model.Release{}, false, nil
}

func (q *fakeReleaseQueue) Requeue(_ context.Context, releaseID string) (model.Release, error) {
	for _, row := range q.rows {
		if row.ReleaseID == releaseID && row.Status == model.ReleaseStatusFailed {
			row.Status = model.ReleaseStatusQueued
			row.Attempt++
			row.FailureReason = ""
			return *row, nil
		}
	}
	return model.Release{}, repository.ErrNotFound
}

func (q *fakeReleaseQueue) RecordOutcome(_ context.Context, releaseID string, outcome repository.ReleaseOutcome) error {
	for _, row := range q.rows {
		if row.ReleaseID != releaseID {
			continue
		}
		row.Status = outcome.Status
		if outcome.Version != "" {
			row.Version = outcome.Version
		}
		if outcome.BuildID != "" {
			row.BuildID = outcome.BuildID
		}
		row.FailureReason = outcome.FailureReason
		return nil
	}
	return repository.ErrNotFound
}

func (q *fakeReleaseQueue) ExpireStale(context.Context, time.Duration, string) (int, error) {
	return 0, nil
}

func (q *fakeReleaseQueue) statuses() []string {
	statuses := make([]string, 0, len(q.rows))
	for _, row := range q.rows {
		statuses = append(statuses, string(row.Status))
	}
	return statuses
}

type recordingBuilds struct {
	created []model.Build
	err     error
}

func (b *recordingBuilds) Create(_ context.Context, build model.Build) (model.Build, error) {
	if b.err != nil {
		return model.Build{}, b.err
	}
	build.BuildID = "build-1"
	b.created = append(b.created, build)
	return build, nil
}

type fakeReleaseRunner struct {
	result releaseexec.Result
	err    error
	runs   int
}

func (r *fakeReleaseRunner) Run(context.Context, releaseexec.ReleaseJobParams) (releaseexec.Result, error) {
	r.runs++
	return r.result, r.err
}

func enqueue(t *testing.T, s *ReleaseService, commit string) EnqueueResult {
	t.Helper()
	result, err := s.Enqueue(context.Background(), ReleaseRequest{
		ReviewID:     "review-1",
		TargetBranch: "main",
		CommitID:     commit,
	})
	if err != nil {
		t.Fatalf("enqueue %s: %v", commit, err)
	}
	return result
}

func TestEnqueueQueuesANewCommit(t *testing.T) {
	queue := &fakeReleaseQueue{}
	service := NewReleaseService(queue, &recordingBuilds{}, &fakeReleaseRunner{})

	result := enqueue(t, service, "commit-a")
	if !result.Created {
		t.Fatal("a commit with no release was not queued")
	}
	if result.Release.Status != model.ReleaseStatusQueued {
		t.Fatalf("status = %q, want queued", result.Release.Status)
	}
	if len(queue.rows) != 1 {
		t.Fatalf("rows = %d, want exactly one", len(queue.rows))
	}
}

func TestEnqueueRejectsARequestWithNothingToRelease(t *testing.T) {
	service := NewReleaseService(&fakeReleaseQueue{}, &recordingBuilds{}, &fakeReleaseRunner{})
	for _, request := range []ReleaseRequest{
		{TargetBranch: "main"},
		{CommitID: "commit-a"},
	} {
		if _, err := service.Enqueue(context.Background(), request); !errors.Is(err, repository.ErrInvalidInput) {
			t.Fatalf("enqueue(%+v) error = %v, want invalid input", request, err)
		}
	}
}

// TestEnqueueMintsNothingForAnAlreadyReleasedCommit is the worst failure this
// queue could have: two versions for one merge commit. The already-released row
// is the answer, and no second row appears.
func TestEnqueueMintsNothingForAnAlreadyReleasedCommit(t *testing.T) {
	queue := &fakeReleaseQueue{}
	service := NewReleaseService(queue, &recordingBuilds{}, &fakeReleaseRunner{})
	first := enqueue(t, service, "commit-a")
	mustNoError(t, queue.RecordOutcome(context.Background(), first.Release.ReleaseID, repository.ReleaseOutcome{
		Status:  model.ReleaseStatusReleased,
		Version: "1.0.150",
	}))

	again := enqueue(t, service, "commit-a")
	if !again.AlreadyReleased {
		t.Fatal("re-triggering a released commit did not report it as already released")
	}
	if again.Created || again.Requeued {
		t.Fatalf("re-triggering a released commit queued work: %+v", again)
	}
	if again.Release.Version != "1.0.150" {
		t.Fatalf("version = %q, want the version already minted for this commit", again.Release.Version)
	}
	if len(queue.rows) != 1 {
		t.Fatalf("rows = %d, want one release for one commit", len(queue.rows))
	}
	if queue.rows[0].Attempt != 1 {
		t.Fatalf("attempt = %d, want the released row untouched", queue.rows[0].Attempt)
	}
}

// TestEnqueueRequeuesAFailedCommit: a transient failure must not lock a commit
// out of ever being released, and the retry has to be a new attempt so its Job
// and workflow run rather than replay.
func TestEnqueueRequeuesAFailedCommit(t *testing.T) {
	queue := &fakeReleaseQueue{}
	service := NewReleaseService(queue, &recordingBuilds{}, &fakeReleaseRunner{})
	first := enqueue(t, service, "commit-a")
	mustNoError(t, queue.RecordOutcome(context.Background(), first.Release.ReleaseID, repository.ReleaseOutcome{
		Status:        model.ReleaseStatusFailed,
		FailureReason: "the registry rejected the push",
	}))

	again := enqueue(t, service, "commit-a")
	if !again.Requeued {
		t.Fatalf("re-triggering a failed commit did not requeue it: %+v", again)
	}
	if again.Release.Attempt != 2 {
		t.Fatalf("attempt = %d, want a second attempt", again.Release.Attempt)
	}
	if again.Release.Status != model.ReleaseStatusQueued {
		t.Fatalf("status = %q, want queued", again.Release.Status)
	}
	if again.Release.FailureReason != "" {
		t.Fatalf("requeued release still carries the old reason %q", again.Release.FailureReason)
	}
	if len(queue.rows) != 1 {
		t.Fatalf("rows = %d, want the same row requeued, not a second one", len(queue.rows))
	}
}

func TestEnqueueReturnsTheInFlightReleaseForACommit(t *testing.T) {
	queue := &fakeReleaseQueue{}
	service := NewReleaseService(queue, &recordingBuilds{}, &fakeReleaseRunner{})
	first := enqueue(t, service, "commit-a")

	again := enqueue(t, service, "commit-a")
	if again.Created || again.Requeued || again.AlreadyReleased {
		t.Fatalf("re-triggering a queued commit did something: %+v", again)
	}
	if again.Release.ReleaseID != first.Release.ReleaseID {
		t.Fatalf("release id = %q, want the row already queued %q", again.Release.ReleaseID, first.Release.ReleaseID)
	}
}

// TestRunRecordsTheVersionAndTheBuild: the release mints the version inside the
// Job, so the row and the review's build are the only records of what was
// published.
func TestRunRecordsTheVersionAndTheBuild(t *testing.T) {
	queue := &fakeReleaseQueue{}
	builds := &recordingBuilds{}
	runner := &fakeReleaseRunner{result: releaseexec.Result{Outcome: releaseexec.OutcomeSucceeded, Version: "1.0.150"}}
	service := NewReleaseService(queue, builds, runner)
	release := enqueue(t, service, "commit-a").Release

	if err := service.Run(context.Background(), release, releaseexec.ReleaseJobParams{}); err != nil {
		t.Fatalf("run: %v", err)
	}

	row := mustGet(t, queue, release.ReleaseID)
	if row.Status != model.ReleaseStatusReleased {
		t.Fatalf("status = %q, want released", row.Status)
	}
	if row.Version != "1.0.150" {
		t.Fatalf("version = %q, want the version the run published", row.Version)
	}
	if row.BuildID != "build-1" {
		t.Fatalf("buildId = %q, want the recorded build", row.BuildID)
	}
	if len(builds.created) != 1 {
		t.Fatalf("builds recorded = %d, want one", len(builds.created))
	}
	build := builds.created[0]
	if build.ReviewID != "review-1" || build.CommitID != "commit-a" || build.Version != "1.0.150" || !build.Successful {
		t.Fatalf("recorded build = %+v, want the review, commit, version and success of this release", build)
	}
}

// TestRunRecordsTheReleasesOwnFailure: recording that a Job exited names nothing
// an operator can act on, so the release's own account has to survive into the
// row.
func TestRunRecordsTheReleasesOwnFailure(t *testing.T) {
	const detail = "release: refusing to release: origin/main moved to 1a2b3c4 while the build was in flight"
	queue := &fakeReleaseQueue{}
	runner := &fakeReleaseRunner{result: releaseexec.Result{Outcome: releaseexec.OutcomeFailed, Failure: detail}}
	service := NewReleaseService(queue, &recordingBuilds{}, runner)
	release := enqueue(t, service, "commit-a").Release

	if err := service.Run(context.Background(), release, releaseexec.ReleaseJobParams{TargetBranch: "main", CommitID: "commit-a"}); err == nil {
		t.Fatal("expected the failed release to surface an error")
	}

	row := mustGet(t, queue, release.ReleaseID)
	if row.Status != model.ReleaseStatusFailed {
		t.Fatalf("status = %q, want failed", row.Status)
	}
	if !strings.Contains(row.FailureReason, detail) {
		t.Fatalf("failureReason = %q, want the release's own account", row.FailureReason)
	}
	if row.Version != "" {
		t.Fatalf("version = %q, want empty: a failed release published nothing", row.Version)
	}
}

// TestRunSaysWhereToLookWhenTheJobLeftNothing: a reason of last resort still has
// to be actionable, so it names the Job an operator can read for themselves.
func TestRunSaysWhereToLookWhenTheJobLeftNothing(t *testing.T) {
	queue := &fakeReleaseQueue{}
	runner := &fakeReleaseRunner{result: releaseexec.Result{Outcome: releaseexec.OutcomeFailed}}
	service := NewReleaseService(queue, &recordingBuilds{}, runner)
	release := enqueue(t, service, "commit-a").Release

	params := releaseexec.ReleaseJobParams{Tenant: "acme", TargetBranch: "main", CommitID: "commit-a", ReleaseID: release.ReleaseID, Attempt: 1, Namespace: "acme-devops"}
	if err := service.Run(context.Background(), release, params); err == nil {
		t.Fatal("expected the failed release to surface an error")
	}
	reason := mustGet(t, queue, release.ReleaseID).FailureReason
	for _, want := range []string{"acme-devops", releaseexec.ReleaseJobName("acme", "main", release.ReleaseID, 1)} {
		if !strings.Contains(reason, want) {
			t.Fatalf("failureReason = %q, want it to mention %q", reason, want)
		}
	}
}

// TestRunRefusesASuccessThatNamesNoVersion: a run that exits 0 without saying
// what it published leaves nothing able to name the version, which is not a
// success the control plane can record.
func TestRunRefusesASuccessThatNamesNoVersion(t *testing.T) {
	queue := &fakeReleaseQueue{}
	builds := &recordingBuilds{}
	runner := &fakeReleaseRunner{result: releaseexec.Result{Outcome: releaseexec.OutcomeSucceeded}}
	service := NewReleaseService(queue, builds, runner)
	release := enqueue(t, service, "commit-a").Release

	if err := service.Run(context.Background(), release, releaseexec.ReleaseJobParams{}); err == nil {
		t.Fatal("expected a version-less success to surface an error")
	}
	row := mustGet(t, queue, release.ReleaseID)
	if row.Status != model.ReleaseStatusFailed {
		t.Fatalf("status = %q, want failed", row.Status)
	}
	if len(builds.created) != 0 {
		t.Fatalf("a build was recorded for a release that named no version: %+v", builds.created)
	}
}

func TestRunFailsWithoutAnExecutor(t *testing.T) {
	queue := &fakeReleaseQueue{}
	service := NewReleaseService(queue, &recordingBuilds{}, nil)
	release := enqueue(t, service, "commit-a").Release

	if err := service.Run(context.Background(), release, releaseexec.ReleaseJobParams{}); err == nil {
		t.Fatal("expected a release with no executor to surface an error")
	}
	if got := mustGet(t, queue, release.ReleaseID).Status; got != model.ReleaseStatusFailed {
		t.Fatalf("status = %q, want failed rather than a row stuck running", got)
	}
}

// TestDispatchRunsOneReleaseAtATime is the serialisation contract. Two commits
// queued close together must produce two sequential releases: `erun release`
// bumps a semver, writes version-bearing files, tags and pushes, so two
// concurrent runs on one version line corrupt it.
func TestDispatchRunsOneReleaseAtATime(t *testing.T) {
	queue := &fakeReleaseQueue{}
	service := NewReleaseService(queue, &recordingBuilds{}, &fakeReleaseRunner{})
	first := enqueue(t, service, "commit-a").Release
	enqueue(t, service, "commit-b")

	var started []string
	collect := func(release model.Release) error {
		started = append(started, release.ReleaseID)
		return nil
	}

	count, err := service.Dispatch(context.Background(), collect)
	mustNoError(t, err)
	if count != 1 || len(started) != 1 || started[0] != first.ReleaseID {
		t.Fatalf("first dispatch started %v (count %d), want only the head of the queue", started, count)
	}
	if got := queue.statuses(); got[1] != string(model.ReleaseStatusQueued) {
		t.Fatalf("second release status = %q, want it still queued behind the first", got[1])
	}

	// A second dispatch while the first is in flight must start nothing.
	count, err = service.Dispatch(context.Background(), collect)
	mustNoError(t, err)
	if count != 0 {
		t.Fatalf("dispatch started %d releases while one was in flight, want 0", count)
	}

	// Once the first finishes, the second runs — sequentially, not concurrently.
	mustNoError(t, queue.RecordOutcome(context.Background(), first.ReleaseID, repository.ReleaseOutcome{
		Status: model.ReleaseStatusReleased, Version: "1.0.150",
	}))
	count, err = service.Dispatch(context.Background(), collect)
	mustNoError(t, err)
	if count != 1 || len(started) != 2 {
		t.Fatalf("dispatch after the first finished started %v (count %d), want the next one", started, count)
	}
}

// TestDispatchRunsTheNextReleaseAfterAFailure: a failed release must not wedge
// the queue behind it.
func TestDispatchRunsTheNextReleaseAfterAFailure(t *testing.T) {
	queue := &fakeReleaseQueue{}
	runner := &fakeReleaseRunner{result: releaseexec.Result{Outcome: releaseexec.OutcomeFailed, Failure: "the build did not pass"}}
	service := NewReleaseService(queue, &recordingBuilds{}, runner)
	first := enqueue(t, service, "commit-a").Release
	second := enqueue(t, service, "commit-b").Release

	var started []string
	collect := func(release model.Release) error {
		started = append(started, release.ReleaseID)
		return nil
	}
	if _, err := service.Dispatch(context.Background(), collect); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := service.Run(context.Background(), first, releaseexec.ReleaseJobParams{}); err == nil {
		t.Fatal("expected the first release to fail")
	}

	count, err := service.Dispatch(context.Background(), collect)
	mustNoError(t, err)
	if count != 1 || started[len(started)-1] != second.ReleaseID {
		t.Fatalf("after a failure the queue started %v (count %d), want the next release", started, count)
	}
	if got := mustGet(t, queue, first.ReleaseID); got.Status != model.ReleaseStatusFailed || got.FailureReason == "" {
		t.Fatalf("failed release = %+v, want a failed row carrying its reason", got)
	}
}

// TestDispatchStopsAtItsCap bounds the fan-out, so a queue full of work cannot
// launch an unbounded number of Jobs in one pass.
func TestDispatchStopsAtItsCap(t *testing.T) {
	// A claim that always succeeds stands in for many tenants each allowed one
	// in-flight release; without the cap this pass would never stop.
	service := NewReleaseService(&alwaysClaimQueue{}, &recordingBuilds{}, &fakeReleaseRunner{})

	started := 0
	count, err := service.Dispatch(context.Background(), func(model.Release) error {
		started++
		return nil
	})
	mustNoError(t, err)
	if count != maxDispatchPerPass || started != maxDispatchPerPass {
		t.Fatalf("dispatch started %d (reported %d), want the cap of %d", started, count, maxDispatchPerPass)
	}
}

// TestDispatchHandsBackAClaimItCouldNotStart: the claim already moved the row to
// running, so a start that never happened would hold the tenant's only slot
// until the stale window expires it.
func TestDispatchHandsBackAClaimItCouldNotStart(t *testing.T) {
	queue := &fakeReleaseQueue{}
	service := NewReleaseService(queue, &recordingBuilds{}, &fakeReleaseRunner{})
	release := enqueue(t, service, "commit-a").Release

	_, err := service.Dispatch(context.Background(), func(model.Release) error {
		return errors.New("the durable workflow could not be started")
	})
	if err == nil {
		t.Fatal("expected the failed start to surface an error")
	}
	row := mustGet(t, queue, release.ReleaseID)
	if row.Status != model.ReleaseStatusFailed {
		t.Fatalf("status = %q, want failed rather than a row left running with nothing behind it", row.Status)
	}
	if !strings.Contains(row.FailureReason, "could not be started") {
		t.Fatalf("failureReason = %q, want it to say the release never started", row.FailureReason)
	}
}

// alwaysClaimQueue hands out a fresh claimable release every time, standing in
// for a queue with more runnable work than one pass may start.
type alwaysClaimQueue struct {
	fakeReleaseQueue
	handed int
}

func (q *alwaysClaimQueue) ClaimNext(context.Context, repository.ClaimWindow) (model.Release, bool, error) {
	q.handed++
	return model.Release{ReleaseID: "release-" + string(rune('a'+q.handed-1)), Attempt: 1}, true, nil
}

func mustGet(t *testing.T, queue *fakeReleaseQueue, releaseID string) model.Release {
	t.Helper()
	row, err := queue.Get(context.Background(), releaseID)
	if err != nil {
		t.Fatalf("get release %s: %v", releaseID, err)
	}
	return row
}

func mustNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
