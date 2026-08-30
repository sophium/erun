package routes

import (
	"context"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

type ConfigTenantRepository interface {
	Current(ctx context.Context) (model.Tenant, error)
}

// ConfigRateLimitReader reads the platform's current invite-request
// rate-limit window. *repository.PlatformRateLimitRepository satisfies it.
type ConfigRateLimitReader interface {
	Get(ctx context.Context) (model.PlatformRateLimit, error)
}

// ConfigRoutes serves the console read model: the caller's tenant config
// denormalized as the on-disk erun config shape. RLS tenant-scopes every read,
// so the response only ever contains the caller's own rows.
type ConfigRoutes struct {
	tenants      ConfigTenantRepository
	environments EnvironmentRepository
	contexts     ContextRepository
	rateLimits   ConfigRateLimitReader
}

type configResponse struct {
	Tenant       model.Tenant        `json:"tenant"`
	Environments []model.Environment `json:"environments"`
	Contexts     []model.Context     `json:"contexts"`
	// InviteRequestRateLimitWindowSeconds is the current
	// POST /v1/invite-requests admission window (issue §9: "the desktop
	// reflects the current window"), changed only through
	// PATCH /v1/config/invite-request-rate-limit.
	InviteRequestRateLimitWindowSeconds int `json:"inviteRequestRateLimitWindowSeconds"`
}

func RegisterConfigRoute(register ProtectedRouteRegistrar, tenants ConfigTenantRepository, environments EnvironmentRepository, contexts ContextRepository, rateLimits ConfigRateLimitReader) {
	routes := ConfigRoutes{tenants: tenants, environments: environments, contexts: contexts, rateLimits: rateLimits}
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
	rateLimit, err := r.rateLimits.Get(ctx)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, configResponse{
		Tenant:                              tenant,
		Environments:                        environments,
		Contexts:                            contexts,
		InviteRequestRateLimitWindowSeconds: rateLimit.InviteRequestWindowSeconds,
	})
}
