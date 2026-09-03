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
	eruncommon "github.com/sophium/erun/erun-common"
)

// EntitlementChecker answers whether the calling security context already
// holds a specific backend permission. repository.PermissionAuthorizer
// implements this by construction (same Authorize(ctx, method, apiPath)
// shape backendapi.Authorizer already uses for route-level enforcement), so
// mcp_token.go reuses the existing permission model instead of inventing a
// parallel one.
type EntitlementChecker interface {
	Authorize(ctx context.Context, method string, apiPath string) error
}

// adminEntitlementMethod/Path is the routeroles.TenantAdminOnly permission
// this route treats as a stand-in for "may mint erun:admin" (mapping
// decision, erun#1891). routeroles' backend classes and MCP's capability
// tiers do not line up 1:1: this route's own classification is
// TenantUserClass ("operating an environment that already exists"), and
// erun:read/erun:attach/erun:operate are exactly that -- observation, driving
// an existing attach session, and driving an existing environment's own
// lifecycle (deploy/context_start/context_stop/resize, erun#1107), nothing an
// ordinary TenantUser cannot already do through the API. erun:admin is not a
// peer of that class: MCPToolCapability's default-closed table puts
// delete/context_init/terraform/init (each TenantAdminOnly on the backend) in
// the very same tier as exec_raw and every other tool that runs arbitrary
// code, so a caller must separately hold a TenantAdminOnly permission to
// receive it -- reaching this route at all is not enough. DELETE on this
// exact environment is the narrowest available anchor for that check:
// whatever role structure exists in the future, "can this caller delete this
// environment" is the closest single permission to "should this caller be
// trusted with admin (including exec_raw) reach into it" -- the more
// restrictive reading where the mapping is not exact. A caller entitled to
// none of read/attach/operate never reaches this check at all: the route's
// own TenantUserClass gate already refused them before the handler runs.
const (
	adminEntitlementMethod = http.MethodDelete
	adminEntitlementPath   = "/v1/environments/{environment_id}"
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
	// entitlement gates erun:admin minting on adminEntitlementMethod/Path (see
	// their doc comment). Never consulted for erun:read/erun:attach, which the
	// route's own TenantUserClass gate already entitles every caller to.
	entitlement EntitlementChecker
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
		// A bare ErrForbidden renders as the generic "Forbidden" through
		// writeRepositoryError's shared mapping -- indistinguishable, to the
		// console operator reading it, from a broken request. This is the one
		// call site where the caller chose a specific escalation (the scope
		// selector's "Admin" option) and the specific reason it was refused is
		// always the same one (see authorizeScope's doc comment), so naming it
		// costs nothing and saves an operator from reporting a bug that is
		// actually a missing permission.
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

// authorizeScope refuses a requested erun:admin scope unless the caller
// separately holds adminEntitlementMethod/Path (see that constant's doc
// comment for the mapping this enforces). erun:read, erun:attach, and
// erun:operate need no check here: this route is itself
// routeroles.TenantUserClass, so reaching the handler already proves that
// entitlement. A nil entitlement checker (no permission backend wired) fails
// closed rather than minting admin unconditionally.
func (r MCPTokenRoutes) authorizeScope(ctx context.Context, scope string) error {
	requestsAdmin := false
	for _, requested := range strings.Fields(scope) {
		if requested == string(eruncommon.MCPCapabilityAdmin) {
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
