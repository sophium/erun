package routes

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/mcptoken"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-common/infra/mcptools"
)

// EntitlementChecker reports whether the caller holds a backend permission.
type EntitlementChecker interface {
	Authorize(ctx context.Context, method string, apiPath string) error
}

// erun:admin shares a tier with exec_raw, so minting it additionally requires
// DELETE on this environment, the narrowest TenantAdminOnly anchor.
const (
	adminEntitlementMethod = http.MethodDelete
	adminEntitlementPath   = "/v1/environments/{environment_id}"
)

// MCPTokenRoutes mints a per-env MCP bearer token signed by the backend's MCP
// identity, so the env's MCP edge can verify it without the caller holding a key.
type MCPTokenRoutes struct {
	environments EnvironmentRepository
	tenants      ConfigTenantRepository
	// signer is nil when no signing key is configured; the handler then answers 501.
	signer *mcptoken.Signer
	// entitlement is consulted only for erun:admin.
	entitlement EntitlementChecker
}

// mintMCPTokenRequest.Scope must not default to admin: this route mints for
// callers it cannot identify, unlike the desktop's local key.
type mintMCPTokenRequest struct {
	Scope string `json:"scope"`
}

// defaultMintedMCPScope is read-only, never admin.
const defaultMintedMCPScope = string(mcptools.MCPCapabilityRead)

type mcpTokenResponse struct {
	Token    string `json:"token"`
	Audience string `json:"audience"`
	Scope    string `json:"scope"`
}

func RegisterMCPTokenRoutes(register ProtectedRouteRegistrar, environments EnvironmentRepository, tenants ConfigTenantRepository, signer *mcptoken.Signer, entitlement EntitlementChecker) {
	routes := MCPTokenRoutes{environments: environments, tenants: tenants, signer: signer, entitlement: entitlement}
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
	if err := r.authorizeScope(ctx, scope); err != nil {
		// Name the missing permission so an operator does not report it as a bug.
		if errors.Is(err, repository.ErrForbidden) {
			writeErrorCode(w, http.StatusForbidden, "MCP_ADMIN_SCOPE_FORBIDDEN",
				"minting an erun:admin MCP token requires permission to delete this environment; ask your tenant admin for that access, or request erun:operate instead")
			return
		}
		writeRepositoryError(w, req, err)
		return
	}
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

// authorizeScope checks only erun:admin; the route's TenantUserClass gate covers
// the rest. A nil checker fails closed.
func (r MCPTokenRoutes) authorizeScope(ctx context.Context, scope string) error {
	requestsAdmin := false
	for _, requested := range strings.Fields(scope) {
		if requested == string(mcptools.MCPCapabilityAdmin) {
			requestsAdmin = true
			break
		}
	}
	if !requestsAdmin {
		return nil
	}
	if r.entitlement == nil {
		return repository.ErrForbidden
	}
	return r.entitlement.Authorize(ctx, adminEntitlementMethod, adminEntitlementPath)
}

// requestedMCPScope validates against the known capability vocabulary rather
// than minting whatever the caller sent.
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
		if !mcptools.IsKnownMCPCapability(requested) {
			return "", errors.New("unknown mcp token scope: " + requested)
		}
	}
	return scope, nil
}
