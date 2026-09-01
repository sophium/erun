package routes

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/mcptoken"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	eruncommon "github.com/sophium/erun/erun-common"
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

// mintMCPTokenRequest lets the caller request a capability tier for the token
// this route mints. This route mints for arbitrary consumers -- the console
// handing a credential to its own operator, but also whatever reaches this
// endpoint next (a mobile client attaching to a session, erun#1106) -- so an
// absent Scope must not fall back to the desktop's own admin-by-default
// compatibility case (MCPCapabilitiesFromClaims): that default exists for a
// single operator's own local key, not for a route that mints on demand for
// callers it cannot identify. See defaultMintedMCPScope below.
type mintMCPTokenRequest struct {
	Scope string `json:"scope"`
}

// defaultMintedMCPScope is what this route mints when the caller does not ask
// for a scope: read-only observation, the least capability a token can carry
// and still be useful, never admin.
const defaultMintedMCPScope = string(eruncommon.MCPCapabilityRead)

type mcpTokenResponse struct {
	Token    string `json:"token"`
	Audience string `json:"audience"`
	Scope    string `json:"scope"`
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
	scope, err := requestedMCPScope(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := req.Context()
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
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), err)
		return
	}
	token, audience, err := r.signer.Sign(tenant.Name, environment.Name, securityContext.ErunUserID, scope, time.Now())
	if err != nil {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), err)
		return
	}
	writeJSON(w, http.StatusOK, mcpTokenResponse{Token: token, Audience: audience, Scope: scope})
}

// requestedMCPScope resolves the capability tier to mint. A body-less request
// is the common case (mint at the safe default), so only a malformed body or
// an unrecognized scope is an error -- the server validates against the fixed
// capability vocabulary (eruncommon.IsKnownMCPCapability) rather than minting
// whatever string the caller sent, because the caller asking for a capability
// is not the authority on whether it exists.
func requestedMCPScope(req *http.Request) (string, error) {
	var body mintMCPTokenRequest
	if err := decodeJSON(req, &body); err != nil && !errors.Is(err, io.EOF) {
		return "", errors.New("invalid request body")
	}
	scope := strings.TrimSpace(body.Scope)
	if scope == "" {
		return defaultMintedMCPScope, nil
	}
	for _, requested := range strings.Fields(scope) {
		if !eruncommon.IsKnownMCPCapability(requested) {
			return "", errors.New("unknown mcp token scope: " + requested)
		}
	}
	return scope, nil
}
