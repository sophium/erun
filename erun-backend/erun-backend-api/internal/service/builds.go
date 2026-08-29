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

// InvalidVersionError refuses a RECORDED build whose version fails the
// version grammar.
type InvalidVersionError struct {
	Version string
}

func (e *InvalidVersionError) Error() string {
	return fmt.Sprintf("version %q does not match the required grammar", e.Version)
}

func (e *InvalidVersionError) Unwrap() error { return repository.ErrInvalidInput }

// MissingFailureDetailError refuses a failed GATE build with no
// failureDetail: the gate's own account of why it did not succeed is the
// only thing that makes the failure actionable.
type MissingFailureDetailError struct{}

func (e *MissingFailureDetailError) Error() string {
	return "failureDetail is required on a failed GATE build"
}

func (e *MissingFailureDetailError) Unwrap() error { return repository.ErrInvalidInput }

type BuildRepository interface {
	Create(ctx context.Context, build model.Build) (model.Build, error)
}

// BuildReviewService applies the review-status transition a recorded build
// triggers: a RECORDED build's outcome moves the review between OPEN/FAILED
// and READY/FAILED, and (unchanged by build kind) a failed build against a
// MERGE review moves it to FAILED. A successful GATE build does not move the
// review here — that needs the git-state verification only PATCH
// .../status's MERGED transition performs (ReviewService.UpdateStatus /
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
	switch build.Kind {
	case model.BuildKindRecorded:
		if !buildVersionPattern.MatchString(build.Version) {
			return model.Build{}, &InvalidVersionError{Version: build.Version}
		}
	case model.BuildKindGate:
		if !build.Successful && strings.TrimSpace(build.FailureDetail) == "" {
			return model.Build{}, &MissingFailureDetailError{}
		}
	}
	created, err := s.builds.Create(ctx, build)
	if err != nil {
		return model.Build{}, err
	}
	if _, _, err := s.reviews.MarkBuildResult(ctx, created.ReviewID, created.BuildID, created.Successful); err != nil {
		return model.Build{}, err
	}
	return created, nil
}
