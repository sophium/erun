package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// fakeReviewRepo models reviews and review_merge_queue: FIFO queue order, and
// FindNextMergeQueueReview returns only READY reviews.
type fakeReviewRepo struct {
	reviews map[string]*model.Review
	queue   []model.ReviewMergeQueueEntry
	nextID  int64
}

func newFakeReviewRepo(reviews ...model.Review) *fakeReviewRepo {
	repo := &fakeReviewRepo{reviews: map[string]*model.Review{}}
	for _, review := range reviews {
		r := review
		repo.reviews[r.ReviewID] = &r
	}
	return repo
}

func (f *fakeReviewRepo) Get(_ context.Context, reviewID string) (model.Review, error) {
	r, ok := f.reviews[reviewID]
	if !ok {
		return model.Review{}, repository.ErrNotFound
	}
	return *r, nil
}

func (f *fakeReviewRepo) Update(_ context.Context, review model.Review) (model.Review, error) {
	if _, ok := f.reviews[review.ReviewID]; !ok {
		return model.Review{}, repository.ErrNotFound
	}
	r := review
	f.reviews[review.ReviewID] = &r
	return r, nil
}

// An empty filter matches everything; a review with no repository matches only that.
func inRepository(review model.Review, repositoryFilter string) bool {
	return repositoryFilter == "" || review.Repository == repositoryFilter
}

func (f *fakeReviewRepo) FindNextMergeQueueReview(_ context.Context, repositoryFilter, targetBranch string) (model.Review, error) {
	for _, entry := range f.queue {
		if entry.TargetBranch != targetBranch {
			continue
		}
		if review, ok := f.reviews[entry.ReviewID]; ok && review.Status == model.ReviewStatusReady && inRepository(*review, repositoryFilter) {
			return *review, nil
		}
	}
	return model.Review{}, repository.ErrNotFound
}

func (f *fakeReviewRepo) FindActiveMergeReview(_ context.Context, repositoryFilter, targetBranch string) (model.Review, error) {
	for _, review := range f.reviews {
		if review.TargetBranch == targetBranch && review.Status == model.ReviewStatusMerge && inRepository(*review, repositoryFilter) {
			return *review, nil
		}
	}
	return model.Review{}, repository.ErrNotFound
}

// Tests never hold more than one MERGED review, so order does not matter here.
func (f *fakeReviewRepo) FindLastMergedReview(_ context.Context, repositoryFilter, targetBranch string) (model.Review, error) {
	for _, review := range f.reviews {
		if review.TargetBranch == targetBranch && review.Status == model.ReviewStatusMerged && inRepository(*review, repositoryFilter) {
			return *review, nil
		}
	}
	return model.Review{}, repository.ErrNotFound
}

func (f *fakeReviewRepo) QueuedRepositories(_ context.Context, targetBranch string) (repository.MergeQueueRepositories, error) {
	seen := map[string]bool{}
	queued := repository.MergeQueueRepositories{}
	for _, entry := range f.queue {
		if entry.TargetBranch != targetBranch {
			continue
		}
		review, ok := f.reviews[entry.ReviewID]
		if !ok || review.Status != model.ReviewStatusReady {
			continue
		}
		if strings.TrimSpace(review.Repository) == "" {
			queued.Unrecorded = append(queued.Unrecorded, review.ReviewID)
			continue
		}
		if !seen[review.Repository] {
			seen[review.Repository] = true
			queued.Named = append(queued.Named, review.Repository)
		}
	}
	return queued, nil
}

func (f *fakeReviewRepo) CreateMergeQueueEntry(_ context.Context, entry model.ReviewMergeQueueEntry) (model.ReviewMergeQueueEntry, error) {
	f.nextID++
	entry.ReviewMergeQueueID = f.nextID
	f.queue = append(f.queue, entry)
	return entry, nil
}

func (f *fakeReviewRepo) DeleteMergeQueueEntryByReview(_ context.Context, reviewID string) error {
	kept := f.queue[:0]
	for _, entry := range f.queue {
		if entry.ReviewID != reviewID {
			kept = append(kept, entry)
		}
	}
	f.queue = kept
	return nil
}

type fakeReviewBuilds struct {
	builds map[string]model.Build
}

func (f *fakeReviewBuilds) Get(_ context.Context, _ string, buildID string) (model.Build, error) {
	b, ok := f.builds[buildID]
	if !ok {
		return model.Build{}, repository.ErrNotFound
	}
	return b, nil
}

type fakeReviewComments struct {
	byReview map[string][]model.Comment
}

