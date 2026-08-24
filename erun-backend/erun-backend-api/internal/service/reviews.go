package service

import (
	"context"
	"errors"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

type ReviewRepository interface {
	Get(ctx context.Context, reviewID string) (model.Review, error)
	Update(ctx context.Context, review model.Review) (model.Review, error)
	FindNextMergeQueueReview(ctx context.Context, targetBranch string) (model.Review, error)
	FindActiveMergeReview(ctx context.Context, targetBranch string) (model.Review, error)
	CreateMergeQueueEntry(ctx context.Context, entry model.ReviewMergeQueueEntry) (model.ReviewMergeQueueEntry, error)
	DeleteMergeQueueEntryByReview(ctx context.Context, reviewID string) error
}

type ReviewBuildRepository interface {
	Get(ctx context.Context, buildID string) (model.Build, error)
}

type ReviewService struct {
	reviews ReviewRepository
	builds  ReviewBuildRepository
}

func NewReviewService(reviews ReviewRepository, builds ReviewBuildRepository) *ReviewService {
	return &ReviewService{reviews: reviews, builds: builds}
}

func (s *ReviewService) PrepareCreate(review model.Review) model.Review {
	if review.Status == "" {
		review.Status = model.ReviewStatusOpen
	}
	return review
}

func (s *ReviewService) AdvanceMergeQueue(ctx context.Context, targetBranch string) (model.Review, error) {
	if targetBranch == "" {
		return model.Review{}, repository.ErrInvalidInput
	}
	if _, err := s.reviews.FindActiveMergeReview(ctx, targetBranch); err == nil {
		return model.Review{}, repository.ErrNotFound
	} else if !errors.Is(err, repository.ErrNotFound) {
		return model.Review{}, err
	}

	review, err := s.reviews.FindNextMergeQueueReview(ctx, targetBranch)
	if err != nil {
		return model.Review{}, err
	}
	if err := s.reviews.DeleteMergeQueueEntryByReview(ctx, review.ReviewID); err != nil {
		return model.Review{}, err
	}
	review.Status = model.ReviewStatusMerge
	return s.reviews.Update(ctx, review)
}

func (s *ReviewService) UpdateStatus(ctx context.Context, reviewID string, status model.ReviewStatus, buildID string) (model.Review, error) {
	// MERGE is reached only by AdvanceMergeQueue promoting the queue head, and
	// MERGED only by the merge queue's own gate build succeeding — a caller's
	// PATCH asserting either is an assertion nothing verified, which is exactly
	// what a merge queue exists to refuse.
	if status == model.ReviewStatusMerge || status == model.ReviewStatusMerged {
		return model.Review{}, repository.ErrInvalidInput
	}
	review, err := s.reviews.Get(ctx, reviewID)
	if err != nil {
		return model.Review{}, err
	}

	// READY without a build is the missed-merge-window path, not a build result.
	if status == model.ReviewStatusReady && buildID == "" {
		return s.requeueMergingReview(ctx, review)
	}

	if reviewLastBuildColumn(status) != "" {
		if buildID == "" {
			return model.Review{}, repository.ErrInvalidInput
		}
		return s.updateBuildStatus(ctx, review, status, buildID)
	}

	return s.dequeueWithStatus(ctx, review, status)
}

// requeueMergingReview returns a review that missed its merge window to READY at
// the end of its target branch queue; only a merging review can take that path.
func (s *ReviewService) requeueMergingReview(ctx context.Context, review model.Review) (model.Review, error) {
	if review.Status != model.ReviewStatusMerge {
		return model.Review{}, repository.ErrNotFound
	}
	review.Status = model.ReviewStatusReady
	updated, err := s.reviews.Update(ctx, review)
	if err != nil {
		return model.Review{}, err
	}
	if err := s.enqueueReview(ctx, updated); err != nil {
		return model.Review{}, err
	}
	return updated, nil
}

// dequeueWithStatus applies a status no queued review may hold, so the review
// also leaves the merge queue.
func (s *ReviewService) dequeueWithStatus(ctx context.Context, review model.Review, status model.ReviewStatus) (model.Review, error) {
	review.Status = status
	updated, err := s.reviews.Update(ctx, review)
	if err != nil {
		return model.Review{}, err
	}
	if err := s.reviews.DeleteMergeQueueEntryByReview(ctx, updated.ReviewID); err != nil {
		return model.Review{}, err
	}
	return updated, nil
}

// MarkBuildResult applies the review-status transition a recorded build
// triggers. It reports the review a promotion to MERGE landed on (ok=true) so
// the caller can hand it to the merge queue dispatcher — the promoted review is
// not necessarily the one this build belongs to, since a successful build only
// unblocks its own target branch's queue and AdvanceMergeQueue promotes
// whichever review is at the head of it.
func (s *ReviewService) MarkBuildResult(ctx context.Context, reviewID string, buildID string, successful bool) (model.Review, bool, error) {
	review, err := s.reviews.Get(ctx, reviewID)
	if err != nil {
		return model.Review{}, false, err
	}

	if successful {
		return s.markBuildSucceeded(ctx, review, buildID)
	}
	return s.markBuildFailed(ctx, review, buildID)
}

// markBuildSucceeded queues a review whose build passed and lets its target
// branch start merging. A review in any other status has already moved past this
// build, so its status stands.
func (s *ReviewService) markBuildSucceeded(ctx context.Context, review model.Review, buildID string) (model.Review, bool, error) {
	if review.Status != model.ReviewStatusOpen && review.Status != model.ReviewStatusFailed {
		return model.Review{}, false, nil
	}
	review.Status = model.ReviewStatusReady
	review.LastReadyBuildID = buildID
	updated, err := s.reviews.Update(ctx, review)
	if err != nil {
		return model.Review{}, false, err
	}
	if err := s.enqueueReview(ctx, updated); err != nil {
		return model.Review{}, false, err
	}
	// Another review already merging on that branch is the normal case, not a
	// failure of this build.
	promoted, err := s.AdvanceMergeQueue(ctx, updated.TargetBranch)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return model.Review{}, false, nil
		}
		return model.Review{}, false, err
	}
	return promoted, true, nil
}

