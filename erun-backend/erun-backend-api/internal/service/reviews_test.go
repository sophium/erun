package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// fakeReviewRepo models reviews + review_merge_queue in memory, closely enough
// to exercise ReviewService's promotion logic: queue order is FIFO by
// insertion (the real table's surrogate integer key), and FindNextMergeQueueReview
// only returns a READY review, matching the real join's WHERE clause.
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

// inRepository mirrors the real queries' repository predicate: an empty
// repository filters nothing, and a named one matches exactly, so a review
// with no recorded repository is reached only by the unfiltered answer.
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

// FindLastMergedReview picks any MERGED review on targetBranch in repository:
// tests never have more than one, so insertion order does not matter here the
// way it does for the real query's ORDER BY.
func (f *fakeReviewRepo) FindLastMergedReview(_ context.Context, repositoryFilter, targetBranch string) (model.Review, error) {
	for _, review := range f.reviews {
		if review.TargetBranch == targetBranch && review.Status == model.ReviewStatusMerged && inRepository(*review, repositoryFilter) {
			return *review, nil
		}
	}
	return model.Review{}, repository.ErrNotFound
}

// QueuedRepositories mirrors the real query: the distinct repositories of the
// READY reviews this fake has queued for targetBranch, and the queued reviews
// that record none, in queue order.
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

// fakeReviewBuilds is the narrow ReviewBuildRepository dependency: a lookup by
// build id, used only by updateBuildStatus's caller-reported FAILED/READY path.
type fakeReviewBuilds struct {
	builds map[string]model.Build
}

func (f *fakeReviewBuilds) Get(_ context.Context, buildID string) (model.Build, error) {
	b, ok := f.builds[buildID]
	if !ok {
		return model.Build{}, repository.ErrNotFound
	}
	return b, nil
}

// fakeReviewComments is the narrow ReviewCommentRepository dependency:
// AdvanceMergeQueue's unresolved-thread gate lists a review's comments and
// counts open root comments.
type fakeReviewComments struct {
	byReview map[string][]model.Comment
}

func (f *fakeReviewComments) List(_ context.Context, filter repository.CommentFilter) ([]model.Comment, error) {
	return f.byReview[filter.ReviewID], nil
}

// fakeReviewAudit is the narrow ReviewAuditLogger dependency:
// OverrideAdvanceMergeQueue's one required side effect. Tests that want "no
// audit logger configured" pass a literal nil for the ReviewAuditLogger
// parameter instead of a typed *fakeReviewAudit nil, which would not compare
// equal to nil through the interface.
type fakeReviewAudit struct {
	events []model.AuditEvent
}

func (f *fakeReviewAudit) LogAuditEvent(_ context.Context, event model.AuditEvent) error {
	f.events = append(f.events, event)
	return nil
}

// fakeMergeVerifier is the narrow MergeVerifier dependency: a fixed answer
// (or error) for whatever commit/branch acceptMerged asks about, so a test
// can drive each of the three verification conditions independently.
type fakeMergeVerifier struct {
	onBranch      bool
	parent        string
	err           error
	isAncestor    bool
	isAncestorErr error
	// contained and landingCommit answer reconcileMerged's own question, the
	// one a review that landed without the queue is asked instead.
	contained     bool
	landingCommit string
	changesErr    error
}

func (f fakeMergeVerifier) Contains(_ context.Context, _, _, _ string) (bool, string, error) {
	return f.onBranch, f.parent, f.err
}

func (f fakeMergeVerifier) IsAncestor(_ context.Context, _, _, _, _ string) (bool, error) {
	return f.isAncestor, f.isAncestorErr
}

func (f fakeMergeVerifier) ContainsChanges(_ context.Context, _, _, _ string) (bool, string, error) {
	return f.contained, f.landingCommit, f.changesErr
}

// fakeReleaseTrigger records every release TriggerRelease was asked to start.
type fakeReleaseTrigger struct {
	requests []ReleaseRequest
	err      error
}

func (f *fakeReleaseTrigger) TriggerRelease(_ context.Context, request ReleaseRequest) error {
	f.requests = append(f.requests, request)
	return f.err
}

// newTestReviewService wires a ReviewService with fakes sized for the common
// case: no comments recorded anywhere (so no test accidentally trips the
// unresolved-thread gate by omission), a working audit logger, and no merge
// verifier or release trigger (nil is fine for every test that never reaches
// a MERGED transition).
func newTestReviewService(reviews ReviewRepository, builds ReviewBuildRepository) (*ReviewService, *fakeReviewAudit) {
	audit := &fakeReviewAudit{}
	return NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, audit, nil, nil), audit
}

// TestUpdateStatusRefusesMergeAlways: MERGE is reached only by
// AdvanceMergeQueue promoting the queue head, never by a caller's PATCH.
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

// reconcilingReview wires a review sitting at status whose branch the
// verifier will answer for, so a test can drive reconcileMerged without a
// real remote.
func reconcilingReview(status model.ReviewStatus, verifier fakeMergeVerifier) (*fakeReviewRepo, *ReviewService) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", SourceBranch: "bug/thing", Status: status})
	svc := NewReviewService(reviews, &fakeReviewBuilds{},
		&fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{}, verifier, nil)
	return reviews, svc
}

// TestReconcileMergedAcceptsALandedBranchFromAnyOpenStatus is the transition
// erun#2575 is about: a review whose work landed without the queue — a GitHub
// squash merge — reaches MERGED from wherever it was sitting, on the
// verifier's word that the branch's changes really are in the target. Before
// this it could never move at all, so it sat OPEN forever and made the OPEN
// count grow by one for every change that landed that way.
func TestReconcileMergedAcceptsALandedBranchFromAnyOpenStatus(t *testing.T) {
	for _, status := range []model.ReviewStatus{model.ReviewStatusOpen, model.ReviewStatusReady, model.ReviewStatusFailed} {
		t.Run(string(status), func(t *testing.T) {
			reviews, svc := reconcilingReview(status, fakeMergeVerifier{contained: true, landingCommit: "squash-1"})

			updated, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "", "file:///remote.git")
			if err != nil {
				t.Fatalf("UpdateStatus(MERGED) from %s error = %v, want it reconciled", status, err)
			}
			if updated.Status != model.ReviewStatusMerged {
				t.Fatalf("returned status = %s, want MERGED", updated.Status)
			}
			if got := reviews.reviews["review-1"].Status; got != model.ReviewStatusMerged {
				t.Fatalf("persisted status = %s, want MERGED", got)
			}
			if got := reviews.reviews["review-1"].LastMergedBuildID; got != "" {
				t.Fatalf("lastMergedBuildId = %q, want it empty: a reconciliation has no build", got)
			}
		})
	}
}

