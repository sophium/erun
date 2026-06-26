package routes

import (
	"context"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

type ConfigTenantRepository interface {
	Current(ctx context.Context) (model.Tenant, error)
}

// ConfigRoutes serves the console read model: the caller's tenant config
// denormalized as the on-disk erun config shape (tenant + environments +
// contexts). All three reads are tenant-scoped by RLS, so the response only
// ever contains the caller's own rows.
type ConfigRoutes struct {
	tenants      ConfigTenantRepository
	environments EnvironmentRepository
	contexts     ContextRepository
}

// configResponse is the denormalized read model the web console renders. It
// projects the per-tenant erun config: the tenant header plus its
// environments and the cloud contexts they reference.
type configResponse struct {
	Tenant       model.Tenant        `json:"tenant"`
	Environments []model.Environment `json:"environments"`
	Contexts     []model.Context     `json:"contexts"`
}

func RegisterConfigRoute(register ProtectedRouteRegistrar, tenants ConfigTenantRepository, environments EnvironmentRepository, contexts ContextRepository) {
	routes := ConfigRoutes{tenants: tenants, environments: environments, contexts: contexts}
	register(http.MethodGet, "/v1/config", http.HandlerFunc(routes.getConfig))
}

func (r ConfigRoutes) getConfig(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	tenant, err := r.tenants.Current(ctx)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	environments, err := r.environments.List(ctx)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	contexts, err := r.contexts.List(ctx)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, configResponse{
		Tenant:       tenant,
		Environments: environments,
		Contexts:     contexts,
	})
}
