package service

import (
	"context"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

type BuildRepository interface {
	Create(ctx context.Context, build model.Build) (model.Build, error)
}

// BuildReviewService applies the review-status transition a recorded build
// triggers, and reports back the review a promotion to MERGE landed on
// (ok=true) so BuildService can hand it to the merge queue dispatcher — the
// same event the manual merge-queue/advance route already surfaces to its own
// caller.
type BuildReviewService interface {
	MarkBuildResult(ctx context.Context, reviewID string, buildID string, successful bool) (model.Review, bool, error)
}

// MergeQueueDispatcher starts the merge gate for a review a build just
// promoted to MERGE. Optional: nil leaves the review sitting in MERGE for a
// caller to advance manually.
type MergeQueueDispatcher interface {
	Dispatch(ctx context.Context, review model.Review)
}

type BuildService struct {
	builds  BuildRepository
	reviews BuildReviewService
	merge   MergeQueueDispatcher
}

func NewBuildService(builds BuildRepository, reviews BuildReviewService, merge MergeQueueDispatcher) *BuildService {
	return &BuildService{builds: builds, reviews: reviews, merge: merge}
}

func (s *BuildService) Create(ctx context.Context, build model.Build) (model.Build, error) {
	if build.Kind == "" {
		build.Kind = model.BuildKindRecorded
	}
	created, err := s.builds.Create(ctx, build)
	if err != nil {
		return model.Build{}, err
	}
	promoted, ok, err := s.reviews.MarkBuildResult(ctx, created.ReviewID, created.BuildID, created.Successful)
	if err != nil {
		return model.Build{}, err
	}
	if ok && s.merge != nil {
		s.merge.Dispatch(ctx, promoted)
	}
	return created, nil
}
