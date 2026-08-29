package routes

import (
	"context"
	"errors"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

type BuildRepository interface {
	Get(ctx context.Context, buildID string) (model.Build, error)
	List(ctx context.Context, filter apirepository.BuildFilter) ([]model.Build, error)
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
}

func (r BuildRoutes) listBuilds(w http.ResponseWriter, req *http.Request) {
	builds, err := r.builds.List(req.Context(), apirepository.BuildFilter{ReviewID: req.PathValue("review_id")})
	if err != nil {
		writeRepositoryError(w, err)
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
		writeBuildError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, build)
}

// writeBuildError gives builds.md's documented business codes their exact
// machine code; every other failure falls through to the generic
// status-derived code.
func writeBuildError(w http.ResponseWriter, err error) {
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
	writeRepositoryError(w, err)
}

func (r BuildRoutes) getBuild(w http.ResponseWriter, req *http.Request) {
	build, err := r.builds.Get(req.Context(), req.PathValue("build_id"))
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, build)
}