// TestReconcileMergedRefusesWhenTheBranchIsNotInTheTarget: the reconciliation
// verifies rather than believes — a branch that did not land is refused, and
// the review is left exactly where it was.
func TestReconcileMergedRefusesWhenTheBranchIsNotInTheTarget(t *testing.T) {
	reviews, svc := reconcilingReview(model.ReviewStatusOpen, fakeMergeVerifier{contained: false})

	_, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "", "file:///remote.git")
	var notVerified *MergeNotVerifiedError
	if !errors.As(err, &notVerified) {
		t.Fatalf("UpdateStatus(MERGED) error = %v, want *MergeNotVerifiedError", err)
	}
	if !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("error = %v, want it to unwrap to ErrInvalidInput", err)
	}
	if got := reviews.reviews["review-1"].Status; got != model.ReviewStatusOpen {
		t.Fatalf("status = %s, want the review left at OPEN, not moved to MERGED", got)
	}
}

// TestReconcileMergedRefusesWithNoVerifier: without a way to check the real
// repository there is nothing to reconcile against, the same fail-closed
// stance verifyRepositoryState takes for a queue-driven merge.
func TestReconcileMergedRefusesWithNoVerifier(t *testing.T) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", SourceBranch: "bug/thing", Status: model.ReviewStatusOpen})
	svc := NewReviewService(reviews, &fakeReviewBuilds{},
		&fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{}, nil, nil)

	_, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "", "")
	var notVerified *MergeNotVerifiedError
	if !errors.As(err, &notVerified) {
		t.Fatalf("UpdateStatus(MERGED) error = %v, want *MergeNotVerifiedError", err)
	}
	if got := reviews.reviews["review-1"].Status; got != model.ReviewStatusOpen {
		t.Fatalf("status = %s, want the review left at OPEN", got)
	}
}

// TestReconcileMergedRefusesAClosedReview: CLOSED is terminal — reconciliation
// is for the review that is stuck open, not for reopening a decision already
// made.
func TestReconcileMergedRefusesAClosedReview(t *testing.T) {
	reviews, svc := reconcilingReview(model.ReviewStatusClosed, fakeMergeVerifier{contained: true})

	_, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "", "file:///remote.git")
	var invalidTransition *InvalidTransitionError
	if !errors.As(err, &invalidTransition) {
		t.Fatalf("UpdateStatus(MERGED) error = %v, want *InvalidTransitionError", err)
	}
	if got := reviews.reviews["review-1"].Status; got != model.ReviewStatusClosed {
		t.Fatalf("status = %s, want the review left at CLOSED", got)
	}
}

// mergingReviewWithGateBuild sets up a review sitting at MERGE with a
// successful GATE build already recorded against it — the state every
// acceptMerged test starts from, so each one only has to vary the one
// condition it means to exercise.
func mergingReviewWithGateBuild(commit string) (*fakeReviewRepo, *fakeReviewBuilds) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", Status: model.ReviewStatusMerge})
	builds := &fakeReviewBuilds{builds: map[string]model.Build{
		"gate-1": {BuildID: "gate-1", ReviewID: "review-1", Kind: model.BuildKindGate, Successful: true, CommitID: commit},
	}}
	return reviews, builds
}

// TestAcceptMergedRefusesWhenCommitIsNotOnTheTargetBranch is refusal
// condition 1: a reported merge commit the verifier cannot find on
// origin/<targetBranch> at all must never become MERGED, however the caller
// asserts it.
func TestAcceptMergedRefusesWhenCommitIsNotOnTheTargetBranch(t *testing.T) {
	reviews, builds := mergingReviewWithGateBuild("merge-commit")
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranch: false}, nil)

	_, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "gate-1", "file:///remote.git")

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

// mergingReviewGatedAgainst wires up mergingReviewWithGateBuild plus a prior
// MERGED review on the same branch at commit "real-tip" — the target tip
// this review's gate build has to descend from.
func mergingReviewGatedAgainst(commit string) (*fakeReviewRepo, *fakeReviewBuilds) {
	reviews, builds := mergingReviewWithGateBuild(commit)
	priorMerge := model.Review{ReviewID: "review-0", TargetBranch: "main", Status: model.ReviewStatusMerged, LastMergedBuildID: "gate-0"}
	reviews.reviews["review-0"] = &priorMerge
	builds.builds["gate-0"] = model.Build{BuildID: "gate-0", ReviewID: "review-0", Kind: model.BuildKindGate, Successful: true, CommitID: "real-tip"}
	return reviews, builds
}

// TestAcceptMergedRefusesWhenGatedTipIsNotAnAncestorOfTheReportedCommit is
// refusal condition 2: the reported commit is on the branch, but the target
// tip this review was gated against (the previous MERGED review's own merge
// commit on the same branch) is not really its ancestor — proof the merge
// was built against a history that never included the gated work, such as a
// force-push replacing it.
func TestAcceptMergedRefusesWhenGatedTipIsNotAnAncestorOfTheReportedCommit(t *testing.T) {
	reviews, builds := mergingReviewGatedAgainst("merge-commit")
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranch: true, parent: "wrong-parent", isAncestor: false}, nil)

	_, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "gate-1", "file:///remote.git")

	var notVerified *MergeNotVerifiedError
	if !errors.As(err, &notVerified) {
		t.Fatalf("UpdateStatus(MERGED) error = %v, want *MergeNotVerifiedError", err)
	}
	if got := reviews.reviews["review-1"].Status; got != model.ReviewStatusMerge {
		t.Fatalf("status = %s, want the review left at MERGE, not moved to MERGED", got)
	}
}

