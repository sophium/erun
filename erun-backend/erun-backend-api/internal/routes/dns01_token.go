package routes

import (
	"net/http"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/mcptoken"
)

// DNS01TokenRoutes mints a per-env DNS-01 broker token: the long-lived, backend-
// signed credential the cluster's cert-manager DNS-01 webhook presents to the
// broker so it can solve ACME challenges within the env's own subzone. Distinct
// capability from the MCP token (different audience), so the two cannot be
// replayed against each other.
type DNS01TokenRoutes struct {
	environments EnvironmentRepository
	tenants      ConfigTenantRepository
	// signer is nil when no backend MCP signing key is configured; the handler
	// then reports 501 rather than minting an unverifiable token.
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
	// Both reads are row-level-security scoped to the caller's tenant, so the
	// endpoint can only mint for the caller's own environment.
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