func (f *fakeReviewComments) List(_ context.Context, filter repository.CommentFilter) ([]model.Comment, error) {
	return f.byReview[filter.ReviewID], nil
}

// fakeReviewAudit: tests wanting no audit logger pass a literal nil, since a
// typed nil would not compare equal to nil through the interface.
type fakeReviewAudit struct {
	events []model.AuditEvent
}

func (f *fakeReviewAudit) LogAuditEvent(_ context.Context, event model.AuditEvent) error {
	f.events = append(f.events, event)
	return nil
}

type fakeMergeVerifier struct {
	onBranch bool
	// onBranchCommits, when set, answers Contains per commit instead of onBranch.
	onBranchCommits map[string]bool
	parent          string
	err             error
	isAncestor      bool
	isAncestorErr   error
	// contained and landingCommit answer reconcileMerged.
	contained     bool
	landingCommit string
	changesErr    error
}

func (f fakeMergeVerifier) Contains(_ context.Context, _, _, commit string) (bool, string, error) {
	if f.onBranchCommits != nil {
		return f.onBranchCommits[commit], f.parent, f.err
	}
	return f.onBranch, f.parent, f.err
}

func (f fakeMergeVerifier) IsAncestor(_ context.Context, _, _, _, _ string) (bool, error) {
	return f.isAncestor, f.isAncestorErr
}

func (f fakeMergeVerifier) ContainsChanges(_ context.Context, _, _, _ string) (bool, string, error) {
	return f.contained, f.landingCommit, f.changesErr
}

type fakeReleaseTrigger struct {
	requests []ReleaseRequest
	err      error
}

func (f *fakeReleaseTrigger) TriggerRelease(_ context.Context, request ReleaseRequest) error {
	f.requests = append(f.requests, request)
	return f.err
}

// newTestReviewService records no comments, so no test trips the thread gate by omission.
func newTestReviewService(reviews ReviewRepository, builds ReviewBuildRepository) (*ReviewService, *fakeReviewAudit) {
	audit := &fakeReviewAudit{}
	return NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, audit, nil, nil), audit
}

func TestUpdateStatusRefusesMergeAlways(t *testing.T) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", Status: model.ReviewStatusReady})
	svc, _ := newTestReviewService(reviews, &fakeReviewBuilds{})

	_, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerge, "build-1", "")
	var invalidTransition *InvalidTransitionError
	if !errors.As(err, &invalidTransition) {
		t.Fatalf("UpdateStatus(MERGE) error = %v, want *InvalidTransitionError", err)
	}
	if !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("UpdateStatus(MERGE) error = %v, want it to unwrap to ErrInvalidInput", err)
	}
	if invalidTransition.From != model.ReviewStatusReady || invalidTransition.To != model.ReviewStatusMerge {
		t.Fatalf("InvalidTransitionError = %+v, want from READY to MERGE", invalidTransition)
	}
	if got := reviews.reviews["review-1"].Status; got != model.ReviewStatusReady {
		t.Fatalf("UpdateStatus(MERGE) changed the review to %s despite being refused", got)
	}
}

const testRepository = "file:///remote"

// mergingReviewWithMergeBuild returns a MERGE review with a successful merge build
// and no recorded repository (the legacy shape).
func mergingReviewWithMergeBuild(commit string) (*fakeReviewRepo, *fakeReviewBuilds) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", Status: model.ReviewStatusMerge})
	builds := &fakeReviewBuilds{builds: map[string]model.Build{
		"merge-1": {BuildID: "merge-1", ReviewID: "review-1", Kind: model.BuildKindRecorded, Version: "1.0.0", Successful: true, CommitID: commit},
	}}
	return reviews, builds
}

func TestAcceptMergedRefusesWhenCommitIsNotOnTheTargetBranch(t *testing.T) {
	reviews, builds := mergingReviewWithMergeBuild("merge-commit")
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranch: false}, nil)

	_, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "merge-1", "file:///remote.git")

	var notVerified *MergeNotVerifiedError
	if !errors.As(err, &notVerified) {
		t.Fatalf("UpdateStatus(MERGED) error = %v, want *MergeNotVerifiedError", err)
	}
	if !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("error = %v, want it to unwrap to ErrInvalidInput", err)
	}
	if got := reviews.reviews["review-1"].Status; got != model.ReviewStatusMerge {
		t.Fatalf("status = %s, want the review left at MERGE, not moved to MERGED", got)
	}
}

