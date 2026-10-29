package routes

import (
	"context"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	modelpkg "github.com/sophium/erun/erun-common/base/model"
)

type WhoamiUserRepository interface {
	Get(ctx context.Context, userID string) (model.User, error)
	RoleNames(ctx context.Context, userID string) ([]string, error)
}

// WhoamiTenantRepository reuses GET /v1/tenants' read path so whoami's tenant name cannot diverge from it.
type WhoamiTenantRepository interface {
	Current(ctx context.Context) (model.Tenant, error)
}

// WhoamiCapabilityResolver is implemented by the authorizer itself, so capabilities come from the enforcing code.
type WhoamiCapabilityResolver interface {
	PermittedRoutes(ctx context.Context, candidates []modelpkg.PlatformCapability) ([]modelpkg.PlatformCapability, error)
}

// WhoamiRouteCatalog is evaluated per request so registration order cannot drop routes.
type WhoamiRouteCatalog func() []modelpkg.PlatformCapability

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
	TenantID   string   `json:"tenantId"`
	TenantName string   `json:"tenantName,omitempty"`
	UserID     string   `json:"userId"`
	Username   string   `json:"username,omitempty"`
	Roles      []string `json:"roles,omitempty"`
	// Capabilities is not omitempty: a missing set means "platform cannot tell" to a client, which then attempts every call.
	Capabilities modelpkg.PlatformCapabilities `json:"capabilities"`
	Issuer       string                        `json:"issuer"`
	Subject      string                        `json:"subject"`
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
	// An unresolvable capability set fails the request; omitting it would read as "you may do nothing".
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
