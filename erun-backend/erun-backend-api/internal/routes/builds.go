package routes

import (
	"context"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

type BuildRepository interface {
	Create(ctx context.Context, build model.Build) (model.Build, error)
	Get(ctx context.Context, reviewID string, buildID string) (model.Build, error)
	ListByReview(ctx context.Context, reviewID string) ([]model.Build, error)
}

type BuildRoutes struct {
	builds BuildRepository
}

func RegisterBuildRoutes(register ProtectedRouteRegistrar, builds BuildRepository) {
	routes := BuildRoutes{builds: builds}
	register(http.MethodGet, "/v1/reviews/{review_id}/builds", http.HandlerFunc(routes.listBuilds))
	register(http.MethodPost, "/v1/reviews/{review_id}/builds", http.HandlerFunc(routes.createBuild))
	register(http.MethodGet, "/v1/reviews/{review_id}/builds/{build_id}", http.HandlerFunc(routes.getBuild))
}

type createBuildRequest struct {
	Successful bool   `json:"successful"`
	CommitID   string `json:"commitId"`
	Version    string `json:"version"`
}

func (r BuildRoutes) listBuilds(w http.ResponseWriter, req *http.Request) {
	builds, err := r.builds.ListByReview(req.Context(), req.PathValue("review_id"))
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, builds)
}

func (r BuildRoutes) createBuild(w http.ResponseWriter, req *http.Request) {
	var input createBuildRequest
	if err := decodeJSON(req, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	build, err := r.builds.Create(req.Context(), model.Build{
		ReviewID:   req.PathValue("review_id"),
		Successful: input.Successful,
		CommitID:   input.CommitID,
		Version:    input.Version,
	})
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, build)
}

func (r BuildRoutes) getBuild(w http.ResponseWriter, req *http.Request) {
	build, err := r.builds.Get(req.Context(), req.PathValue("review_id"), req.PathValue("build_id"))
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, build)
}