// TestAcceptMergedSucceedsWhenParentIsTheGatedTip is the ordinary,
// no-regression case: the reported commit's own parent is exactly the
// target tip this review was gated against.
func TestAcceptMergedSucceedsWhenParentIsTheGatedTip(t *testing.T) {
	reviews, builds := mergingReviewGatedAgainst("merge-commit")
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranch: true, parent: "real-tip", isAncestor: true}, nil)

	updated, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "gate-1", "file:///remote.git")
	if err != nil {
		t.Fatalf("UpdateStatus(MERGED): %v", err)
	}
	if updated.Status != model.ReviewStatusMerged {
		t.Fatalf("status = %s, want MERGED", updated.Status)
	}
}

// TestAcceptMergedSucceedsWhenUnrelatedCommitsLandedBetweenGatingAndReporting:
// commits that arrived directly on the target branch between the gated tip
// and the reported commit — the release flow's `[skip ci]` pushes, for
// instance — mean the reported commit's immediate parent is no longer the
// gated tip, but the gated work is still really its ancestor, so the merge is
// accepted rather than permanently refused.
func TestAcceptMergedSucceedsWhenUnrelatedCommitsLandedBetweenGatingAndReporting(t *testing.T) {
	reviews, builds := mergingReviewGatedAgainst("merge-commit")
	release := &fakeReleaseTrigger{}
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranch: true, parent: "release-prepare-commit", isAncestor: true}, release)

	updated, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "gate-1", "file:///remote.git")
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

// TestAcceptMergedRefusesWithNoSuccessfulGateBuildRecorded is refusal
// condition 3: even a commit that really is on the target branch with the
// right parent must not become MERGED unless a successful GATE build is
// actually recorded for it — a buildId pointing at a failed build is not a
// gate that passed.
func TestAcceptMergedRefusesWithNoSuccessfulGateBuildRecorded(t *testing.T) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", Status: model.ReviewStatusMerge})
	builds := &fakeReviewBuilds{builds: map[string]model.Build{
		"gate-1": {BuildID: "gate-1", ReviewID: "review-1", Kind: model.BuildKindGate, Successful: false, CommitID: "merge-commit"},
	}}
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranch: true}, nil)

	_, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "gate-1", "file:///remote.git")

	var notVerified *MergeNotVerifiedError
	if !errors.As(err, &notVerified) {
		t.Fatalf("UpdateStatus(MERGED) error = %v, want *MergeNotVerifiedError", err)
	}
	if got := reviews.reviews["review-1"].Status; got != model.ReviewStatusMerge {
		t.Fatalf("status = %s, want the review left at MERGE, not moved to MERGED", got)
	}
}

// TestAcceptMergedSucceedsWhenAllThreeConditionsHold proves the checks above
// are not just refusing everything: a genuinely verified merge does become
// MERGED and does trigger the release it earned.
func TestAcceptMergedSucceedsWhenAllThreeConditionsHold(t *testing.T) {
	reviews, builds := mergingReviewWithGateBuild("merge-commit")
	release := &fakeReleaseTrigger{}
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranch: true, parent: ""}, release)

	updated, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "gate-1", "file:///remote.git")
	if err != nil {
		t.Fatalf("UpdateStatus(MERGED): %v", err)
	}
	if updated.Status != model.ReviewStatusMerged || updated.LastMergedBuildID != "gate-1" {
		t.Fatalf("updated = %+v, want MERGED with lastMergedBuildId=gate-1", updated)
	}
	if len(release.requests) != 1 || release.requests[0].CommitID != "merge-commit" {
		t.Fatalf("release requests = %+v, want one for merge-commit", release.requests)
	}
}

// TestMarkBuildResultPromotesTheQueueHeadNotTheBuiltReview: a build succeeding
// for one review can promote a *different* review — whichever is at the head
// of that target branch's queue. Every caller of MarkBuildResult has to
// dispatch the review it actually returns, not assume it is the one the build
// belonged to.
func TestMarkBuildResultPromotesTheQueueHeadNotTheBuiltReview(t *testing.T) {
	reviews := newFakeReviewRepo(
		model.Review{ReviewID: "review-head", TargetBranch: "main", Status: model.ReviewStatusReady},
		model.Review{ReviewID: "review-built", TargetBranch: "main", Status: model.ReviewStatusOpen},
	)
	// review-head is already queued, ahead of the review this build is for.
	reviews.queue = []model.ReviewMergeQueueEntry{{ReviewMergeQueueID: 1, TargetBranch: "main", ReviewID: "review-head"}}
	reviews.nextID = 1
	svc, _ := newTestReviewService(reviews, &fakeReviewBuilds{})

	promoted, ok, err := svc.MarkBuildResult(context.Background(), "review-built", "build-1", true)
	if err != nil {
		t.Fatalf("MarkBuildResult: %v", err)
	}
	if !ok {
		t.Fatal("MarkBuildResult reported no promotion, want review-head promoted")
	}
	if promoted.ReviewID != "review-head" {
		t.Fatalf("promoted review = %q, want the queue head review-head", promoted.ReviewID)
	}
	if reviews.reviews["review-head"].Status != model.ReviewStatusMerge {
		t.Fatalf("review-head status = %s, want MERGE", reviews.reviews["review-head"].Status)
	}
	if reviews.reviews["review-built"].Status != model.ReviewStatusReady {
		t.Fatalf("review-built status = %s, want READY (queued behind review-head)", reviews.reviews["review-built"].Status)
	}
}

// TestMarkBuildResultFailureDequeuesAndPromotesNothing: a failed build must
// fail its own review and never report a promotion.
func TestMarkBuildResultFailureDequeuesAndPromotesNothing(t *testing.T) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", Status: model.ReviewStatusMerge})
	reviews.queue = []model.ReviewMergeQueueEntry{{ReviewMergeQueueID: 1, TargetBranch: "main", ReviewID: "review-1"}}
	svc, _ := newTestReviewService(reviews, &fakeReviewBuilds{})

	_, ok, err := svc.MarkBuildResult(context.Background(), "review-1", "build-1", false)
	if err != nil {
		t.Fatalf("MarkBuildResult: %v", err)
	}
	if ok {
		t.Fatal("a failed build reported a promotion")
	}
	if reviews.reviews["review-1"].Status != model.ReviewStatusFailed {
		t.Fatalf("status = %s, want FAILED", reviews.reviews["review-1"].Status)
	}
	if len(reviews.queue) != 0 {
		t.Fatalf("queue = %+v, want the failed review removed", reviews.queue)
	}
}