// mergingReviewGatedAgainst adds a prior MERGED review at "real-tip" in
// testRepository; its build is a historical GATE row.
func mergingReviewGatedAgainst(commit string) (*fakeReviewRepo, *fakeReviewBuilds) {
	reviews, builds := mergingReviewWithMergeBuild(commit)
	priorMerge := model.Review{ReviewID: "review-0", TargetBranch: "main", Status: model.ReviewStatusMerged, LastMergedBuildID: "gate-0", Repository: testRepository}
	reviews.reviews["review-0"] = &priorMerge
	builds.builds["gate-0"] = model.Build{BuildID: "gate-0", ReviewID: "review-0", Kind: model.BuildKindGate, Successful: true, CommitID: "real-tip"}
	return reviews, builds
}

func TestAcceptMergedRefusesWhenGatedTipIsNotAnAncestorOfTheReportedCommit(t *testing.T) {
	reviews, builds := mergingReviewGatedAgainst("merge-commit")
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranch: true, parent: "wrong-parent", isAncestor: false}, nil)

	_, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "merge-1", "file:///remote.git")

	var notVerified *MergeNotVerifiedError
	if !errors.As(err, &notVerified) {
		t.Fatalf("UpdateStatus(MERGED) error = %v, want *MergeNotVerifiedError", err)
	}
	if got := reviews.reviews["review-1"].Status; got != model.ReviewStatusMerge {
		t.Fatalf("status = %s, want the review left at MERGE, not moved to MERGED", got)
	}
}

func TestAcceptMergedSucceedsWhenParentIsTheGatedTip(t *testing.T) {
	reviews, builds := mergingReviewGatedAgainst("merge-commit")
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranch: true, parent: "real-tip", isAncestor: true}, nil)

	updated, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "merge-1", "file:///remote.git")
	if err != nil {
		t.Fatalf("UpdateStatus(MERGED): %v", err)
	}
	if updated.Status != model.ReviewStatusMerged {
		t.Fatalf("status = %s, want MERGED", updated.Status)
	}
}

// E.g. the release flow's `[skip ci]` pushes between gating and reporting.
func TestAcceptMergedSucceedsWhenUnrelatedCommitsLandedBetweenGatingAndReporting(t *testing.T) {
	reviews, builds := mergingReviewGatedAgainst("merge-commit")
	release := &fakeReleaseTrigger{}
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranch: true, parent: "release-prepare-commit", isAncestor: true}, release)

	updated, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "merge-1", "file:///remote.git")
	if err != nil {
		t.Fatalf("UpdateStatus(MERGED): %v", err)
	}
	if updated.Status != model.ReviewStatusMerged {
		t.Fatalf("status = %s, want MERGED", updated.Status)
	}
	if len(release.requests) != 1 {
		t.Fatalf("release requests = %+v, want exactly one triggered", release.requests)
	}
}

func TestAcceptMergedRefusesWithNoSuccessfulMergeBuildRecorded(t *testing.T) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", Status: model.ReviewStatusMerge})
	builds := &fakeReviewBuilds{builds: map[string]model.Build{
		"merge-1": {BuildID: "merge-1", ReviewID: "review-1", Kind: model.BuildKindRecorded, Version: "1.0.0", Successful: false, CommitID: "merge-commit"},
	}}
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranch: true}, nil)

	_, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "merge-1", "file:///remote.git")

	var notVerified *MergeNotVerifiedError
	if !errors.As(err, &notVerified) {
		t.Fatalf("UpdateStatus(MERGED) error = %v, want *MergeNotVerifiedError", err)
	}
	if got := reviews.reviews["review-1"].Status; got != model.ReviewStatusMerge {
		t.Fatalf("status = %s, want the review left at MERGE, not moved to MERGED", got)
	}
}

func TestAcceptMergedRefusesTheReadyBuildOfAnUnlandedSourceTip(t *testing.T) {
	reviews, builds := mergingReviewGatedAgainst("merge-commit")
	reviews.reviews["review-1"].LastReadyBuildID = "ready-1"
	builds.builds["ready-1"] = model.Build{BuildID: "ready-1", ReviewID: "review-1", Kind: model.BuildKindRecorded, Version: "1.0.0", Successful: true, CommitID: "source-tip"}
	release := &fakeReleaseTrigger{}
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranchCommits: map[string]bool{"merge-commit": true, "real-tip": true}, isAncestor: true}, release)

	_, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "ready-1", "file:///remote.git")

	var notVerified *MergeNotVerifiedError
	if !errors.As(err, &notVerified) {
		t.Fatalf("UpdateStatus(MERGED) with the READY build error = %v, want *MergeNotVerifiedError", err)
	}
	if !strings.Contains(err.Error(), "source-tip") {
		t.Fatalf("error = %q, want it to name the commit that is not on the target branch", err.Error())
	}
	if got := reviews.reviews["review-1"].Status; got != model.ReviewStatusMerge {
		t.Fatalf("status = %s, want the review left at MERGE", got)
	}
	if len(release.requests) != 0 {
		t.Fatalf("release requests = %+v, want none for a refused merge", release.requests)
	}
}