// markBuildFailed fails a review whose build failed and drops it from the merge
// queue. A review past those statuses keeps the status it has.
func (s *ReviewService) markBuildFailed(ctx context.Context, review model.Review, buildID string) (model.Review, bool, error) {
	if review.Status != model.ReviewStatusOpen &&
		review.Status != model.ReviewStatusFailed &&
		review.Status != model.ReviewStatusReady &&
		review.Status != model.ReviewStatusMerge {
		return model.Review{}, false, nil
	}
	review.Status = model.ReviewStatusFailed
	review.LastFailedBuildID = buildID
	if _, err := s.reviews.Update(ctx, review); err != nil {
		return model.Review{}, false, err
	}
	return model.Review{}, false, s.reviews.DeleteMergeQueueEntryByReview(ctx, review.ReviewID)
}

func (s *ReviewService) updateBuildStatus(ctx context.Context, review model.Review, status model.ReviewStatus, buildID string) (model.Review, error) {
	column := reviewLastBuildColumn(status)
	if column == "" {
		return model.Review{}, repository.ErrInvalidInput
	}
	build, err := s.builds.Get(ctx, buildID)
	if err != nil {
		return model.Review{}, err
	}
	if build.ReviewID != review.ReviewID || build.Successful != (status != model.ReviewStatusFailed) {
		return model.Review{}, repository.ErrNotFound
	}

	review.Status = status
	switch column {
	case "last_failed_build_id":
		review.LastFailedBuildID = buildID
	case "last_ready_build_id":
		review.LastReadyBuildID = buildID
	}
	updated, err := s.reviews.Update(ctx, review)
	if err != nil {
		return model.Review{}, err
	}
	if status == model.ReviewStatusReady {
		return updated, s.enqueueReview(ctx, updated)
	}
	return updated, s.reviews.DeleteMergeQueueEntryByReview(ctx, updated.ReviewID)
}

func (s *ReviewService) enqueueReview(ctx context.Context, review model.Review) error {
	if err := s.reviews.DeleteMergeQueueEntryByReview(ctx, review.ReviewID); err != nil {
		return err
	}
	_, err := s.reviews.CreateMergeQueueEntry(ctx, model.ReviewMergeQueueEntry{
		TargetBranch: review.TargetBranch,
		ReviewID:     review.ReviewID,
	})
	return err
}

// reviewLastBuildColumn names the last-build column a caller-reported status
// populates. MERGED has no case here: it is never a caller-reported status —
// the merge queue's own gate result is the only path that writes it.
func reviewLastBuildColumn(status model.ReviewStatus) string {
	switch status {
	case model.ReviewStatusFailed:
		return "last_failed_build_id"
	case model.ReviewStatusReady:
		return "last_ready_build_id"
	default:
		return ""
	}
}
