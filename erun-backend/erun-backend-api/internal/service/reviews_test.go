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

func (f *fakeReviewRepo) FindNextMergeQueueReview(_ context.Context, targetBranch string) (model.Review, error) {
	for _, entry := range f.queue {
		if entry.TargetBranch != targetBranch {
			continue
		}
		if review, ok := f.reviews[entry.ReviewID]; ok && review.Status == model.ReviewStatusReady {
			return *review, nil
		}
	}
	return model.Review{}, repository.ErrNotFound
}

func (f *fakeReviewRepo) FindActiveMergeReview(_ context.Context, targetBranch string) (model.Review, error) {
	for _, review := range f.reviews {
		if review.TargetBranch == targetBranch && review.Status == model.ReviewStatusMerge {
			return *review, nil
		}
	}
	return model.Review{}, repository.ErrNotFound
}

// FindLastMergedReview picks any MERGED review on targetBranch: tests never
// have more than one, so insertion order does not matter here the way it
// does for the real query's ORDER BY.
func (f *fakeReviewRepo) FindLastMergedReview(_ context.Context, targetBranch string) (model.Review, error) {
	for _, review := range f.reviews {
		if review.TargetBranch == targetBranch && review.Status == model.ReviewStatusMerged {
			return *review, nil
		}
	}
	return model.Review{}, repository.ErrNotFound
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
	onBranch bool
	parent   string
	err      error
}

func (f fakeMergeVerifier) Contains(_ context.Context, _, _, _ string) (bool, string, error) {
	return f.onBranch, f.parent, f.err
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

// TestUpdateStatusRefusesMergeAndMerged is the API hole the issue reports:
// MERGED (and MERGE) must never be settable by a caller's PATCH. Only the
// merge queue's own gate result (MergeQueueService) may write them.
func TestUpdateStatusRefusesMergeAndMerged(t *testing.T) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", Status: model.ReviewStatusReady})
	svc, _ := newTestReviewService(reviews, &fakeReviewBuilds{})

	for _, status := range []model.ReviewStatus{model.ReviewStatusMerge, model.ReviewStatusMerged} {
		_, err := svc.UpdateStatus(context.Background(), "review-1", status, "build-1")
		var invalidTransition *InvalidTransitionError
		if !errors.As(err, &invalidTransition) {
			t.Fatalf("UpdateStatus(%s) error = %v, want *InvalidTransitionError", status, err)
		}
		if !errors.Is(err, repository.ErrInvalidInput) {
			t.Fatalf("UpdateStatus(%s) error = %v, want it to unwrap to ErrInvalidInput", status, err)
		}
		if invalidTransition.From != model.ReviewStatusReady || invalidTransition.To != status {
			t.Fatalf("InvalidTransitionError = %+v, want from READY to %s", invalidTransition, status)
		}
		if got := reviews.reviews["review-1"].Status; got != model.ReviewStatusReady {
			t.Fatalf("UpdateStatus(%s) changed the review to %s despite being refused", status, got)
		}
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
// branch at a time.
func TestAdvanceMergeQueueRefusesWhileAnotherReviewIsMerging(t *testing.T) {
	reviews := newFakeReviewRepo(
		model.Review{ReviewID: "review-merging", TargetBranch: "main", Status: model.ReviewStatusMerge},
		model.Review{ReviewID: "review-queued", TargetBranch: "main", Status: model.ReviewStatusReady},
	)
	reviews.queue = []model.ReviewMergeQueueEntry{{ReviewMergeQueueID: 1, TargetBranch: "main", ReviewID: "review-queued"}}
	svc, _ := newTestReviewService(reviews, &fakeReviewBuilds{})

	if _, err := svc.AdvanceMergeQueue(context.Background(), "main"); err != repository.ErrNotFound {
		t.Fatalf("AdvanceMergeQueue while another review is merging: err = %v, want ErrNotFound", err)
	}
	if reviews.reviews["review-queued"].Status != model.ReviewStatusReady {
		t.Fatalf("review-queued status = %s, want unchanged READY", reviews.reviews["review-queued"].Status)
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
	svc := NewReviewService(reviews, &fakeReviewBuilds{}, comments, &fakeReviewAudit{})

	_, err := svc.AdvanceMergeQueue(context.Background(), "main")
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
	svc := NewReviewService(reviews, &fakeReviewBuilds{}, comments, &fakeReviewAudit{})

	promoted, err := svc.AdvanceMergeQueue(context.Background(), "main")
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
	svc := NewReviewService(reviews, &fakeReviewBuilds{}, comments, &fakeReviewAudit{})

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

// TestOverrideAdvanceMergeQueueRequiresReason: the override is the one
// deliberate escape from the gate, and a blank reason is a quiet bypass, not
// a deliberate one.
func TestOverrideAdvanceMergeQueueRequiresReason(t *testing.T) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", Status: model.ReviewStatusReady})
	reviews.queue = []model.ReviewMergeQueueEntry{{ReviewMergeQueueID: 1, TargetBranch: "main", ReviewID: "review-1"}}
	audit := &fakeReviewAudit{}
	svc := NewReviewService(reviews, &fakeReviewBuilds{}, &fakeReviewComments{byReview: map[string][]model.Comment{}}, audit)

	for _, reason := range []string{"", "   "} {
		if _, err := svc.OverrideAdvanceMergeQueue(context.Background(), "main", reason); !errors.Is(err, repository.ErrInvalidInput) {
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
	svc := NewReviewService(reviews, &fakeReviewBuilds{}, comments, audit)
	ctx := security.WithContext(context.Background(), security.Context{
		TenantID: "tenant-1", ErunUserID: "user-1", ExternalIssuer: "https://issuer.example", ExternalUserID: "sub-1",
	})

	promoted, err := svc.OverrideAdvanceMergeQueue(ctx, "main", "hotfix, reviewers unavailable")
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
	svc := NewReviewService(reviews, &fakeReviewBuilds{}, &fakeReviewComments{byReview: map[string][]model.Comment{}}, nil)

	if _, err := svc.OverrideAdvanceMergeQueue(context.Background(), "main", "reason"); err == nil {
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
	svc := NewReviewService(reviews, &fakeReviewBuilds{}, &fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{})

	if _, err := svc.OverrideAdvanceMergeQueue(context.Background(), "main", "reason"); !errors.Is(err, repository.ErrMissingSecurityContext) {
		t.Fatalf("error = %v, want ErrMissingSecurityContext", err)
	}
	if reviews.reviews["review-1"].Status != model.ReviewStatusReady {
		t.Fatalf("review-1 status = %s, want unchanged", reviews.reviews["review-1"].Status)
	}
}
