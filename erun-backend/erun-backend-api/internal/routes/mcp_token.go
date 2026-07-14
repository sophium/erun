package routes

import (
	"net/http"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/mcptoken"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// MCPTokenRoutes mints a per-env MCP bearer token for the caller. The token is
// signed by the backend's MCP identity and carries the per-env audience, so a
// deployed env's erun-mcp edge (injected with the backend's public key)
// authenticates the console/agent without the caller holding a signing key.
type MCPTokenRoutes struct {
	environments EnvironmentRepository
	tenants      ConfigTenantRepository
	// signer is nil when no backend MCP signing key is configured; the handler
	// then reports 501 rather than minting an unverifiable token.
	signer *mcptoken.Signer
}

type mcpTokenResponse struct {
	Token    string `json:"token"`
	Audience string `json:"audience"`
}

func RegisterMCPTokenRoutes(register ProtectedRouteRegistrar, environments EnvironmentRepository, tenants ConfigTenantRepository, signer *mcptoken.Signer) {
	routes := MCPTokenRoutes{environments: environments, tenants: tenants, signer: signer}
	register(http.MethodPost, "/v1/environments/{environment_id}/mcp-token", http.HandlerFunc(routes.mintMCPToken))
}

func (r MCPTokenRoutes) mintMCPToken(w http.ResponseWriter, req *http.Request) {
	if r.signer == nil {
		writeError(w, http.StatusNotImplemented, "mcp token signing is not configured")
		return
	}
	ctx := req.Context()
	environment, err := r.environments.Get(ctx, req.PathValue("environment_id"))
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	tenant, err := r.tenants.Current(ctx)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
		return
	}
	token, audience, err := r.signer.Sign(tenant.Name, environment.Name, securityContext.ErunUserID, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
		return
	}
	writeJSON(w, http.StatusOK, mcpTokenResponse{Token: token, Audience: audience})
}
