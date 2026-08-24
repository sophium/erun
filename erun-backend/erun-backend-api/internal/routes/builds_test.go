package routes

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
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
