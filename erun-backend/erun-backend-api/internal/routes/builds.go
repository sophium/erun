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
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

type BuildRepository interface {
	// Get is tenant-scoped because erun_operations' RLS policy is unconditional.
	Get(ctx context.Context, tenantID, buildID string) (model.Build, error)
	List(ctx context.Context, filter apirepository.BuildFilter) ([]model.Build, error)
	ListPage(ctx context.Context, filter apirepository.BuildListFilter) (apirepository.BuildPage, error)
}

type BuildService interface {
	Create(ctx context.Context, build model.Build) (model.Build, error)
}

type BuildRoutes struct {
	builds       BuildRepository
	environments EnvironmentGetter
	service      BuildService
}

func RegisterBuildRoutes(register ProtectedRouteRegistrar, builds BuildRepository, environments EnvironmentGetter, service BuildService) {
	routes := BuildRoutes{builds: builds, environments: environments, service: service}
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
	if err := r.requireEnvironment(req.Context(), build.EnvironmentID); err != nil {
		writeBuildError(w, req, err)
		return
	}
	if err := normalizeReportedBuildKind(&build); err != nil {
		writeBuildError(w, req, err)
		return
	}
	build, err := r.service.Create(req.Context(), build)
	if err != nil {
		writeBuildError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, build)
}

// requireEnvironment refuses an environmentId the caller's tenant cannot see,
// answering cross-tenant and nonexistent ids identically so the route never
// reveals whether an id exists. An empty id passes.
func (r BuildRoutes) requireEnvironment(ctx context.Context, environmentID string) error {
	if strings.TrimSpace(environmentID) == "" {
		return nil
	}
	_, err := r.environments.Get(ctx, environmentID)
	return err
}

// createUnattachedBuild is POST /v1/builds; ReviewID is always cleared.
func (r BuildRoutes) createUnattachedBuild(w http.ResponseWriter, req *http.Request) {
	var build model.Build
	if err := decodeJSON(req, &build); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	build.ReviewID = ""
	if err := r.requireEnvironment(req.Context(), build.EnvironmentID); err != nil {
		writeBuildError(w, req, err)
		return
	}
	if err := normalizeReportedBuildKind(&build); err != nil {
		writeBuildError(w, req, err)
		return
	}
	build, err := r.service.Create(req.Context(), build)
	if err != nil {
		writeBuildError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, build)
}

// normalizeReportedBuildKind defaults an omitted kind to RECORDED and refuses others.
func normalizeReportedBuildKind(build *model.Build) error {
	if build.Kind == "" {
		build.Kind = model.BuildKindRecorded
	}
	if build.Kind != model.BuildKindRecorded {
		return &service.UnsupportedBuildKindError{Kind: build.Kind}
	}
	return nil
}

type buildsPageResponse struct {
	Builds     []model.Build `json:"builds"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

// listAllBuilds is GET /v1/builds, the paginated tenant-wide build history.
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
		CommitID:      get("commit"),
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

// writeBuildError maps builds.md's documented business codes.
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
	var unsupportedKind *service.UnsupportedBuildKindError
	if errors.As(err, &unsupportedKind) {
		writeErrorDetails(w, http.StatusBadRequest, "INVALID_BODY", unsupportedKind.Error(), map[string]any{"field": "kind"})
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
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), errors.New("security context not found in request"))
		return
	}
	build, err := r.builds.Get(req.Context(), securityContext.TenantID, req.PathValue("build_id"))
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, build)
}