func TestAcceptMergedRefusesTheReadyBuildOfALandedSourceTip(t *testing.T) {
	reviews, builds := mergingReviewGatedAgainst("source-tip")
	reviews.reviews["review-1"].LastReadyBuildID = "ready-1"
	builds.builds["ready-1"] = model.Build{BuildID: "ready-1", ReviewID: "review-1", Kind: model.BuildKindRecorded, Version: "1.0.0", Successful: true, CommitID: "source-tip"}
	release := &fakeReleaseTrigger{}
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranch: true, isAncestor: true}, release)

	_, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "ready-1", "file:///remote.git")

	var notVerified *MergeNotVerifiedError
	if !errors.As(err, &notVerified) {
		t.Fatalf("UpdateStatus(MERGED) with the READY build error = %v, want *MergeNotVerifiedError", err)
	}
	if !strings.Contains(err.Error(), "READY build") {
		t.Fatalf("error = %q, want it to name the READY build", err.Error())
	}
	if len(release.requests) != 0 {
		t.Fatalf("release requests = %+v, want none for a refused merge", release.requests)
	}
}

// A commit is its own ancestor, so this would otherwise pass.
func TestAcceptMergedRefusesABuildOfTheGatedTip(t *testing.T) {
	reviews, builds := mergingReviewGatedAgainst("real-tip")
	release := &fakeReleaseTrigger{}
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranch: true, isAncestor: true}, release)

	_, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "merge-1", "file:///remote.git")

	var notVerified *MergeNotVerifiedError
	if !errors.As(err, &notVerified) {
		t.Fatalf("UpdateStatus(MERGED) with a build of the gated tip error = %v, want *MergeNotVerifiedError", err)
	}
	if !strings.Contains(err.Error(), "gated against") {
		t.Fatalf("error = %q, want it to say the commit is the gated tip", err.Error())
	}
	if got := reviews.reviews["review-1"].Status; got != model.ReviewStatusMerge {
		t.Fatalf("status = %s, want the review left at MERGE", got)
	}
	if len(release.requests) != 0 {
		t.Fatalf("release requests = %+v, want none for a refused merge", release.requests)
	}
}

func TestAcceptMergedAcceptsARecordedBuildOfTheLandedMergeCommit(t *testing.T) {
	reviews, builds := mergingReviewGatedAgainst("merge-commit")
	reviews.reviews["review-1"].LastReadyBuildID = "ready-1"
	builds.builds["ready-1"] = model.Build{BuildID: "ready-1", ReviewID: "review-1", Kind: model.BuildKindRecorded, Version: "1.0.0", Successful: true, CommitID: "source-tip"}
	release := &fakeReleaseTrigger{}
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranchCommits: map[string]bool{"merge-commit": true, "real-tip": true}, isAncestor: true}, release)

	updated, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "merge-1", "file:///remote.git")
	if err != nil {
		t.Fatalf("UpdateStatus(MERGED): %v", err)
	}
	if updated.Status != model.ReviewStatusMerged || updated.LastMergedBuildID != "merge-1" {
		t.Fatalf("updated = %+v, want MERGED with lastMergedBuildId=merge-1", updated)
	}
	if len(release.requests) != 1 || release.requests[0].CommitID != "merge-commit" {
		t.Fatalf("release requests = %+v, want one for merge-commit", release.requests)
	}
}

func TestAcceptMergedStillAcceptsAHistoricalGateBuild(t *testing.T) {
	reviews, builds := mergingReviewGatedAgainst("merge-commit")
	builds.builds["merge-1"] = model.Build{BuildID: "merge-1", ReviewID: "review-1", Kind: model.BuildKindGate, Successful: true, CommitID: "merge-commit"}
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranch: true, isAncestor: true}, nil)

	updated, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "merge-1", "file:///remote.git")
	if err != nil {
		t.Fatalf("UpdateStatus(MERGED) with a historical GATE build: %v", err)
	}
	if updated.Status != model.ReviewStatusMerged {
		t.Fatalf("status = %s, want MERGED", updated.Status)
	}
}

