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
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
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

// ownEnvironmentGetter resolves the named ids as the caller's own and refuses
// everything else, standing in for the real row-level-security-scoped
// environment read.
func ownEnvironmentGetter(ids ...string) EnvironmentGetter {
	known := make(map[string]bool, len(ids))
	for _, id := range ids {
		known[id] = true
	}
	return ownEnvironments(known)
}

type ownEnvironments map[string]bool

func (s ownEnvironments) Get(_ context.Context, environmentID string) (model.Environment, error) {
	if s[environmentID] {
		return model.Environment{EnvironmentID: environmentID}, nil
	}
	return model.Environment{}, apirepository.ErrNotFound
}

type stubBuildRepository struct {
	build model.Build
	err   error
	// gotTenantID records the tenant the route named, so a test can hold the
	// handler to passing the caller's own rather than merely passing one.
	gotTenantID string
}

func (s *stubBuildRepository) Get(_ context.Context, tenantID, _ string) (model.Build, error) {
	s.gotTenantID = tenantID
	return s.build, s.err
}

func (s *stubBuildRepository) List(context.Context, apirepository.BuildFilter) ([]model.Build, error) {
	return nil, s.err
}

func (s *stubBuildRepository) ListPage(context.Context, apirepository.BuildListFilter) (apirepository.BuildPage, error) {
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
	builds := &stubBuildRepository{build: model.Build{BuildID: "build-1", Kind: model.BuildKindGate}}
	routes := BuildRoutes{builds: builds}
	req := httptest.NewRequest(http.MethodGet, "/v1/reviews/review-1/builds/build-1", nil)
	req.SetPathValue("build_id", "build-1")
	// The read names the caller's tenant, so the handler needs the scoped
	// security context the auth middleware would have installed.
	req = req.WithContext(security.WithContext(req.Context(), security.Context{TenantID: "tenant-1", TenantType: string(model.TenantTypeCompany)}))
	rec := httptest.NewRecorder()

	routes.getBuild(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if builds.gotTenantID != "tenant-1" {
		t.Fatalf("repository read for tenant %q, want the caller's own tenant-1", builds.gotTenantID)
	}
}

// TestCreateUnattachedBuildClearsAnyCallerSuppliedReviewID: POST /v1/builds
// is the unattached path -- a caller-supplied reviewId in the body must not
// smuggle a review link in through it (that is what the nested route is
// for).
func TestCreateUnattachedBuildClearsAnyCallerSuppliedReviewID(t *testing.T) {
	service := &stubBuildService{}
	routes := BuildRoutes{service: service, environments: ownEnvironmentGetter("env-1")}
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
	routes := BuildRoutes{service: service, environments: ownEnvironmentGetter("env-1")}
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
	routes := BuildRoutes{builds: &stubBuildRepository{build: model.Build{BuildID: "build-1"}}}
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
	routes := BuildRoutes{builds: &stubBuildRepository{}}
	req := httptest.NewRequest(http.MethodGet, "/v1/builds?successful=maybe", nil)
	rec := httptest.NewRecorder()

	routes.listAllBuilds(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// stubTrackingEnvironmentGetter records every id it was asked about, so a
// test can tell "the environment was never consulted" from "it was consulted
// and answered not-found".
type stubTrackingEnvironmentGetter struct {
	own   ownEnvironments
	asked []string
}

func (s *stubTrackingEnvironmentGetter) Get(_ context.Context, environmentID string) (model.Environment, error) {
	s.asked = append(s.asked, environmentID)
	return s.own.Get(context.Background(), environmentID)
}

// postBuild drives one of the two build-reporting handlers.
func postBuild(t *testing.T, routes BuildRoutes, path, environmentID, reviewID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	if reviewID != "" {
		req.SetPathValue("review_id", reviewID)
	}
	rec := httptest.NewRecorder()
	if reviewID != "" {
		routes.createBuild(rec, req)
	} else {
		routes.createUnattachedBuild(rec, req)
	}
	return rec
}

// TestBuildRoutesRefuseAnotherTenantsEnvironmentBeforeCreating covers both
// build-reporting routes: an environmentId the caller's own RLS-scoped read
// cannot see is refused before the build is ever written, so the route is
// never the place that decides whether an id exists somewhere the caller
// cannot look.
func TestBuildRoutesRefuseAnotherTenantsEnvironmentBeforeCreating(t *testing.T) {
	// The getter's rule is the real one: an id the caller's tenant does not
	// own is simply not found. Cross-tenant and nonexistent are then distinct
	// inputs -- only one of them names a row that really exists -- which is
	// exactly why their answers must not differ.
	environments := &stubTrackingEnvironmentGetter{own: ownEnvironments{"env-mine": true}}

	cases := []struct {
		name          string
		path          string
		reviewID      string
		body          string
		wantEnvironmt string
	}{
		{
			name:          "unattached, another tenant's environment",
			path:          "/v1/builds",
			body:          `{"successful":true,"commitId":"abcdef0123456789abcdef0123456789abcdef01","version":"1.0.0","environmentId":"env-theirs"}`,
			wantEnvironmt: "env-theirs",
		},
		{
			name:          "unattached, an environment that exists nowhere",
			path:          "/v1/builds",
			body:          `{"successful":true,"commitId":"abcdef0123456789abcdef0123456789abcdef01","version":"1.0.0","environmentId":"env-nowhere"}`,
			wantEnvironmt: "env-nowhere",
		},
		{
			name:          "review-linked, another tenant's environment",
			path:          "/v1/reviews/review-1/builds",
			reviewID:      "review-1",
			body:          `{"successful":true,"commitId":"abcdef0123456789abcdef0123456789abcdef01","version":"1.0.0","environmentId":"env-theirs"}`,
			wantEnvironmt: "env-theirs",
		},
		{
			name:          "review-linked, an environment that exists nowhere",
			path:          "/v1/reviews/review-1/builds",
			reviewID:      "review-1",
			body:          `{"successful":true,"commitId":"abcdef0123456789abcdef0123456789abcdef01","version":"1.0.0","environmentId":"env-nowhere"}`,
			wantEnvironmt: "env-nowhere",
		},
	}

	responses := make(map[string]string, len(cases))
	for _, tc := range cases {
		service := &stubBuildService{}
		routes := BuildRoutes{service: service, environments: environments}
		rec := postBuild(t, routes, tc.path, tc.wantEnvironmt, tc.reviewID, tc.body)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404: %s", tc.name, rec.Code, rec.Body.String())
		}
		if service.created.BuildID != "" || service.created.CommitID != "" {
			t.Fatalf("%s: the build was created anyway: %+v", tc.name, service.created)
		}
		responses[tc.name] = rec.Body.String()
	}

	// The refusal must not depend on which of the two it was: a caller that
	// does not own the id must learn nothing about whether it exists.
	if responses["unattached, another tenant's environment"] != responses["unattached, an environment that exists nowhere"] {
		t.Fatalf("the two unattached refusals differ:\n  other tenant: %q\n  nonexistent:  %q",
			responses["unattached, another tenant's environment"], responses["unattached, an environment that exists nowhere"])
	}
	if responses["review-linked, another tenant's environment"] != responses["review-linked, an environment that exists nowhere"] {
		t.Fatalf("the two review-linked refusals differ:\n  other tenant: %q\n  nonexistent:  %q",
			responses["review-linked, another tenant's environment"], responses["review-linked, an environment that exists nowhere"])
	}
}

// TestBuildRoutesAcceptTheCallersOwnEnvironment: the guard refuses ids the
// caller does not own, not environmentId itself.
func TestBuildRoutesAcceptTheCallersOwnEnvironment(t *testing.T) {
	environments := &stubTrackingEnvironmentGetter{own: ownEnvironments{"env-mine": true}}
	service := &stubBuildService{}
	routes := BuildRoutes{service: service, environments: environments}

	rec := postBuild(t, routes, "/v1/builds", "env-mine", "",
		`{"successful":true,"commitId":"abcdef0123456789abcdef0123456789abcdef01","version":"1.0.0","environmentId":"env-mine"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if service.created.EnvironmentID != "env-mine" {
		t.Fatalf("environmentId persisted = %q, want env-mine", service.created.EnvironmentID)
	}
}

// TestUnattachedBuildWithNoEnvironmentIsNotAnEnvironmentLookup: an omitted
// environmentId is a real case (the build's own identity rule then decides),
// not an id that failed to resolve -- so the guard must not invent a lookup
// for it.
func TestUnattachedBuildWithNoEnvironmentIsNotAnEnvironmentLookup(t *testing.T) {
	environments := &stubTrackingEnvironmentGetter{own: ownEnvironments{}}
	service := &stubBuildService{err: &service.UnattachedBuildRequiresEnvironmentError{}}
	routes := BuildRoutes{service: service, environments: environments}

	rec := postBuild(t, routes, "/v1/builds", "", "",
		`{"successful":true,"commitId":"abcdef0123456789abcdef0123456789abcdef01","version":"1.0.0"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if len(environments.asked) != 0 {
		t.Fatalf("environment lookups = %v, want none for an omitted environmentId", environments.asked)
	}
}
