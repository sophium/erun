package routes

import (
	"net/http"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/mcptoken"
)

// DNS01TokenRoutes mints a per-env DNS-01 broker token for cert-manager's
// webhook. Its audience differs from the MCP token so neither replays as the other.
type DNS01TokenRoutes struct {
	environments EnvironmentRepository
	tenants      ConfigTenantRepository
	// nil signer makes the handler report 501.
	signer *mcptoken.Signer
}

type dns01TokenResponse struct {
	Token    string `json:"token"`
	Audience string `json:"audience"`
}

func RegisterDNS01TokenRoutes(register ProtectedRouteRegistrar, environments EnvironmentRepository, tenants ConfigTenantRepository, signer *mcptoken.Signer) {
	routes := DNS01TokenRoutes{environments: environments, tenants: tenants, signer: signer}
	register(http.MethodPost, "/v1/environments/{environment_id}/dns01-token", http.HandlerFunc(routes.mintDNS01Token))
}

func (r DNS01TokenRoutes) mintDNS01Token(w http.ResponseWriter, req *http.Request) {
	if r.signer == nil {
		writeError(w, http.StatusNotImplemented, "dns01 token signing is not configured")
		return
	}
	ctx := req.Context()
	// RLS-scoped, so only the caller's own environment can be minted for.
	environment, err := r.environments.Get(ctx, req.PathValue("environment_id"))
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	tenant, err := r.tenants.Current(ctx)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	token, audience, err := r.signer.SignDNS01(tenant.Name, environment.Name, time.Now())
	if err != nil {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), err)
		return
	}
	writeJSON(w, http.StatusOK, dns01TokenResponse{Token: token, Audience: audience})
}
