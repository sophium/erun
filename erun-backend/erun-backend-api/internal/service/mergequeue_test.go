package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/mergeexec"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

// fakeMergeReviews is the narrow raw-repository dependency MergeQueueService
// uses to apply MERGE -> MERGED/FAILED itself, deliberately independent of
// ReviewService (see AGENTS.md "Merge Queue" for why).
type fakeMergeReviews struct {
	review       model.Review
	deletedQueue bool
	getErr       error
}

func (f *fakeMergeReviews) Get(context.Context, string) (model.Review, error) {
	if f.getErr != nil {
		return model.Review{}, f.getErr
	}
	return f.review, nil
}

func (f *fakeMergeReviews) Update(_ context.Context, review model.Review) (model.Review, error) {
	f.review = review
	return review, nil
}

func (f *fakeMergeReviews) DeleteMergeQueueEntryByReview(context.Context, string) error {
	f.deletedQueue = true
	return nil
}

type fakeMergeBuilds struct {
	created []model.Build
}

func (f *fakeMergeBuilds) Create(_ context.Context, build model.Build) (model.Build, error) {
	build.BuildID = "gate-build-" + build.CommitID
	f.created = append(f.created, build)
	return build, nil
}

type fakeMergeRunner struct {
	result mergeexec.Result
	err    error
}

func (f *fakeMergeRunner) Run(context.Context, mergeexec.MergeJobParams) (mergeexec.Result, error) {
	return f.result, f.err
}

type fakeReleaseTrigger struct {
	requests []ReleaseRequest
}

func (f *fakeReleaseTrigger) TriggerRelease(_ context.Context, request ReleaseRequest) error {
	f.requests = append(f.requests, request)
	return nil
}

func mergingReview() model.Review {
	return model.Review{ReviewID: "review-1", TargetBranch: "main", SourceBranch: "feature/x", Status: model.ReviewStatusMerge}
}

