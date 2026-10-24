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

func TestBuildCreateAcceptsASnapshotVersion(t *testing.T) {
	svc := NewBuildService(&fakeBuildRepo{}, fakeBuildReviewService{})
	_, err := svc.Create(context.Background(), model.Build{
		ReviewID: "review-1", CommitID: validCommitID, Version: "1.2.3-snapshot-20260824091244", Successful: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestBuildCreateRefusesTheRetiredGateKind(t *testing.T) {
	repo := &fakeBuildRepo{}
	svc := NewBuildService(repo, fakeBuildReviewService{})
	_, err := svc.Create(context.Background(), model.Build{
		ReviewID: "review-1", CommitID: validCommitID, Kind: model.BuildKindGate, Successful: true,
	})

	var unsupported *UnsupportedBuildKindError
	if !errors.As(err, &unsupported) {
		t.Fatalf("Create error = %v, want *UnsupportedBuildKindError", err)
	}
	if !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("Create error = %v, want it to unwrap to ErrInvalidInput", err)
	}
	if repo.created.CommitID != "" {
		t.Fatalf("a GATE build reached the repository: %+v", repo.created)
	}
}

func TestBuildCreateRequiresAVersionOnAMergeQueueBuild(t *testing.T) {
	svc := NewBuildService(&fakeBuildRepo{}, fakeBuildReviewService{})
	_, err := svc.Create(context.Background(), model.Build{
		ReviewID: "review-1", CommitID: validCommitID, Successful: true,
	})

	var invalidVersion *InvalidVersionError
	if !errors.As(err, &invalidVersion) {
		t.Fatalf("Create error = %v, want *InvalidVersionError for a build with no version", err)
	}
}

func TestBuildCreatePersistsAFailureDetailOnAnyFailedBuild(t *testing.T) {
	repo := &fakeBuildRepo{}
	svc := NewBuildService(repo, fakeBuildReviewService{})
	_, err := svc.Create(context.Background(), model.Build{
		ReviewID: "review-1", CommitID: validCommitID, Version: "1.0.0", Successful: false,
		FailureDetail: "erun build failed: go vet found 3 issues",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if repo.created.Kind != model.BuildKindRecorded || repo.created.FailureDetail == "" {
		t.Fatalf("created = %+v, want a RECORDED build carrying its failure detail", repo.created)
	}
}

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
