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
	_, err := svc.Create(context.Background(), model.Build{CommitID: validCommitID, Version: "not-a-version", Successful: true})

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
		CommitID: validCommitID, Version: "1.2.3-snapshot-20260824091244", Successful: true,
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
		CommitID: validCommitID, Kind: model.BuildKindGate, Successful: false,
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
		CommitID: validCommitID, Kind: model.BuildKindGate, Successful: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if repo.created.Kind != model.BuildKindGate {
		t.Fatalf("created build kind = %q, want GATE to pass through", repo.created.Kind)
	}
}