// TestAdvanceMergeQueueRefusesWhileAnotherReviewIsMerging is the invariant the
// gate's serialisation depends on: only one review may be MERGE per target
// branch at a time. The refusal must name the review holding the slot — which
// review it is decides the operator's next move (finish it, or requeue it back
// to READY), and a bare not-found sends them looking for a missing endpoint, a
// deleted review, or a mistyped branch instead.
func TestAdvanceMergeQueueRefusesWhileAnotherReviewIsMerging(t *testing.T) {
	reviews := newFakeReviewRepo(
		model.Review{ReviewID: "review-merging", Name: "Land the widget", SourceBranch: "feature/widget", TargetBranch: "main", Status: model.ReviewStatusMerge},
		model.Review{ReviewID: "review-queued", TargetBranch: "main", Status: model.ReviewStatusReady},
	)
	reviews.queue = []model.ReviewMergeQueueEntry{{ReviewMergeQueueID: 1, TargetBranch: "main", ReviewID: "review-queued"}}
	svc, _ := newTestReviewService(reviews, &fakeReviewBuilds{})

	_, err := svc.AdvanceMergeQueue(context.Background(), "", "main")
	var occupied *MergeQueueOccupiedError
	if !errors.As(err, &occupied) {
		t.Fatalf("AdvanceMergeQueue while another review is merging: err = %v, want *MergeQueueOccupiedError", err)
	}
	if occupied.TargetBranch != "main" || occupied.ReviewID != "review-merging" ||
		occupied.Name != "Land the widget" || occupied.SourceBranch != "feature/widget" {
		t.Fatalf("occupied = %+v, want the review already at MERGE on main (review-merging, Land the widget, feature/widget)", occupied)
	}
	if reviews.reviews["review-queued"].Status != model.ReviewStatusReady {
		t.Fatalf("review-queued status = %s, want unchanged READY", reviews.reviews["review-queued"].Status)
	}
}

// TestOverrideAdvanceMergeQueueRefusesWhileAnotherReviewIsMerging pins the
// override to the same occupancy rule: bypassing the thread gate does not
// bypass the one-MERGE-per-branch invariant, and its refusal names the same
// blocking review rather than falling through to a not-found.
func TestOverrideAdvanceMergeQueueRefusesWhileAnotherReviewIsMerging(t *testing.T) {
	reviews := newFakeReviewRepo(
		model.Review{ReviewID: "review-merging", Name: "Land the widget", SourceBranch: "feature/widget", TargetBranch: "main", Status: model.ReviewStatusMerge},
		model.Review{ReviewID: "review-queued", TargetBranch: "main", Status: model.ReviewStatusReady},
	)
	reviews.queue = []model.ReviewMergeQueueEntry{{ReviewMergeQueueID: 1, TargetBranch: "main", ReviewID: "review-queued"}}
	svc, _ := newTestReviewService(reviews, &fakeReviewBuilds{})

	_, err := svc.OverrideAdvanceMergeQueue(context.Background(), "", "main", "hotfix, reviewers unavailable")
	var occupied *MergeQueueOccupiedError
	if !errors.As(err, &occupied) {
		t.Fatalf("OverrideAdvanceMergeQueue while another review is merging: err = %v, want *MergeQueueOccupiedError", err)
	}
	if occupied.ReviewID != "review-merging" || occupied.TargetBranch != "main" {
		t.Fatalf("occupied = %+v, want review-merging on main", occupied)
	}
}

// TestAdvanceMergeQueueRefusesUnresolvedThreads: a review with an open
// comment thread must not advance, and the refusal must name how many threads
// and which review, not just "cannot advance".
func TestAdvanceMergeQueueRefusesUnresolvedThreads(t *testing.T) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", Status: model.ReviewStatusReady})
	reviews.queue = []model.ReviewMergeQueueEntry{{ReviewMergeQueueID: 1, TargetBranch: "main", ReviewID: "review-1"}}
	comments := &fakeReviewComments{byReview: map[string][]model.Comment{
		"review-1": {{CommentID: "c1", ReviewID: "review-1", Status: model.CommentStatusOpen}},
	}}
	svc := NewReviewService(reviews, &fakeReviewBuilds{}, comments, &fakeReviewAudit{}, nil, nil)

	_, err := svc.AdvanceMergeQueue(context.Background(), "", "main")
	var blocked *UnresolvedThreadsError
	if !errors.As(err, &blocked) {
		t.Fatalf("AdvanceMergeQueue error = %v, want *UnresolvedThreadsError", err)
	}
	if blocked.ReviewID != "review-1" || blocked.UnresolvedThreads != 1 {
		t.Fatalf("blocked = %+v, want {ReviewID: review-1, UnresolvedThreads: 1}", blocked)
	}
	if reviews.reviews["review-1"].Status != model.ReviewStatusReady {
		t.Fatalf("review-1 status = %s, want unchanged READY", reviews.reviews["review-1"].Status)
	}
	if len(reviews.queue) != 1 {
		t.Fatalf("queue = %+v, want review-1 still queued", reviews.queue)
	}
}

