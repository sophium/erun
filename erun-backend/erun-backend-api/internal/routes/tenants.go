package routes

import (
	"context"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type TenantRepository interface {
	Create(ctx context.Context, params repository.CreateTenantParams) (model.Tenant, error)
}

type TenantRoutes struct {
	tenants TenantRepository
}

// createTenantRequest is the operations-only tenant-registration body. It carries
// the tenant identity plus the OIDC issuer mapping that resolves tokens to the
// new tenant. orgFieldKey/orgFieldValue are set only for an org-scoped (shared)
// issuer; a single-tenant issuer leaves both empty.
type createTenantRequest struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	Issuer        string `json:"issuer"`
	OrgFieldKey   string `json:"orgFieldKey"`
	OrgFieldValue string `json:"orgFieldValue"`
	DisplayName   string `json:"displayName"`
}

func RegisterTenantRoutes(register ProtectedRouteRegistrar, tenants TenantRepository) {
	routes := TenantRoutes{tenants: tenants}
	register(http.MethodPost, "/v1/tenants", http.HandlerFunc(routes.createTenant))
}

// createTenant registers a new tenant plus its OIDC issuer mapping. Beyond the
// broad WriteAll permission that authorization middleware enforces for POST, this
// handler adds an explicit operations gate: the caller's resolved tenant must be
// an OPERATIONS tenant, because tenants/issuers/tenant_issuers are root
// resolution tables writable only by erun_operations. A non-OPERATIONS caller is
// rejected with 403 here, before any write is attempted.
func (r TenantRoutes) createTenant(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		// Protected routes always run behind authentication middleware that stamps
		// the security context, so a missing context is an internal wiring error,
		// not a client fault.
		writeError(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
		return
	}
	if securityContext.TenantType != string(model.TenantTypeOperations) {
		writeError(w, http.StatusForbidden, "tenant registration requires an operations tenant")
		return
	}

	var body createTenantRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	name := strings.TrimSpace(body.Name)
	issuer := strings.TrimSpace(body.Issuer)
	if name == "" || issuer == "" {
		writeError(w, http.StatusBadRequest, "name and issuer are required")
		return
	}
	tenantType := model.TenantType(strings.TrimSpace(body.Type))
	if tenantType == "" {
		tenantType = model.TenantTypeCompany
	}
	if tenantType != model.TenantTypeCompany && tenantType != model.TenantTypeOperations {
		writeError(w, http.StatusBadRequest, "type must be one of COMPANY, OPERATIONS")
		return
	}

	created, err := r.tenants.Create(req.Context(), repository.CreateTenantParams{
		Name:          name,
		Type:          tenantType,
		Issuer:        issuer,
		OrgFieldKey:   strings.TrimSpace(body.OrgFieldKey),
		OrgFieldValue: strings.TrimSpace(body.OrgFieldValue),
		DisplayName:   strings.TrimSpace(body.DisplayName),
	})
	if err != nil {
		writeRepositoryError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, created)
}
