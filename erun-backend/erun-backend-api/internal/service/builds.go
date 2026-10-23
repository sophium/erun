package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// buildVersionPattern is the semver-or-agent-env-snapshot grammar documented
// in collaboration/builds.md's Validation rules table (the same grammar as
// agent-reference/release-policy#version-string-grammar).
var buildVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$`)

// InvalidVersionError refuses a build whose version fails the version
// grammar.
type InvalidVersionError struct {
	Version string
}

func (e *InvalidVersionError) Error() string {
	return fmt.Sprintf("version %q does not match the required grammar", e.Version)
}

func (e *InvalidVersionError) Unwrap() error { return repository.ErrInvalidInput }

// UnsupportedBuildKindError refuses a build written with any kind but
// RECORDED. GATE is still a value historical rows carry, so a caller sending
// it is an older client recording the merge queue's build, and it is told
// what to send instead.
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

// UnattachedBuildRequiresEnvironmentError refuses a build reported with
// neither a review nor an environment: tenant + review is one identity a
// build can report against, tenant + environment is the other, and a build
// naming neither has no identity at all.
type UnattachedBuildRequiresEnvironmentError struct{}

func (e *UnattachedBuildRequiresEnvironmentError) Error() string {
	return "a build with no review must report the environment it ran in"
}

func (e *UnattachedBuildRequiresEnvironmentError) Unwrap() error { return repository.ErrInvalidInput }

type BuildRepository interface {
	Create(ctx context.Context, build model.Build) (model.Build, error)
}

// BuildReviewService applies the review-status transition a recorded build
// triggers: a build's outcome moves the review between OPEN/FAILED and
// READY/FAILED, and a failed build against a MERGE review moves it to FAILED.
// A successful build against a MERGE review (the prepared merge commit's)
// does not move the review here — that needs the git-state verification only
// PATCH .../status's MERGED transition performs (ReviewService.UpdateStatus /
// acceptMerged), because a build report alone is only ever the caller's own
// word for what happened.
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
	// A build with no review has no review status to transition.
	if created.ReviewID == "" {
		return created, nil
	}
	if _, _, err := s.reviews.MarkBuildResult(ctx, created.ReviewID, created.BuildID, created.Successful); err != nil {
		return model.Build{}, err
	}
	return created, nil
}