// TestAdvanceMergeQueuePromotesWhenAllThreadsResolved is the regression the
// gate is most likely to introduce: a review with every thread resolved (or
// whose only open comment is a reply, which never carries its own status)
// must still advance exactly as it did before the gate existed.
func TestAdvanceMergeQueuePromotesWhenAllThreadsResolved(t *testing.T) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", Status: model.ReviewStatusReady})
	reviews.queue = []model.ReviewMergeQueueEntry{{ReviewMergeQueueID: 1, TargetBranch: "main", ReviewID: "review-1"}}
	comments := &fakeReviewComments{byReview: map[string][]model.Comment{
		"review-1": {
			{CommentID: "c1", ReviewID: "review-1", Status: model.CommentStatusClosed},
			// A reply on the resolved thread; replies never carry their own
			// status, so an OPEN one here must not count as unresolved.
			{CommentID: "c2", ReviewID: "review-1", ParentCommentID: "c1", Status: model.CommentStatusOpen},
		},
	}}
	svc := NewReviewService(reviews, &fakeReviewBuilds{}, comments, &fakeReviewAudit{}, nil, nil)

	promoted, err := svc.AdvanceMergeQueue(context.Background(), "", "main")
	if err != nil {
		t.Fatalf("AdvanceMergeQueue with all threads resolved: %v", err)
	}
	if promoted.Status != model.ReviewStatusMerge {
		t.Fatalf("promoted status = %s, want MERGE", promoted.Status)
	}
	if len(reviews.queue) != 0 {
		t.Fatalf("queue = %+v, want review-1 dequeued", reviews.queue)
	}
}

// TestMarkBuildResultToleratesQueueHeadWithUnresolvedThreads guards the
// regression the gate would otherwise introduce into build reporting: the
// queue head being blocked belongs to a different review than the one whose
// build just succeeded, so reporting that build must not fail.
func TestMarkBuildResultToleratesQueueHeadWithUnresolvedThreads(t *testing.T) {
	reviews := newFakeReviewRepo(
		model.Review{ReviewID: "review-head", TargetBranch: "main", Status: model.ReviewStatusReady},
		model.Review{ReviewID: "review-built", TargetBranch: "main", Status: model.ReviewStatusOpen},
	)
	reviews.queue = []model.ReviewMergeQueueEntry{{ReviewMergeQueueID: 1, TargetBranch: "main", ReviewID: "review-head"}}
	reviews.nextID = 1
	comments := &fakeReviewComments{byReview: map[string][]model.Comment{
		"review-head": {{CommentID: "c1", ReviewID: "review-head", Status: model.CommentStatusOpen}},
	}}
	svc := NewReviewService(reviews, &fakeReviewBuilds{}, comments, &fakeReviewAudit{}, nil, nil)

	_, ok, err := svc.MarkBuildResult(context.Background(), "review-built", "build-1", true)
	if err != nil {
		t.Fatalf("MarkBuildResult: %v, want no error even though the queue head is gated", err)
	}
	if ok {
		t.Fatal("MarkBuildResult reported a promotion despite the queue head being blocked by unresolved threads")
	}
	if reviews.reviews["review-head"].Status != model.ReviewStatusReady {
		t.Fatalf("review-head status = %s, want unchanged READY (still blocked, still queued)", reviews.reviews["review-head"].Status)
	}
	if reviews.reviews["review-built"].Status != model.ReviewStatusReady {
		t.Fatalf("review-built status = %s, want READY (its own build succeeded and it queued normally)", reviews.reviews["review-built"].Status)
	}
}

// TestMarkBuildResultToleratesAnotherReviewMerging guards the occupied-slot
// refusal on the build-reporting path: the review already at MERGE is not
// necessarily the one whose build just succeeded, so its occupancy must not
// fail that report any more than a gated queue head does.
func TestMarkBuildResultToleratesAnotherReviewMerging(t *testing.T) {
	reviews := newFakeReviewRepo(
		model.Review{ReviewID: "review-merging", TargetBranch: "main", Status: model.ReviewStatusMerge},
		model.Review{ReviewID: "review-built", TargetBranch: "main", Status: model.ReviewStatusOpen},
	)
	svc, _ := newTestReviewService(reviews, &fakeReviewBuilds{})

	_, ok, err := svc.MarkBuildResult(context.Background(), "review-built", "build-1", true)
	if err != nil {
		t.Fatalf("MarkBuildResult: %v, want no error even though another review holds MERGE", err)
	}
	if ok {
		t.Fatal("MarkBuildResult reported a promotion while another review holds MERGE")
	}
	if reviews.reviews["review-built"].Status != model.ReviewStatusReady {
		t.Fatalf("review-built status = %s, want READY (its own build succeeded and it queued normally)", reviews.reviews["review-built"].Status)
	}
}

// TestMarkBuildResultToleratesAnAmbiguousQueue is the same shape of
// non-failure for the refusal that names no repository: the build is already
// recorded and its review already READY by the time the promotion is
// attempted, and the ambiguity is about which repository's queue an
// unfiltered promotion would be for. Failing the report would tell a caller
// their build did not land when it did, and a retry would record a second one.
func TestMarkBuildResultToleratesAnAmbiguousQueue(t *testing.T) {
	reviews := newFakeReviewRepo(
		model.Review{ReviewID: "review-erun", Repository: "https://github.com/sophium/erun", TargetBranch: "main", Status: model.ReviewStatusReady},
		model.Review{ReviewID: "review-other", Repository: "https://github.com/sophium/other", TargetBranch: "main", Status: model.ReviewStatusReady},
		model.Review{ReviewID: "review-built", TargetBranch: "main", Status: model.ReviewStatusOpen},
	)
	reviews.queue = []model.ReviewMergeQueueEntry{
		{ReviewMergeQueueID: 1, TargetBranch: "main", ReviewID: "review-erun"},
		{ReviewMergeQueueID: 2, TargetBranch: "main", ReviewID: "review-other"},
	}
	svc, _ := newTestReviewService(reviews, &fakeReviewBuilds{})

	_, ok, err := svc.MarkBuildResult(context.Background(), "review-built", "build-1", true)
	if err != nil {
		t.Fatalf("MarkBuildResult: %v, want no error even though the queue is several repositories'", err)
	}
	if ok {
		t.Fatal("MarkBuildResult reported a promotion from a queue no repository was named for")
	}
	if reviews.reviews["review-built"].Status != model.ReviewStatusReady {
		t.Fatalf("review-built status = %s, want READY (its own build succeeded and it queued normally)", reviews.reviews["review-built"].Status)
	}
}

