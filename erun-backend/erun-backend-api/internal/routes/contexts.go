package routes

import (
	"context"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

type ContextRepository interface {
	List(ctx context.Context) ([]model.Context, error)
	Get(ctx context.Context, contextID string) (model.Context, error)
}

type ContextRoutes struct {
	contexts ContextRepository
}

func RegisterContextRoutes(register ProtectedRouteRegistrar, contexts ContextRepository) {
	routes := ContextRoutes{contexts: contexts}
	register(http.MethodGet, "/v1/contexts", http.HandlerFunc(routes.listContexts))
	register(http.MethodGet, "/v1/contexts/{context_id}", http.HandlerFunc(routes.getContext))
}

func (r ContextRoutes) listContexts(w http.ResponseWriter, req *http.Request) {
	contexts, err := r.contexts.List(req.Context())
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, contexts)
}

func (r ContextRoutes) getContext(w http.ResponseWriter, req *http.Request) {
	cloudContext, err := r.contexts.Get(req.Context(), req.PathValue("context_id"))
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cloudContext)
}
