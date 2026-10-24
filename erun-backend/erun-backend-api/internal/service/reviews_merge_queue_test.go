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

func TestMarkBuildResultPromotesTheQueueHeadNotTheBuiltReview(t *testing.T) {
	reviews := newFakeReviewRepo(
		model.Review{ReviewID: "review-head", TargetBranch: "main", Status: model.ReviewStatusReady},
		model.Review{ReviewID: "review-built", TargetBranch: "main", Status: model.ReviewStatusOpen},
	)
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

func TestAdvanceMergeQueuePromotesWhenAllThreadsResolved(t *testing.T) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", Status: model.ReviewStatusReady})
	reviews.queue = []model.ReviewMergeQueueEntry{{ReviewMergeQueueID: 1, TargetBranch: "main", ReviewID: "review-1"}}
	comments := &fakeReviewComments{byReview: map[string][]model.Comment{
		"review-1": {
			{CommentID: "c1", ReviewID: "review-1", Status: model.CommentStatusClosed},
			// Replies never carry their own status, so an OPEN reply is not unresolved.
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

// Failing the report would make a retry record a second build.
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

// Naming the repository takes that repository's head; naming none refuses.
func TestAdvanceMergeQueuePromotesTheNamedRepositorysOwnHead(t *testing.T) {
	reviews := newFakeReviewRepo(
		model.Review{ReviewID: "review-erun", Repository: "https://github.com/sophium/erun", TargetBranch: "main", Status: model.ReviewStatusReady},
		model.Review{ReviewID: "review-other", Repository: "https://github.com/sophium/other", TargetBranch: "main", Status: model.ReviewStatusReady},
	)
	// Queued first, so a branch-only promotion would take it.
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
