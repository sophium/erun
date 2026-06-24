package routes

import (
	"context"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

type EnvironmentRepository interface {
	List(ctx context.Context) ([]model.Environment, error)
	Get(ctx context.Context, environmentID string) (model.Environment, error)
}

type EnvironmentRoutes struct {
	environments EnvironmentRepository
}

func RegisterEnvironmentRoutes(register ProtectedRouteRegistrar, environments EnvironmentRepository) {
	routes := EnvironmentRoutes{environments: environments}
	register(http.MethodGet, "/v1/environments", http.HandlerFunc(routes.listEnvironments))
	register(http.MethodGet, "/v1/environments/{environment_id}", http.HandlerFunc(routes.getEnvironment))
}

func (r EnvironmentRoutes) listEnvironments(w http.ResponseWriter, req *http.Request) {
	environments, err := r.environments.List(req.Context())
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, environments)
}

func (r EnvironmentRoutes) getEnvironment(w http.ResponseWriter, req *http.Request) {
	environment, err := r.environments.Get(req.Context(), req.PathValue("environment_id"))
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, environment)
}