// TestRequeueRefusesAReviewThatIsNotMerging: the missed-merge-window requeue
// only recovers a review holding the queue's slot. The review was resolved by
// id, so it exists and the caller can see it — the refusal names the status it
// actually holds rather than reporting a not-found for a review in plain sight.
func TestRequeueRefusesAReviewThatIsNotMerging(t *testing.T) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", Status: model.ReviewStatusReady})
	svc, _ := newTestReviewService(reviews, &fakeReviewBuilds{})

	_, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusReady, "", "")
	var notMerging *ReviewNotMergingError
	if !errors.As(err, &notMerging) {
		t.Fatalf("requeue of a READY review: err = %v, want *ReviewNotMergingError", err)
	}
	if notMerging.ReviewID != "review-1" || notMerging.Status != model.ReviewStatusReady {
		t.Fatalf("notMerging = %+v, want review-1 at READY", notMerging)
	}
	if reviews.reviews["review-1"].Status != model.ReviewStatusReady {
		t.Fatalf("status = %s, want unchanged READY", reviews.reviews["review-1"].Status)
	}
}

// TestRequeueMovesAMergingReviewBackToReady is the recovery the occupied-slot
// refusal points an operator at: a review stuck at MERGE returns to READY and
// rejoins its target branch's queue, freeing the slot for the next advance.
func TestRequeueMovesAMergingReviewBackToReady(t *testing.T) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", Status: model.ReviewStatusMerge})
	svc, _ := newTestReviewService(reviews, &fakeReviewBuilds{})

	updated, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusReady, "", "")
	if err != nil {
		t.Fatalf("requeue of a MERGE review: %v", err)
	}
	if updated.Status != model.ReviewStatusReady {
		t.Fatalf("status = %s, want READY", updated.Status)
	}
	if len(reviews.queue) != 1 || reviews.queue[0].ReviewID != "review-1" {
		t.Fatalf("queue = %+v, want review-1 re-enqueued", reviews.queue)
	}
}

// TestOverrideAdvanceMergeQueueRequiresReason: the override is the one
// deliberate escape from the gate, and a blank reason is a quiet bypass, not
// a deliberate one.
func TestOverrideAdvanceMergeQueueRequiresReason(t *testing.T) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", Status: model.ReviewStatusReady})
	reviews.queue = []model.ReviewMergeQueueEntry{{ReviewMergeQueueID: 1, TargetBranch: "main", ReviewID: "review-1"}}
	audit := &fakeReviewAudit{}
	svc := NewReviewService(reviews, &fakeReviewBuilds{}, &fakeReviewComments{byReview: map[string][]model.Comment{}}, audit, nil, nil)

	for _, reason := range []string{"", "   "} {
		if _, err := svc.OverrideAdvanceMergeQueue(context.Background(), "", "main", reason); !errors.Is(err, repository.ErrInvalidInput) {
			t.Fatalf("OverrideAdvanceMergeQueue(reason=%q) error = %v, want ErrInvalidInput", reason, err)
		}
	}
	if reviews.reviews["review-1"].Status != model.ReviewStatusReady {
		t.Fatalf("review-1 status = %s, want unchanged: a refused override must not promote", reviews.reviews["review-1"].Status)
	}
	if len(audit.events) != 0 {
		t.Fatalf("audit events = %d, want 0 for a refused override", len(audit.events))
	}
}

// TestOverrideAdvanceMergeQueueBypassesGateAndAudits proves the override
// actually gets past the gate the previous tests confirm is otherwise
// enforced, and that doing so leaves a durable, reason-carrying audit record
// rather than a quiet bypass.
func TestOverrideAdvanceMergeQueueBypassesGateAndAudits(t *testing.T) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", Status: model.ReviewStatusReady})
	reviews.queue = []model.ReviewMergeQueueEntry{{ReviewMergeQueueID: 1, TargetBranch: "main", ReviewID: "review-1"}}
	comments := &fakeReviewComments{byReview: map[string][]model.Comment{
		"review-1": {{CommentID: "c1", ReviewID: "review-1", Status: model.CommentStatusOpen}},
	}}
	audit := &fakeReviewAudit{}
	svc := NewReviewService(reviews, &fakeReviewBuilds{}, comments, audit, nil, nil)
	ctx := security.WithContext(context.Background(), security.Context{
		TenantID: "tenant-1", ErunUserID: "user-1", ExternalIssuer: "https://issuer.example", ExternalUserID: "sub-1",
	})

	promoted, err := svc.OverrideAdvanceMergeQueue(ctx, "", "main", "hotfix, reviewers unavailable")
	if err != nil {
		t.Fatalf("OverrideAdvanceMergeQueue: %v", err)
	}
	if promoted.Status != model.ReviewStatusMerge {
		t.Fatalf("promoted status = %s, want MERGE despite the unresolved thread", promoted.Status)
	}
	if len(audit.events) != 1 {
		t.Fatalf("audit events = %d, want exactly 1", len(audit.events))
	}
	event := audit.events[0]
	if event.Type != model.AuditEventTypeAPI || event.APIPath != overrideAdvanceMergeQueueAPIPath {
		t.Fatalf("audit event type/path = %s %s, want API %s", event.Type, event.APIPath, overrideAdvanceMergeQueueAPIPath)
	}
	if event.TenantID != "tenant-1" || event.ErunUserID != "user-1" {
		t.Fatalf("audit event identity = tenant=%s user=%s, want the overriding caller's own", event.TenantID, event.ErunUserID)
	}
	if !strings.Contains(event.APIParameters, "hotfix, reviewers unavailable") || !strings.Contains(event.APIParameters, "review-1") {
		t.Fatalf("audit event parameters = %q, want the reason and the review id", event.APIParameters)
	}
}

// TestOverrideAdvanceMergeQueueFailsClosedWithoutAuditLogger: an override with
// nowhere to record its reason must refuse, not promote unaudited.
func TestOverrideAdvanceMergeQueueFailsClosedWithoutAuditLogger(t *testing.T) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", Status: model.ReviewStatusReady})
	reviews.queue = []model.ReviewMergeQueueEntry{{ReviewMergeQueueID: 1, TargetBranch: "main", ReviewID: "review-1"}}
	svc := NewReviewService(reviews, &fakeReviewBuilds{}, &fakeReviewComments{byReview: map[string][]model.Comment{}}, nil, nil, nil)

	if _, err := svc.OverrideAdvanceMergeQueue(context.Background(), "", "main", "reason"); err == nil {
		t.Fatal("OverrideAdvanceMergeQueue with no audit logger configured: want an error, got nil")
	}
	if reviews.reviews["review-1"].Status != model.ReviewStatusReady {
		t.Fatalf("review-1 status = %s, want unchanged: an unauditable override must not promote", reviews.reviews["review-1"].Status)
	}
}

