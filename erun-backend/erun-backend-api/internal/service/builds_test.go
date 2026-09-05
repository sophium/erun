package service

import (
	"context"
	"errors"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

type fakeBuildRepo struct {
	created model.Build
}

func (f *fakeBuildRepo) Create(_ context.Context, build model.Build) (model.Build, error) {
	build.BuildID = "build-1"
	f.created = build
	return build, nil
}

type fakeBuildReviewService struct{}

func (fakeBuildReviewService) MarkBuildResult(context.Context, string, string, bool) (model.Review, bool, error) {
	return model.Review{}, false, nil
}

const validCommitID = "abcdef0123456789abcdef0123456789abcdef01"

// TestBuildCreateRefusesAMalformedCommitID: builds.md documents the same
// 40-lowercase-hex-character grammar as comments.md, as INVALID_COMMIT_ID.
func TestBuildCreateRefusesAMalformedCommitID(t *testing.T) {
	svc := NewBuildService(&fakeBuildRepo{}, nil)
	_, err := svc.Create(context.Background(), model.Build{CommitID: "short", Version: "1.0.0", Successful: true})

	var invalidCommitID *InvalidCommitIDError
	if !errors.As(err, &invalidCommitID) {
		t.Fatalf("Create error = %v, want *InvalidCommitIDError", err)
	}
	if !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("Create error = %v, want it to unwrap to ErrInvalidInput", err)
	}
}

// TestBuildCreateRefusesAMalformedVersion: builds.md documents
// INVALID_VERSION for a version failing the semver/snapshot grammar.
func TestBuildCreateRefusesAMalformedVersion(t *testing.T) {
	svc := NewBuildService(&fakeBuildRepo{}, nil)
	_, err := svc.Create(context.Background(), model.Build{ReviewID: "review-1", CommitID: validCommitID, Version: "not-a-version", Successful: true})

	var invalidVersion *InvalidVersionError
	if !errors.As(err, &invalidVersion) {
		t.Fatalf("Create error = %v, want *InvalidVersionError", err)
	}
	if !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("Create error = %v, want it to unwrap to ErrInvalidInput", err)
	}
}

// TestBuildCreateAcceptsASnapshotVersion: the agent-env snapshot tag shape
// (<semver>-snapshot-<UTC-timestamp>) is a documented valid version, not just
// a plain semver.
func TestBuildCreateAcceptsASnapshotVersion(t *testing.T) {
	svc := NewBuildService(&fakeBuildRepo{}, fakeBuildReviewService{})
	_, err := svc.Create(context.Background(), model.Build{
		ReviewID: "review-1", CommitID: validCommitID, Version: "1.2.3-snapshot-20260824091244", Successful: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
}

// TestBuildCreateRefusesAFailedGateBuildWithNoFailureDetail: the gate's own
// account of why it did not succeed is the only thing that makes a failed
// GATE build actionable.
func TestBuildCreateRefusesAFailedGateBuildWithNoFailureDetail(t *testing.T) {
	svc := NewBuildService(&fakeBuildRepo{}, fakeBuildReviewService{})
	_, err := svc.Create(context.Background(), model.Build{
		ReviewID: "review-1", CommitID: validCommitID, Kind: model.BuildKindGate, Successful: false,
	})

	var missingFailureDetail *MissingFailureDetailError
	if !errors.As(err, &missingFailureDetail) {
		t.Fatalf("Create error = %v, want *MissingFailureDetailError", err)
	}
	if !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("Create error = %v, want it to unwrap to ErrInvalidInput", err)
	}
}

// TestBuildCreateAcceptsAGateBuildWithNoVersion: a GATE build publishes
// nothing, so it must not be held to RECORDED's version grammar.
func TestBuildCreateAcceptsAGateBuildWithNoVersion(t *testing.T) {
	repo := &fakeBuildRepo{}
	svc := NewBuildService(repo, fakeBuildReviewService{})
	_, err := svc.Create(context.Background(), model.Build{
		ReviewID: "review-1", CommitID: validCommitID, Kind: model.BuildKindGate, Successful: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if repo.created.Kind != model.BuildKindGate {
		t.Fatalf("created build kind = %q, want GATE to pass through", repo.created.Kind)
	}
}

// TestBuildCreateRefusesAGateBuildWithNoReview: a GATE build always gates a
// specific review's merge, so an unattached build can never claim that kind.
func TestBuildCreateRefusesAGateBuildWithNoReview(t *testing.T) {
	svc := NewBuildService(&fakeBuildRepo{}, fakeBuildReviewService{})
	_, err := svc.Create(context.Background(), model.Build{
		EnvironmentID: "env-1", CommitID: validCommitID, Kind: model.BuildKindGate, Successful: true,
	})

	var gateRequiresReview *GateBuildRequiresReviewError
	if !errors.As(err, &gateRequiresReview) {
		t.Fatalf("Create error = %v, want *GateBuildRequiresReviewError", err)
	}
	if !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("Create error = %v, want it to unwrap to ErrInvalidInput", err)
	}
}

// TestBuildCreateRefusesNeitherReviewNorEnvironment: a build naming neither a
// review nor an environment has no identity to persist against.
func TestBuildCreateRefusesNeitherReviewNorEnvironment(t *testing.T) {
	svc := NewBuildService(&fakeBuildRepo{}, fakeBuildReviewService{})
	_, err := svc.Create(context.Background(), model.Build{
		CommitID: validCommitID, Version: "1.0.0", Successful: true,
	})

	var missingIdentity *UnattachedBuildRequiresEnvironmentError
	if !errors.As(err, &missingIdentity) {
		t.Fatalf("Create error = %v, want *UnattachedBuildRequiresEnvironmentError", err)
	}
	if !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("Create error = %v, want it to unwrap to ErrInvalidInput", err)
	}
}

// TestBuildCreateAcceptsAnUnattachedRecordedBuild: an ordinary `erun build`
// reports a RECORDED build against its environment with no review at all,
// and the review-status transition (which needs a review to transition) is
// skipped rather than called with an empty id.
func TestBuildCreateAcceptsAnUnattachedRecordedBuild(t *testing.T) {
	repo := &fakeBuildRepo{}
	reviews := &countingBuildReviewService{}
	svc := NewBuildService(repo, reviews)
	created, err := svc.Create(context.Background(), model.Build{
		EnvironmentID: "env-1", CommitID: validCommitID, Version: "1.0.0", Successful: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ReviewID != "" {
		t.Fatalf("created.ReviewID = %q, want empty", created.ReviewID)
	}
	if created.EnvironmentID != "env-1" {
		t.Fatalf("created.EnvironmentID = %q, want env-1", created.EnvironmentID)
	}
	if reviews.calls != 0 {
		t.Fatalf("MarkBuildResult calls = %d, want 0 for a build with no review", reviews.calls)
	}
}

type countingBuildReviewService struct{ calls int }

func (c *countingBuildReviewService) MarkBuildResult(context.Context, string, string, bool) (model.Review, bool, error) {
	c.calls++
	return model.Review{}, false, nil
}
