package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// buildVersionPattern is the semver-or-snapshot grammar documented in collaboration/builds.md.
var buildVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$`)

// InvalidVersionError refuses a build whose version fails the version grammar.
type InvalidVersionError struct {
	Version string
}

func (e *InvalidVersionError) Error() string {
	return fmt.Sprintf("version %q does not match the required grammar", e.Version)
}

func (e *InvalidVersionError) Unwrap() error { return repository.ErrInvalidInput }

// UnsupportedBuildKindError refuses any kind but RECORDED; GATE comes from older clients.
type UnsupportedBuildKindError struct {
	Kind model.BuildKind
}

func (e *UnsupportedBuildKindError) Error() string {
	if e.Kind == model.BuildKindGate {
		return "kind GATE is retired; record the merge commit's build as kind RECORDED with the version its build minted"
	}
	return fmt.Sprintf("kind %q is not supported; a build is recorded as kind RECORDED", e.Kind)
}

func (e *UnsupportedBuildKindError) Unwrap() error { return repository.ErrInvalidInput }

// UnattachedBuildRequiresEnvironmentError refuses a build naming neither a review nor an environment.
type UnattachedBuildRequiresEnvironmentError struct{}

func (e *UnattachedBuildRequiresEnvironmentError) Error() string {
	return "a build with no review must report the environment it ran in"
}

func (e *UnattachedBuildRequiresEnvironmentError) Unwrap() error { return repository.ErrInvalidInput }

type BuildRepository interface {
	Create(ctx context.Context, build model.Build) (model.Build, error)
}

// BuildReviewService applies the review transition a recorded build triggers. A successful MERGE build does
// not move the review: only PATCH .../status's MERGED transition verifies git state.
type BuildReviewService interface {
	MarkBuildResult(ctx context.Context, reviewID string, buildID string, successful bool) (model.Review, bool, error)
}

type BuildService struct {
	builds  BuildRepository
	reviews BuildReviewService
}

func NewBuildService(builds BuildRepository, reviews BuildReviewService) *BuildService {
	return &BuildService{builds: builds, reviews: reviews}
}

func (s *BuildService) Create(ctx context.Context, build model.Build) (model.Build, error) {
	if !commitIDPattern.MatchString(build.CommitID) {
		return model.Build{}, &InvalidCommitIDError{CommitID: build.CommitID}
	}
	if build.Kind == "" {
		build.Kind = model.BuildKindRecorded
	}
	if strings.TrimSpace(build.ReviewID) == "" && strings.TrimSpace(build.EnvironmentID) == "" {
		return model.Build{}, &UnattachedBuildRequiresEnvironmentError{}
	}
	if build.Kind != model.BuildKindRecorded {
		return model.Build{}, &UnsupportedBuildKindError{Kind: build.Kind}
	}
	if !buildVersionPattern.MatchString(build.Version) {
		return model.Build{}, &InvalidVersionError{Version: build.Version}
	}
	created, err := s.builds.Create(ctx, build)
	if err != nil {
		return model.Build{}, err
	}
	if created.ReviewID == "" {
		return created, nil
	}
	if _, _, err := s.reviews.MarkBuildResult(ctx, created.ReviewID, created.BuildID, created.Successful); err != nil {
		return model.Build{}, err
	}
	return created, nil
}
