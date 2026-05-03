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
	if status == model.ReviewStatusMerge {
		return model.Review{}, repository.ErrInvalidInput
	}
	review, err := s.reviews.Get(ctx, reviewID)
	if err != nil {
		return model.Review{}, err
	}

	if status == model.ReviewStatusReady && buildID == "" {
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

	if reviewLastBuildColumn(status) != "" {
		if buildID == "" {
			return model.Review{}, repository.ErrInvalidInput
		}
		return s.updateBuildStatus(ctx, review, status, buildID)
	}

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

func (s *ReviewService) MarkBuildResult(ctx context.Context, reviewID string, buildID string, successful bool) error {
	review, err := s.reviews.Get(ctx, reviewID)
	if err != nil {
		return err
	}

	if successful {
		if review.Status != model.ReviewStatusOpen && review.Status != model.ReviewStatusFailed {
			return nil
		}
		review.Status = model.ReviewStatusReady
		review.LastReadyBuildID = buildID
		updated, err := s.reviews.Update(ctx, review)
		if err != nil {
			return err
		}
		if err := s.enqueueReview(ctx, updated); err != nil {
			return err
		}
		_, err = s.AdvanceMergeQueue(ctx, updated.TargetBranch)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return err
		}
		return nil
	}

	if review.Status != model.ReviewStatusOpen &&
		review.Status != model.ReviewStatusFailed &&
		review.Status != model.ReviewStatusReady &&
		review.Status != model.ReviewStatusMerge {
		return nil
	}
	review.Status = model.ReviewStatusFailed
	review.LastFailedBuildID = buildID
	if _, err := s.reviews.Update(ctx, review); err != nil {
		return err
	}
	return s.reviews.DeleteMergeQueueEntryByReview(ctx, reviewID)
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
	case "last_merged_build_id":
		review.LastMergedBuildID = buildID
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

func reviewLastBuildColumn(status model.ReviewStatus) string {
	switch status {
	case model.ReviewStatusFailed:
		return "last_failed_build_id"
	case model.ReviewStatusReady:
		return "last_ready_build_id"
	case model.ReviewStatusMerged:
		return "last_merged_build_id"
	default:
		return ""
	}
}