// TestMergeQueueServiceMergesOnlyOnAGreenGateBuild is the property the whole
// issue is about: MERGED is written only once the Job actually reported a
// merge commit it built and pushed, gated by an actually-successful `erun
// build`. Both halves — the merge and the build — have to hold.
func TestMergeQueueServiceMergesOnlyOnAGreenGateBuild(t *testing.T) {
	reviews := &fakeMergeReviews{review: mergingReview()}
	builds := &fakeMergeBuilds{}
	release := &fakeReleaseTrigger{}
	runner := &fakeMergeRunner{result: mergeexec.Result{Outcome: mergeexec.OutcomeSucceeded, MergeCommit: "merge-sha-1", SourceCommit: "src-sha-1"}}
	svc := NewMergeQueueService(reviews, builds, runner, release)

	if err := svc.Run(context.Background(), "review-1", "main", mergeexec.MergeJobParams{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if reviews.review.Status != model.ReviewStatusMerged {
		t.Fatalf("status = %s, want MERGED", reviews.review.Status)
	}
	build := onlyCreatedBuild(t, builds)
	assertGateBuild(t, build, true, "merge-sha-1")
	if reviews.review.LastMergedBuildID != build.BuildID {
		t.Fatalf("LastMergedBuildID = %q, want %q", reviews.review.LastMergedBuildID, build.BuildID)
	}
	assertSingleReleaseRequest(t, release, ReleaseRequest{ReviewID: "review-1", TargetBranch: "main", CommitID: "merge-sha-1"})
}

func onlyCreatedBuild(t *testing.T, builds *fakeMergeBuilds) model.Build {
	t.Helper()
	if len(builds.created) != 1 {
		t.Fatalf("created %d builds, want 1", len(builds.created))
	}
	return builds.created[0]
}

// assertGateBuild checks the shape every GATE build must have: the right
// kind, the right commit, and a version only ever absent — a GATE build never
// mints one.
func assertGateBuild(t *testing.T, build model.Build, wantSuccessful bool, wantCommit string) {
	t.Helper()
	if build.Kind != model.BuildKindGate {
		t.Fatalf("gate build kind = %q, want GATE", build.Kind)
	}
	if build.Successful != wantSuccessful {
		t.Fatalf("gate build successful = %v, want %v", build.Successful, wantSuccessful)
	}
	if build.CommitID != wantCommit {
		t.Fatalf("gate build commitId = %q, want %q", build.CommitID, wantCommit)
	}
	if build.Version != "" {
		t.Fatalf("gate build version = %q, want none — the gate publishes nothing", build.Version)
	}
}

func assertSingleReleaseRequest(t *testing.T, release *fakeReleaseTrigger, want ReleaseRequest) {
	t.Helper()
	if len(release.requests) != 1 {
		t.Fatalf("release requests = %+v, want exactly one", release.requests)
	}
	if release.requests[0] != want {
		t.Fatalf("release request = %+v, want %+v", release.requests[0], want)
	}
}

// TestMergeQueueServiceFailsWhenTheGateBuildFails: the merge succeeded (there
// is a merge commit) but `erun build` did not, so the whole attempt is a
// failure — the merge must never land without a passing gate.
func TestMergeQueueServiceFailsWhenTheGateBuildFails(t *testing.T) {
	reviews := &fakeMergeReviews{review: mergingReview()}
	builds := &fakeMergeBuilds{}
	release := &fakeReleaseTrigger{}
	runner := &fakeMergeRunner{result: mergeexec.Result{
		Outcome: mergeexec.OutcomeFailed, MergeCommit: "merge-sha-1", SourceCommit: "src-sha-1",
		Failure: "erun build failed: go vet found 3 issues",
	}}
	svc := NewMergeQueueService(reviews, builds, runner, release)

	if err := svc.Run(context.Background(), "review-1", "main", mergeexec.MergeJobParams{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if reviews.review.Status != model.ReviewStatusFailed {
		t.Fatalf("status = %s, want FAILED", reviews.review.Status)
	}
	if !reviews.deletedQueue {
		t.Fatal("a failed gate build did not remove the review from the merge queue")
	}
	build := onlyCreatedBuild(t, builds)
	assertGateBuild(t, build, false, "merge-sha-1")
	if !strings.Contains(build.FailureDetail, "go vet found 3 issues") {
		t.Fatalf("failureDetail = %q, want it to carry the gate's own words", build.FailureDetail)
	}
	if len(release.requests) != 0 {
		t.Fatalf("a failed gate triggered a release: %+v", release.requests)
	}
}

// TestMergeQueueServiceFailsOnAnUnresolvedConflict: the squash merge itself
// never produced a commit, so the failed attempt is recorded against the
// source branch tip instead — the only artifact that exists to record it
// against.
func TestMergeQueueServiceFailsOnAnUnresolvedConflict(t *testing.T) {
	reviews := &fakeMergeReviews{review: mergingReview()}
	builds := &fakeMergeBuilds{}
	runner := &fakeMergeRunner{result: mergeexec.Result{
		Outcome: mergeexec.OutcomeFailed, MergeCommit: "", SourceCommit: "src-sha-1", Failure: "CONFLICT (content): Merge conflict in main.go",
	}}
	svc := NewMergeQueueService(reviews, builds, runner, &fakeReleaseTrigger{})

	if err := svc.Run(context.Background(), "review-1", "main", mergeexec.MergeJobParams{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if reviews.review.Status != model.ReviewStatusFailed {
		t.Fatalf("status = %s, want FAILED", reviews.review.Status)
	}
	if builds.created[0].CommitID != "src-sha-1" {
		t.Fatalf("commitId = %q, want the source branch tip src-sha-1", builds.created[0].CommitID)
	}
}

// TestMergeQueueServiceRefusesASuccessWithNoReportedCommit is the trap the
// issue calls out explicitly: a Job that exits 0 without ever saying what it
// merged and pushed must NOT become MERGED. Without this check, a merge
// executor whose marker-parsing regressed (or whose push silently no-op'd)
// would report Outcome=succeeded with an empty MergeCommit, and a naive
// implementation that only checked the Job's exit code would mark the review
// MERGED anyway — exactly the "MERGED is a caller's/executor's assertion, not
// a verified fact" defect this feature exists to close.
func TestMergeQueueServiceRefusesASuccessWithNoReportedCommit(t *testing.T) {
	reviews := &fakeMergeReviews{review: mergingReview()}
	builds := &fakeMergeBuilds{}
	release := &fakeReleaseTrigger{}
	runner := &fakeMergeRunner{result: mergeexec.Result{Outcome: mergeexec.OutcomeSucceeded, MergeCommit: "", SourceCommit: "src-sha-1"}}
	svc := NewMergeQueueService(reviews, builds, runner, release)

	if err := svc.Run(context.Background(), "review-1", "main", mergeexec.MergeJobParams{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if reviews.review.Status == model.ReviewStatusMerged {
		t.Fatal("a green Job with no reported merge commit was still marked MERGED")
	}
	if reviews.review.Status != model.ReviewStatusFailed {
		t.Fatalf("status = %s, want FAILED", reviews.review.Status)
	}
	if len(release.requests) != 0 {
		t.Fatalf("triggered a release with no real merge commit: %+v", release.requests)
	}
}

// TestMergeQueueServiceFailsWhenTheRunnerErrors: a Job that could not even be
// launched (e.g. the Kubernetes API call itself failed) must still leave the
// review in a terminal, actionable state rather than stuck on MERGE forever.
func TestMergeQueueServiceFailsWhenTheRunnerErrors(t *testing.T) {
	reviews := &fakeMergeReviews{review: mergingReview()}
	builds := &fakeMergeBuilds{}
	runner := &fakeMergeRunner{err: errors.New("create job: connection refused")}
	svc := NewMergeQueueService(reviews, builds, runner, &fakeReleaseTrigger{})

	if err := svc.Run(context.Background(), "review-1", "main", mergeexec.MergeJobParams{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if reviews.review.Status != model.ReviewStatusFailed {
		t.Fatalf("status = %s, want FAILED", reviews.review.Status)
	}
	if !strings.Contains(builds.created[0].FailureDetail, "connection refused") {
		t.Fatalf("failureDetail = %q, want it to carry the launch error", builds.created[0].FailureDetail)
	}
}

// TestMergeQueueServiceWithNoRunnerFailsRatherThanHangs: an unconfigured merge
// executor must record a review as FAILED with a clear reason, not leave it
// silently stuck on MERGE.
func TestMergeQueueServiceWithNoRunnerFailsRatherThanHangs(t *testing.T) {
	reviews := &fakeMergeReviews{review: mergingReview()}
	builds := &fakeMergeBuilds{}
	svc := NewMergeQueueService(reviews, builds, nil, nil)

	if err := svc.Run(context.Background(), "review-1", "main", mergeexec.MergeJobParams{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if reviews.review.Status != model.ReviewStatusFailed {
		t.Fatalf("status = %s, want FAILED", reviews.review.Status)
	}
	if !strings.Contains(builds.created[0].FailureDetail, "no merge executor configured") {
		t.Fatalf("failureDetail = %q, want it to name the missing executor", builds.created[0].FailureDetail)
	}
}

// TestMergeQueueServiceLeavesAReviewThatAlreadyMovedOnAlone: if an operator
// manually intervened while the gate was running (e.g. the missed-window
// requeue path put the review back to READY), the attempt's own outcome must
// not clobber whatever the review is now.
func TestMergeQueueServiceLeavesAReviewThatAlreadyMovedOnAlone(t *testing.T) {
	reviews := &fakeMergeReviews{review: model.Review{ReviewID: "review-1", TargetBranch: "main", Status: model.ReviewStatusReady}}
	builds := &fakeMergeBuilds{}
	runner := &fakeMergeRunner{result: mergeexec.Result{Outcome: mergeexec.OutcomeSucceeded, MergeCommit: "merge-sha-1"}}
	svc := NewMergeQueueService(reviews, builds, runner, &fakeReleaseTrigger{})

	if err := svc.Run(context.Background(), "review-1", "main", mergeexec.MergeJobParams{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if reviews.review.Status != model.ReviewStatusReady {
		t.Fatalf("status = %s, want the READY status left untouched", reviews.review.Status)
	}
	if len(builds.created) != 0 {
		t.Fatalf("recorded %d builds against a review that had already moved on", len(builds.created))
	}
}
