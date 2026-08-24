package service

import (
	"context"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
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

// TestUpdateStatusRefusesMergeAndMerged is the API hole the issue reports:
// MERGED (and MERGE) must never be settable by a caller's PATCH. Only the
// merge queue's own gate result (MergeQueueService) may write them.
func TestUpdateStatusRefusesMergeAndMerged(t *testing.T) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", Status: model.ReviewStatusReady})
	svc := NewReviewService(reviews, &fakeReviewBuilds{})

	for _, status := range []model.ReviewStatus{model.ReviewStatusMerge, model.ReviewStatusMerged} {
		if _, err := svc.UpdateStatus(context.Background(), "review-1", status, "build-1"); err != repository.ErrInvalidInput {
			t.Fatalf("UpdateStatus(%s) error = %v, want ErrInvalidInput", status, err)
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
	svc := NewReviewService(reviews, &fakeReviewBuilds{})

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
	svc := NewReviewService(reviews, &fakeReviewBuilds{})

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
	svc := NewReviewService(reviews, &fakeReviewBuilds{})

	if _, err := svc.AdvanceMergeQueue(context.Background(), "main"); err != repository.ErrNotFound {
		t.Fatalf("AdvanceMergeQueue while another review is merging: err = %v, want ErrNotFound", err)
	}
	if reviews.reviews["review-queued"].Status != model.ReviewStatusReady {
		t.Fatalf("review-queued status = %s, want unchanged READY", reviews.reviews["review-queued"].Status)
	}
}
