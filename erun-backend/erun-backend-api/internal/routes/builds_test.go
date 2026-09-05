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

func (s stubBuildRepository) ListPage(context.Context, apirepository.BuildListFilter) (apirepository.BuildPage, error) {
	if s.err != nil {
		return apirepository.BuildPage{}, s.err
	}
	return apirepository.BuildPage{Builds: []model.Build{s.build}}, nil
}

// TestCreateBuildAcceptsAReportedGateKind: an environment now reports its own
// merge-queue gate result, so kind=GATE is no longer forced to
// RECORDED the way a client-supplied authorUserId is ignored.
func TestCreateBuildAcceptsAReportedGateKind(t *testing.T) {
	service := &stubBuildService{}
	routes := BuildRoutes{service: service}
	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/review-1/builds",
		bytes.NewBufferString(`{"successful":true,"commitId":"abc123","kind":"GATE"}`))
	req.SetPathValue("review_id", "review-1")
	rec := httptest.NewRecorder()

	routes.createBuild(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if service.created.Kind != model.BuildKindGate {
		t.Fatalf("kind persisted = %q, want %q", service.created.Kind, model.BuildKindGate)
	}
}

// TestCreateBuildRejectsAnUnknownKind: kind is one of two known values, not
// an open string.
func TestCreateBuildRejectsAnUnknownKind(t *testing.T) {
	service := &stubBuildService{}
	routes := BuildRoutes{service: service}
	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/review-1/builds",
		bytes.NewBufferString(`{"successful":true,"commitId":"abc123","kind":"BOGUS"}`))
	req.SetPathValue("review_id", "review-1")
	rec := httptest.NewRecorder()

	routes.createBuild(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
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

// TestCreateUnattachedBuildClearsAnyCallerSuppliedReviewID: POST /v1/builds
// is the unattached path -- a caller-supplied reviewId in the body must not
// smuggle a review link in through it (that is what the nested route is
// for).
func TestCreateUnattachedBuildClearsAnyCallerSuppliedReviewID(t *testing.T) {
	service := &stubBuildService{}
	routes := BuildRoutes{service: service}
	req := httptest.NewRequest(http.MethodPost, "/v1/builds",
		bytes.NewBufferString(`{"successful":true,"commitId":"abcdef0123456789abcdef0123456789abcdef01","version":"1.0.0","environmentId":"env-1","reviewId":"review-1"}`))
	rec := httptest.NewRecorder()

	routes.createUnattachedBuild(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if service.created.ReviewID != "" {
		t.Fatalf("created.ReviewID = %q, want cleared", service.created.ReviewID)
	}
	if service.created.EnvironmentID != "env-1" {
		t.Fatalf("created.EnvironmentID = %q, want env-1", service.created.EnvironmentID)
	}
}

// TestCreateUnattachedBuildRejectsGateKind: a GATE build always gates a
// specific review's merge, so it has no unattached form.
func TestCreateUnattachedBuildRejectsGateKind(t *testing.T) {
	service := &stubBuildService{}
	routes := BuildRoutes{service: service}
	req := httptest.NewRequest(http.MethodPost, "/v1/builds",
		bytes.NewBufferString(`{"successful":true,"commitId":"abcdef0123456789abcdef0123456789abcdef01","environmentId":"env-1","kind":"GATE"}`))
	rec := httptest.NewRecorder()

	routes.createUnattachedBuild(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestCreateUnattachedBuildReportsGateBuildRequiresReviewCode: a service
// refusal for a GATE build with no review surfaces its own machine code
// rather than a generic one.
func TestCreateUnattachedBuildReportsGateBuildRequiresReviewCode(t *testing.T) {
	service := &stubBuildService{err: &service.GateBuildRequiresReviewError{}}
	routes := BuildRoutes{service: service}
	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/review-1/builds",
		bytes.NewBufferString(`{"successful":true,"commitId":"abcdef0123456789abcdef0123456789abcdef01","kind":"GATE"}`))
	req.SetPathValue("review_id", "review-1")
	rec := httptest.NewRecorder()

	routes.createBuild(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"INVALID_BODY"`) {
		t.Fatalf("body = %q, want code INVALID_BODY", rec.Body.String())
	}
}

// TestCreateUnattachedBuildReportsMissingEnvironmentCode: a build with
// neither a review nor an environment has no identity to report against.
func TestCreateUnattachedBuildReportsMissingEnvironmentCode(t *testing.T) {
	service := &stubBuildService{err: &service.UnattachedBuildRequiresEnvironmentError{}}
	routes := BuildRoutes{service: service}
	req := httptest.NewRequest(http.MethodPost, "/v1/builds",
		bytes.NewBufferString(`{"successful":true,"commitId":"abcdef0123456789abcdef0123456789abcdef01","version":"1.0.0"}`))
	rec := httptest.NewRecorder()

	routes.createUnattachedBuild(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"field":"environmentId"`) {
		t.Fatalf("body = %q, want field environmentId", rec.Body.String())
	}
}

// TestListAllBuildsReturnsAPagedEnvelope: GET /v1/builds' response is
// {builds, nextCursor}, not a bare array -- distinct from the review-nested
// list, which stays a bare array since it never paginates.
func TestListAllBuildsReturnsAPagedEnvelope(t *testing.T) {
	routes := BuildRoutes{builds: stubBuildRepository{build: model.Build{BuildID: "build-1"}}}
	req := httptest.NewRequest(http.MethodGet, "/v1/builds", nil)
	rec := httptest.NewRecorder()

	routes.listAllBuilds(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"builds":[`) {
		t.Fatalf("body = %q, want a builds envelope", rec.Body.String())
	}
}

// TestListAllBuildsRejectsAMalformedSuccessfulFilter: a query filter with the
// wrong shape is a 400, not a repository error.
func TestListAllBuildsRejectsAMalformedSuccessfulFilter(t *testing.T) {
	routes := BuildRoutes{builds: stubBuildRepository{}}
	req := httptest.NewRequest(http.MethodGet, "/v1/builds?successful=maybe", nil)
	rec := httptest.NewRecorder()

	routes.listAllBuilds(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}
