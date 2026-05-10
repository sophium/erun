package service

import (
	"context"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

type BuildRepository interface {
	Create(ctx context.Context, build model.Build) (model.Build, error)
}

type BuildReviewService interface {
	MarkBuildResult(ctx context.Context, reviewID string, buildID string, successful bool) error
}

type BuildService struct {
	builds  BuildRepository
	reviews BuildReviewService
}

func NewBuildService(builds BuildRepository, reviews BuildReviewService) *BuildService {
	return &BuildService{builds: builds, reviews: reviews}
}

func (s *BuildService) Create(ctx context.Context, build model.Build) (model.Build, error) {
	created, err := s.builds.Create(ctx, build)
	if err != nil {
		return model.Build{}, err
	}
	return created, s.reviews.MarkBuildResult(ctx, created.ReviewID, created.BuildID, created.Successful)
}
