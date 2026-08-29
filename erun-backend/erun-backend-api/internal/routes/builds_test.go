package routes

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

type stubBuildService struct {
	created model.Build
	err     error
}

func (s *stubBuildService) Create(_ context.Context, build model.Build) (model.Build, error) {
	s.created = build
	if s.err != nil {
		return model.Build{}, s.err
	}
	build.BuildID = "build-1"
	return build, nil
}

type stubBuildRepository struct {
	build model.Build
	err   error
}

func (s stubBuildRepository) Get(context.Context, string) (model.Build, error) { return s.build, s.err }

func (s stubBuildRepository) List(context.Context, apirepository.BuildFilter) ([]model.Build, error) {
	return nil, s.err
}

// TestCreateBuildForcesKindRecorded is the impersonation guard for builds.kind
// the same shape reviews.AuthorUserID already gets: a client cannot assert its
// own reported build is the merge queue's GATE build.
func TestCreateBuildForcesKindRecorded(t *testing.T) {
	service := &stubBuildService{}
	routes := BuildRoutes{service: service}
	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/review-1/builds",
		bytes.NewBufferString(`{"successful":true,"commitId":"abc123","version":"1.0.0","kind":"GATE"}`))
	req.SetPathValue("review_id", "review-1")
	rec := httptest.NewRecorder()

	routes.createBuild(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if service.created.Kind != model.BuildKindRecorded {
		t.Fatalf("kind persisted = %q, want %q despite the client asserting GATE", service.created.Kind, model.BuildKindRecorded)
	}
}

// TestCreateBuildInvalidCommitIDReportsItsCode: builds.md documents
// INVALID_COMMIT_ID as 400 for a commitId that is not 40 lowercase hex chars.
func TestCreateBuildInvalidCommitIDReportsItsCode(t *testing.T) {
	service := &stubBuildService{err: &service.InvalidCommitIDError{CommitID: "bad"}}
	routes := BuildRoutes{service: service}
	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/review-1/builds",
		bytes.NewBufferString(`{"successful":true,"commitId":"bad","version":"1.0.0"}`))
	req.SetPathValue("review_id", "review-1")
	rec := httptest.NewRecorder()

	routes.createBuild(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"INVALID_COMMIT_ID"`) {
		t.Fatalf("body = %q, want code INVALID_COMMIT_ID", rec.Body.String())
	}
}

// TestCreateBuildInvalidVersionReportsItsCode: builds.md documents
// INVALID_VERSION as 400 for a version failing the version grammar.
func TestCreateBuildInvalidVersionReportsItsCode(t *testing.T) {
	service := &stubBuildService{err: &service.InvalidVersionError{Version: "bad"}}
	routes := BuildRoutes{service: service}
	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/review-1/builds",
		bytes.NewBufferString(`{"successful":true,"commitId":"abcdef0123456789abcdef0123456789abcdef01","version":"bad"}`))
	req.SetPathValue("review_id", "review-1")
	rec := httptest.NewRecorder()

	routes.createBuild(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"INVALID_VERSION"`) {
		t.Fatalf("body = %q, want code INVALID_VERSION", rec.Body.String())
	}
}

func TestGetBuildReturnsTheRepositoryResult(t *testing.T) {
	routes := BuildRoutes{builds: stubBuildRepository{build: model.Build{BuildID: "build-1", Kind: model.BuildKindGate}}}
	req := httptest.NewRequest(http.MethodGet, "/v1/reviews/review-1/builds/build-1", nil)
	req.SetPathValue("build_id", "build-1")
	rec := httptest.NewRecorder()

	routes.getBuild(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}
