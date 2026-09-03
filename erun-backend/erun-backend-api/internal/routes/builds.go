package routes

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

type BuildRepository interface {
	Get(ctx context.Context, buildID string) (model.Build, error)
	List(ctx context.Context, filter apirepository.BuildFilter) ([]model.Build, error)
	ListPage(ctx context.Context, filter apirepository.BuildListFilter) (apirepository.BuildPage, error)
}

type BuildService interface {
	Create(ctx context.Context, build model.Build) (model.Build, error)
}

type BuildRoutes struct {
	builds  BuildRepository
	service BuildService
}

func RegisterBuildRoutes(register ProtectedRouteRegistrar, builds BuildRepository, service BuildService) {
	routes := BuildRoutes{builds: builds, service: service}
	register(http.MethodGet, "/v1/reviews/{review_id}/builds", http.HandlerFunc(routes.listBuilds))
	register(http.MethodPost, "/v1/reviews/{review_id}/builds", http.HandlerFunc(routes.createBuild))
	register(http.MethodGet, "/v1/reviews/{review_id}/builds/{build_id}", http.HandlerFunc(routes.getBuild))
	register(http.MethodGet, "/v1/builds", http.HandlerFunc(routes.listAllBuilds))
	register(http.MethodPost, "/v1/builds", http.HandlerFunc(routes.createUnattachedBuild))
}

func (r BuildRoutes) listBuilds(w http.ResponseWriter, req *http.Request) {
	builds, err := r.builds.List(req.Context(), apirepository.BuildFilter{ReviewID: req.PathValue("review_id")})
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, builds)
}

func (r BuildRoutes) createBuild(w http.ResponseWriter, req *http.Request) {
	var build model.Build
	if err := decodeJSON(req, &build); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	build.ReviewID = req.PathValue("review_id")
	// A caller may report either kind now: RECORDED for its own build, or GATE
	// for a merge-queue gate it ran itself — see AGENTS.md "Merge Queue". An
	// unrecognized kind is refused rather than silently coerced to RECORDED.
	if build.Kind == "" {
		build.Kind = model.BuildKindRecorded
	}
	if build.Kind != model.BuildKindRecorded && build.Kind != model.BuildKindGate {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_BODY", "kind must be RECORDED or GATE")
		return
	}
	build, err := r.service.Create(req.Context(), build)
	if err != nil {
		writeBuildError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, build)
}

// createUnattachedBuild is POST /v1/builds: an ordinary `erun build`
// self-reporting outside any review (erun#1954). ReviewID is always cleared
// regardless of what the body carries -- a review-linked build is reported
// through the review-nested route above, never through this one, so there is
// exactly one way to report each kind of build. Kind GATE is refused here:
// a gate build always gates a specific review's merge (see
// GateBuildRequiresReviewError), so it has no unattached form.
func (r BuildRoutes) createUnattachedBuild(w http.ResponseWriter, req *http.Request) {
	var build model.Build
	if err := decodeJSON(req, &build); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	build.ReviewID = ""
	if build.Kind == "" {
		build.Kind = model.BuildKindRecorded
	}
	if build.Kind != model.BuildKindRecorded {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_BODY", "kind must be RECORDED for an unattached build")
		return
	}
	build, err := r.service.Create(req.Context(), build)
	if err != nil {
		writeBuildError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, build)
}

type buildsPageResponse struct {
	Builds     []model.Build `json:"builds"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

// listAllBuilds is GET /v1/builds: the tenant-wide, paginated build history
// covering both review-linked and unattached builds -- unlike the
// review-nested list above, which is naturally bounded to one review and
// stays unpaginated.
func (r BuildRoutes) listAllBuilds(w http.ResponseWriter, req *http.Request) {
	filter, err := parseBuildListFilter(req.URL.Query())
	if err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_QUERY", err.Error())
		return
	}
	page, err := r.builds.ListPage(req.Context(), filter)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, buildsPageResponse{Builds: page.Builds, NextCursor: page.NextCursor})
}

func parseBuildListFilter(query url.Values) (apirepository.BuildListFilter, error) {
	get := func(key string) string { return strings.TrimSpace(query.Get(key)) }

	filter := apirepository.BuildListFilter{
		EnvironmentID: get("environmentId"),
		Kind:          model.BuildKind(get("kind")),
	}
	if successful := get("successful"); successful != "" {
		parsed, err := strconv.ParseBool(successful)
		if err != nil {
			return apirepository.BuildListFilter{}, apirepository.ErrInvalidInput
		}
		filter.Successful = &parsed
	}
	var err error
	if filter.Since, err = parseBuildListTime(get("since")); err != nil {
		return apirepository.BuildListFilter{}, err
	}
	if filter.Until, err = parseBuildListTime(get("until")); err != nil {
		return apirepository.BuildListFilter{}, err
	}
	if filter.Cursor, err = apirepository.ParseBuildCursor(get("cursor")); err != nil {
		return apirepository.BuildListFilter{}, err
	}
	if limit := get("limit"); limit != "" {
		parsed, err := strconv.Atoi(limit)
		if err != nil {
			return apirepository.BuildListFilter{}, apirepository.ErrInvalidInput
		}
		filter.Limit = parsed
	}
	return filter, nil
}

func parseBuildListTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, apirepository.ErrInvalidInput
	}
	return parsed, nil
}

// writeBuildError gives builds.md's documented business codes their exact
// machine code; every other failure falls through to the generic
// status-derived code.
func writeBuildError(w http.ResponseWriter, req *http.Request, err error) {
	var invalidCommitID *service.InvalidCommitIDError
	if errors.As(err, &invalidCommitID) {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_COMMIT_ID", invalidCommitID.Error())
		return
	}
	var invalidVersion *service.InvalidVersionError
	if errors.As(err, &invalidVersion) {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_VERSION", invalidVersion.Error())
		return
	}
	var missingFailureDetail *service.MissingFailureDetailError
	if errors.As(err, &missingFailureDetail) {
		writeErrorDetails(w, http.StatusBadRequest, "INVALID_BODY", missingFailureDetail.Error(), map[string]any{"field": "failureDetail"})
		return
	}
	var gateRequiresReview *service.GateBuildRequiresReviewError
	if errors.As(err, &gateRequiresReview) {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_BODY", gateRequiresReview.Error())
		return
	}
	var unattachedRequiresEnvironment *service.UnattachedBuildRequiresEnvironmentError
	if errors.As(err, &unattachedRequiresEnvironment) {
		writeErrorDetails(w, http.StatusBadRequest, "INVALID_BODY", unattachedRequiresEnvironment.Error(), map[string]any{"field": "environmentId"})
		return
	}
	writeRepositoryError(w, req, err)
}

func (r BuildRoutes) getBuild(w http.ResponseWriter, req *http.Request) {
	build, err := r.builds.Get(req.Context(), req.PathValue("build_id"))
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, build)
}