// TestOverrideAdvanceMergeQueueRequiresSecurityContext: the audit record needs
// a caller identity to be worth anything; a request that somehow reached the
// service with none must refuse rather than log an anonymous override.
func TestOverrideAdvanceMergeQueueRequiresSecurityContext(t *testing.T) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", Status: model.ReviewStatusReady})
	reviews.queue = []model.ReviewMergeQueueEntry{{ReviewMergeQueueID: 1, TargetBranch: "main", ReviewID: "review-1"}}
	svc := NewReviewService(reviews, &fakeReviewBuilds{}, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{}, nil, nil)

	if _, err := svc.OverrideAdvanceMergeQueue(context.Background(), "", "main", "reason"); !errors.Is(err, repository.ErrMissingSecurityContext) {
		t.Fatalf("error = %v, want ErrMissingSecurityContext", err)
	}
	if reviews.reviews["review-1"].Status != model.ReviewStatusReady {
		t.Fatalf("review-1 status = %s, want unchanged", reviews.reviews["review-1"].Status)
	}
}

// The reported failure: a review recorded no repository, so a tenant serving
// two repositories had one queue per target branch and a promotion took
// whichever review happened to sort first. The gate then ran in the wrong
// repository's checkout — against a branch that need not exist there — and
// contributed nothing, which the gate reports as a skip rather than a
// failure. These two cases are the two halves of that: naming the repository
// takes the right head, and naming none refuses rather than guessing.
func TestAdvanceMergeQueuePromotesTheNamedRepositorysOwnHead(t *testing.T) {
	reviews := newFakeReviewRepo(
		model.Review{ReviewID: "review-erun", Repository: "https://github.com/sophium/erun", TargetBranch: "main", Status: model.ReviewStatusReady},
		model.Review{ReviewID: "review-other", Repository: "https://github.com/sophium/other", TargetBranch: "main", Status: model.ReviewStatusReady},
	)
	// The other repository's review is queued first, so a promotion keyed on
	// the target branch alone would take it.
	reviews.queue = []model.ReviewMergeQueueEntry{
		{ReviewMergeQueueID: 1, TargetBranch: "main", ReviewID: "review-other"},
		{ReviewMergeQueueID: 2, TargetBranch: "main", ReviewID: "review-erun"},
	}
	svc, _ := newTestReviewService(reviews, &fakeReviewBuilds{})

	promoted, err := svc.AdvanceMergeQueue(context.Background(), "https://github.com/sophium/erun", "main")
	if err != nil {
		t.Fatalf("AdvanceMergeQueue: %v", err)
	}
	if promoted.ReviewID != "review-erun" {
		t.Fatalf("promoted %s, want review-erun: the queue of the repository named, not the first review queued for that branch", promoted.ReviewID)
	}
	if got := reviews.reviews["review-other"].Status; got != model.ReviewStatusReady {
		t.Fatalf("the other repository's review is %s, want it left READY and unqueued-from, not promoted", got)
	}
}

func TestAdvanceMergeQueueRefusesAQueueThatIsNotOneRepositorys(t *testing.T) {
	reviews := newFakeReviewRepo(
		model.Review{ReviewID: "review-erun", Repository: "https://github.com/sophium/erun", TargetBranch: "main", Status: model.ReviewStatusReady},
		model.Review{ReviewID: "review-other", Repository: "https://github.com/sophium/other", TargetBranch: "main", Status: model.ReviewStatusReady},
	)
	reviews.queue = []model.ReviewMergeQueueEntry{
		{ReviewMergeQueueID: 1, TargetBranch: "main", ReviewID: "review-other"},
		{ReviewMergeQueueID: 2, TargetBranch: "main", ReviewID: "review-erun"},
	}
	svc, _ := newTestReviewService(reviews, &fakeReviewBuilds{})

	_, err := svc.AdvanceMergeQueue(context.Background(), "", "main")
	var ambiguous *AmbiguousMergeQueueError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("AdvanceMergeQueue with no repository: error = %v, want *AmbiguousMergeQueueError", err)
	}
	for _, want := range []string{"https://github.com/sophium/erun", "https://github.com/sophium/other"} {
		if !strings.Contains(ambiguous.Error(), want) {
			t.Fatalf("refusal = %q, want it to name %s so the caller can name one", ambiguous.Error(), want)
		}
	}
	for _, id := range []string{"review-erun", "review-other"} {
		if got := reviews.reviews[id].Status; got != model.ReviewStatusReady {
			t.Fatalf("%s is %s, want both left READY: an ambiguous queue promotes nothing", id, got)
		}
	}
}

// A queue holding reviews from one repository — including the single queue
// every review created before the platform recorded a repository shares —
// still promotes exactly as it always has: refusing it would strand work the
// platform has no other way to advance.
func TestAdvanceMergeQueuePromotesAnUnrecordedQueueThatIsStillOneRepository(t *testing.T) {
	reviews := newFakeReviewRepo(
		model.Review{ReviewID: "review-1", TargetBranch: "main", Status: model.ReviewStatusReady},
		model.Review{ReviewID: "review-2", TargetBranch: "main", Status: model.ReviewStatusReady},
	)
	reviews.queue = []model.ReviewMergeQueueEntry{
		{ReviewMergeQueueID: 1, TargetBranch: "main", ReviewID: "review-1"},
		{ReviewMergeQueueID: 2, TargetBranch: "main", ReviewID: "review-2"},
	}
	svc, _ := newTestReviewService(reviews, &fakeReviewBuilds{})

	promoted, err := svc.AdvanceMergeQueue(context.Background(), "", "main")
	if err != nil {
		t.Fatalf("AdvanceMergeQueue: %v", err)
	}
	if promoted.ReviewID != "review-1" {
		t.Fatalf("promoted %s, want the queue head review-1", promoted.ReviewID)
	}
}