func TestAcceptMergedSucceedsWhenAllThreeConditionsHold(t *testing.T) {
	reviews, builds := mergingReviewWithMergeBuild("merge-commit")
	release := &fakeReleaseTrigger{}
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranch: true, parent: ""}, release)

	updated, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "merge-1", "file:///remote.git")
	if err != nil {
		t.Fatalf("UpdateStatus(MERGED): %v", err)
	}
	if updated.Status != model.ReviewStatusMerged || updated.LastMergedBuildID != "merge-1" {
		t.Fatalf("updated = %+v, want MERGED with lastMergedBuildId=gate-1", updated)
	}
	if len(release.requests) != 1 || release.requests[0].CommitID != "merge-commit" {
		t.Fatalf("release requests = %+v, want one for merge-commit", release.requests)
	}
}

func TestAcceptMergedRefusesARemoteForADifferentRepository(t *testing.T) {
	reviews, builds := mergingReviewWithMergeBuild("merge-commit")
	reviews.reviews["review-1"].Repository = "https://github.com/sophium/erun"
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranch: true, parent: "real-tip", isAncestor: true}, nil)

	_, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "merge-1", "https://github.com/sophium/other")

	var notVerified *MergeNotVerifiedError
	if !errors.As(err, &notVerified) {
		t.Fatalf("UpdateStatus(MERGED) error = %v, want *MergeNotVerifiedError", err)
	}
	if !strings.Contains(err.Error(), "https://github.com/sophium/erun") {
		t.Fatalf("error = %q, want it to name the repository the review records", err.Error())
	}
	if got := reviews.reviews["review-1"].Status; got != model.ReviewStatusMerge {
		t.Fatalf("status = %s, want the review left at MERGE", got)
	}
}

func TestAcceptMergedAcceptsAnSSHSpellingOfTheReviewsOwnRepository(t *testing.T) {
	reviews, builds := mergingReviewWithMergeBuild("merge-commit")
	reviews.reviews["review-1"].Repository = "https://github.com/sophium/erun"
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranch: true, parent: "real-tip", isAncestor: true}, nil)

	updated, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "merge-1", "git@github.com:sophium/erun.git")
	if err != nil {
		t.Fatalf("UpdateStatus(MERGED): %v", err)
	}
	if updated.Repository != "https://github.com/sophium/erun" {
		t.Fatalf("repository = %q, want the review's own canonical identity", updated.Repository)
	}
}

func TestAcceptMergedRecordsTheRepositoryAnUnrecordedReviewWasReportedUnder(t *testing.T) {
	reviews, builds := mergingReviewWithMergeBuild("merge-commit")
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranch: true, parent: "real-tip", isAncestor: true}, nil)

	updated, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "merge-1", "git@github.com:sophium/erun.git")
	if err != nil {
		t.Fatalf("UpdateStatus(MERGED): %v", err)
	}
	if updated.Repository != "https://github.com/sophium/erun" {
		t.Fatalf("repository = %q, want the reported remote recorded as the review's identity", updated.Repository)
	}
}

// An empty repository filter means "every repository", so anchoring on the
// review's column would answer with another repository's merge.
func TestAcceptMergedDoesNotAnchorAnUnrecordedReviewOnAnotherRepositorysMerge(t *testing.T) {
	reviews, builds := mergingReviewWithMergeBuild("merge-commit")
	otherRepoMerge := model.Review{ReviewID: "review-other", TargetBranch: "main", Status: model.ReviewStatusMerged, LastMergedBuildID: "merge-other", Repository: "file:///other"}
	reviews.reviews["review-other"] = &otherRepoMerge
	builds.builds["merge-other"] = model.Build{BuildID: "merge-other", ReviewID: "review-other", Kind: model.BuildKindRecorded, Version: "1.0.0", Successful: true, CommitID: "other-repository-tip"}
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranch: true, parent: "real-tip", isAncestor: false}, nil)

	// file:///remote has never merged onto this branch, so there is nothing to descend from.
	updated, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "merge-1", "file:///remote.git")
	if err != nil {
		t.Fatalf("UpdateStatus(MERGED) error = %v, want the merge accepted: the other repository's tip is not this review's gated base", err)
	}
	if updated.Status != model.ReviewStatusMerged {
		t.Fatalf("status = %s, want MERGED", updated.Status)
	}
	if updated.Repository != testRepository {
		t.Fatalf("repository = %q, want the reported remote recorded as the review's identity", updated.Repository)
	}
}
