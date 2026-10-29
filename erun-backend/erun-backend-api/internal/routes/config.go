package routes

import (
	"context"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

type ConfigTenantRepository interface {
	Current(ctx context.Context) (model.Tenant, error)
}

// ConfigRateLimitReader reads the invite-request rate-limit window.
type ConfigRateLimitReader interface {
	Get(ctx context.Context) (model.PlatformRateLimit, error)
}

// ConfigRoutes serves the console read model of the caller's tenant config.
type ConfigRoutes struct {
	tenants      ConfigTenantRepository
	environments EnvironmentRepository
	contexts     ContextRepository
	rateLimits   ConfigRateLimitReader
}

type configResponse struct {
	Tenant                              model.Tenant        `json:"tenant"`
	Environments                        []model.Environment `json:"environments"`
	Contexts                            []model.Context     `json:"contexts"`
	InviteRequestRateLimitWindowSeconds int                 `json:"inviteRequestRateLimitWindowSeconds"`
}

func RegisterConfigRoute(register ProtectedRouteRegistrar, tenants ConfigTenantRepository, environments EnvironmentRepository, contexts ContextRepository, rateLimits ConfigRateLimitReader) {
	routes := ConfigRoutes{tenants: tenants, environments: environments, contexts: contexts, rateLimits: rateLimits}
	register(http.MethodGet, "/v1/config", http.HandlerFunc(routes.getConfig))
}

func (r ConfigRoutes) getConfig(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	tenant, err := r.tenants.Current(ctx)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	environments, err := r.environments.List(ctx)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	contexts, err := r.contexts.List(ctx)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	rateLimit, err := r.rateLimits.Get(ctx)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, configResponse{
		Tenant:                              tenant,
		Environments:                        environments,
		Contexts:                            contexts,
		InviteRequestRateLimitWindowSeconds: rateLimit.InviteRequestWindowSeconds,
	})
}
