package routes

import (
	"context"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	eruncommon "github.com/sophium/erun/erun-common"
)

type WhoamiUserRepository interface {
	Get(ctx context.Context, userID string) (model.User, error)
	RoleNames(ctx context.Context, userID string) ([]string, error)
}

// WhoamiTenantRepository resolves the caller's own tenant record off the
// exact same tenants.name column and TenantRepository.Current method that
// GET /v1/tenants reads (erun#2083: whoami used to carry no tenant name at
// all, and its plain-text line put the caller's own username where an
// operator naturally reads a tenant name, which is what made an ordinary
// username look like a tenant list disagreement). Reusing the identical read
// path is what makes the two answers structurally unable to diverge.
type WhoamiTenantRepository interface {
	Current(ctx context.Context) (model.Tenant, error)
}

// WhoamiCapabilityResolver narrows a candidate route set to the ones the caller
// may reach. The authorization middleware's own authorizer implements it, so
// the capability set a client renders from is computed by the code that
// enforces it rather than derived a second time.
type WhoamiCapabilityResolver interface {
	PermittedRoutes(ctx context.Context, candidates []eruncommon.PlatformCapability) ([]eruncommon.PlatformCapability, error)
}

// WhoamiRouteCatalog reports every registered route, evaluated per request so
// registration order cannot leave routes out of the answer.
type WhoamiRouteCatalog func() []eruncommon.PlatformCapability

type WhoamiRoutes struct {
	users        WhoamiUserRepository
	tenants      WhoamiTenantRepository
	capabilities WhoamiCapabilityResolver
	catalog      WhoamiRouteCatalog
}

func RegisterWhoamiRoute(register ProtectedRouteRegistrar, users WhoamiUserRepository, tenants WhoamiTenantRepository, capabilities WhoamiCapabilityResolver, catalog WhoamiRouteCatalog) {
	routes := WhoamiRoutes{users: users, tenants: tenants, capabilities: capabilities, catalog: catalog}
	register(http.MethodGet, "/v1/whoami", http.HandlerFunc(routes.handleWhoami))
}

type whoamiResponse struct {
	TenantID string `json:"tenantId"`
	// TenantName is the tenant's name exactly as GET /v1/tenants reports it
	// for this same TenantID -- see WhoamiTenantRepository.
	TenantName string   `json:"tenantName,omitempty"`
	UserID     string   `json:"userId"`
	Username   string   `json:"username,omitempty"`
	Roles      []string `json:"roles,omitempty"`
	// Capabilities is what a client gates its surfaces on. Roles is
	// descriptive: a role's name says nothing about what a tenant granted it.
	//
	// Deliberately not omitempty. A caller who may do nothing has to receive an
	// empty set rather than no field, because a client treats a missing set as
	// "this platform cannot tell me" and falls back to attempting every call.
	Capabilities eruncommon.PlatformCapabilities `json:"capabilities"`
	Issuer       string                          `json:"issuer"`
	Subject      string                          `json:"subject"`
}

func (routes WhoamiRoutes) handleWhoami(w http.ResponseWriter, r *http.Request) {
	securityContext, err := security.RequiredFromContext(r.Context())
	if err != nil {
		writeInternalError(w, r, http.StatusText(http.StatusInternalServerError), err)
		return
	}

	response := whoamiResponse{
		TenantID: securityContext.TenantID,
		UserID:   securityContext.ErunUserID,
		Issuer:   securityContext.ExternalIssuer,
		Subject:  securityContext.ExternalUserID,
	}
	if routes.tenants != nil {
		tenant, err := routes.tenants.Current(r.Context())
		if err != nil {
			writeRepositoryError(w, r, err)
			return
		}
		response.TenantName = tenant.Name
	}
	if routes.users != nil {
		user, err := routes.users.Get(r.Context(), securityContext.ErunUserID)
		if err != nil {
			writeRepositoryError(w, r, err)
			return
		}
		response.Username = user.Username
		roles, err := routes.users.RoleNames(r.Context(), securityContext.ErunUserID)
		if err != nil {
			writeRepositoryError(w, r, err)
			return
		}
		response.Roles = roles
	}
	// A capability set that cannot be resolved fails the request rather than
	// answering without one: an omitted set is indistinguishable from "you may
	// do nothing", and a client that degrades on it would hide surfaces the
	// caller can actually use.
	if routes.capabilities != nil && routes.catalog != nil {
		capabilities, err := routes.capabilities.PermittedRoutes(r.Context(), routes.catalog())
		if err != nil {
			writeRepositoryError(w, r, err)
			return
		}
		response.Capabilities = capabilities
	}

	writeJSON(w, http.StatusOK, response)
}