// One named repository beside reviews created before the platform
// recorded a repository is one repository's queue, not two. Counting the
// absence as a repository refused a tenant's own queue, and the rows it
// refused to promote were the legacy ones — which are reachable here, in
// queue order, exactly as if they had recorded a repository.
func TestAdvanceMergeQueuePromotesAQueueOfOneRepositoryPlusUnrecordedRows(t *testing.T) {
	reviews := newFakeReviewRepo(
		model.Review{ReviewID: "review-legacy", TargetBranch: "main", Status: model.ReviewStatusReady},
		model.Review{ReviewID: "review-erun", Repository: "https://github.com/sophium/erun", TargetBranch: "main", Status: model.ReviewStatusReady},
	)
	reviews.queue = []model.ReviewMergeQueueEntry{
		{ReviewMergeQueueID: 1, TargetBranch: "main", ReviewID: "review-legacy"},
		{ReviewMergeQueueID: 2, TargetBranch: "main", ReviewID: "review-erun"},
	}
	svc, _ := newTestReviewService(reviews, &fakeReviewBuilds{})

	promoted, err := svc.AdvanceMergeQueue(context.Background(), "", "main")
	if err != nil {
		t.Fatalf("AdvanceMergeQueue over one repository plus unrecorded rows: %v", err)
	}
	if promoted.ReviewID != "review-legacy" {
		t.Fatalf("promoted %s, want the queue head review-legacy: an unrecorded row is still in the queue it was queued in", promoted.ReviewID)
	}
}

// A queue that really is several repositories' still refuses, and now names
// the rows it cannot attribute: they belong to none of the named
// repositories, so a caller told only the repositories would have to work out
// for itself which rows are stuck.
func TestAdvanceMergeQueueNamesTheRowsItCannotAttribute(t *testing.T) {
	reviews := newFakeReviewRepo(
		model.Review{ReviewID: "review-erun", Repository: "https://github.com/sophium/erun", TargetBranch: "main", Status: model.ReviewStatusReady},
		model.Review{ReviewID: "review-other", Repository: "https://github.com/sophium/other", TargetBranch: "main", Status: model.ReviewStatusReady},
		model.Review{ReviewID: "review-legacy", TargetBranch: "main", Status: model.ReviewStatusReady},
	)
	reviews.queue = []model.ReviewMergeQueueEntry{
		{ReviewMergeQueueID: 1, TargetBranch: "main", ReviewID: "review-erun"},
		{ReviewMergeQueueID: 2, TargetBranch: "main", ReviewID: "review-other"},
		{ReviewMergeQueueID: 3, TargetBranch: "main", ReviewID: "review-legacy"},
	}
	svc, _ := newTestReviewService(reviews, &fakeReviewBuilds{})

	_, err := svc.AdvanceMergeQueue(context.Background(), "", "main")
	var ambiguous *AmbiguousMergeQueueError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("AdvanceMergeQueue: error = %v, want *AmbiguousMergeQueueError", err)
	}
	if len(ambiguous.Repositories) != 2 {
		t.Fatalf("Repositories = %v, want the two named repositories and only those", ambiguous.Repositories)
	}
	if len(ambiguous.UnrecordedReviewIDs) != 1 || ambiguous.UnrecordedReviewIDs[0] != "review-legacy" {
		t.Fatalf("UnrecordedReviewIDs = %v, want review-legacy", ambiguous.UnrecordedReviewIDs)
	}
	if !strings.Contains(ambiguous.Error(), "review-legacy") {
		t.Fatalf("refusal = %q, want it to name the row it cannot attribute", ambiguous.Error())
	}
}

// The reported failure, second half: verification fetched whatever remote the
// reporter named, so a caller could have the platform confirm a commit
// against a repository the review has nothing to do with. Naming a different
// repository is now refused outright, and the reported identity is the
// canonicalized one — an SSH remote verifies against the HTTPS identity the
// review recorded.
func TestAcceptMergedRefusesARemoteForADifferentRepository(t *testing.T) {
	reviews, builds := mergingReviewWithGateBuild("merge-commit")
	reviews.reviews["review-1"].Repository = "https://github.com/sophium/erun"
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranch: true, parent: "real-tip", isAncestor: true}, nil)

	_, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "gate-1", "https://github.com/sophium/other")

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

// The SSH remote a checkout reports has to be the repository the review
// recorded over HTTPS, or every merge reported from an SSH clone would be
// refused for naming a repository it is not.
func TestAcceptMergedAcceptsAnSSHSpellingOfTheReviewsOwnRepository(t *testing.T) {
	reviews, builds := mergingReviewWithGateBuild("merge-commit")
	reviews.reviews["review-1"].Repository = "https://github.com/sophium/erun"
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranch: true, parent: "real-tip", isAncestor: true}, nil)

	updated, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "gate-1", "git@github.com:sophium/erun.git")
	if err != nil {
		t.Fatalf("UpdateStatus(MERGED): %v", err)
	}
	if updated.Repository != "https://github.com/sophium/erun" {
		t.Fatalf("repository = %q, want the review's own canonical identity", updated.Repository)
	}
}

// A review created before the platform recorded a repository has nothing to
// compare a reported remote against, so the report is what records it — the
// only moment the platform ever holds it.
func TestAcceptMergedRecordsTheRepositoryAnUnrecordedReviewWasReportedUnder(t *testing.T) {
	reviews, builds := mergingReviewWithGateBuild("merge-commit")
	svc := NewReviewService(reviews, builds, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{},
		fakeMergeVerifier{onBranch: true, parent: "real-tip", isAncestor: true}, nil)

	updated, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "gate-1", "git@github.com:sophium/erun.git")
	if err != nil {
		t.Fatalf("UpdateStatus(MERGED): %v", err)
	}
	if updated.Repository != "https://github.com/sophium/erun" {
		t.Fatalf("repository = %q, want the reported remote recorded as the review's identity", updated.Repository)
	}
}
