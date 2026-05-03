package routes

import (
	"context"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	build.ReviewID = req.PathValue("review_id")
	build, err := r.service.Create(req.Context(), build)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, build)
}

func (r BuildRoutes) getBuild(w http.ResponseWriter, req *http.Request) {
	build, err := r.builds.Get(req.Context(), req.PathValue("build_id"))
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, build)
}
