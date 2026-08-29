package routes

import (
	"context"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type TenantIssuerRepository interface {
	List(ctx context.Context) ([]model.TenantIssuer, error)
	UpdateName(ctx context.Context, issuer string, name string) (model.TenantIssuer, error)
	UpdateOrgScope(ctx context.Context, issuer, orgFieldKey, orgFieldValue string) (model.TenantIssuer, error)
}

type TenantIssuerRoutes struct {
	issuers TenantIssuerRepository
}

type updateTenantIssuerRequest struct {
	Issuer string `json:"issuer"`
	Name   string `json:"name"`
	// OrgFieldKey and OrgFieldValue convert a single-tenant issuer to an
	// org-scoped one. Both are required together: the key names the token
	// claim that selects a tenant, the value is this mapping's own org, and
	// setting either alone breaks resolution for the issuer's first tenant.
	OrgFieldKey   string `json:"orgFieldKey"`
	OrgFieldValue string `json:"orgFieldValue"`
}

func RegisterTenantIssuerRoutes(register ProtectedRouteRegistrar, issuers TenantIssuerRepository) {
	routes := TenantIssuerRoutes{issuers: issuers}
	register(http.MethodGet, "/v1/tenant-issuers", http.HandlerFunc(routes.listTenantIssuers))
	register(http.MethodPatch, "/v1/tenant-issuers", http.HandlerFunc(routes.updateTenantIssuerName))
}

func (r TenantIssuerRoutes) listTenantIssuers(w http.ResponseWriter, req *http.Request) {
	issuers, err := r.issuers.List(req.Context())
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, issuers)
}

func (r TenantIssuerRoutes) updateTenantIssuerName(w http.ResponseWriter, req *http.Request) {
	var input updateTenantIssuerRequest
	if err := decodeJSON(req, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.OrgFieldKey != "" || input.OrgFieldValue != "" {
		r.updateTenantIssuerOrgScope(w, req, input)
		return
	}
	issuer, err := r.issuers.UpdateName(req.Context(), input.Issuer, input.Name)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, issuer)
}

// updateTenantIssuerOrgScope converts an issuer to org-scoped. Unlike a
// rename, which touches only the caller's own mapping row, the org-scoping
// mode lives on the shared issuers row and therefore changes how every
// tenant's tokens on that issuer resolve — so it is operations-only, the same
// gate POST /v1/tenants applies for writing these root resolution tables.
func (r TenantIssuerRoutes) updateTenantIssuerOrgScope(w http.ResponseWriter, req *http.Request, input updateTenantIssuerRequest) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
		return
	}
	if securityContext.TenantType != string(model.TenantTypeOperations) {
		writeError(w, http.StatusForbidden, "converting an issuer to org-scoped is restricted to an operations tenant")
		return
	}
	if input.OrgFieldKey == "" || input.OrgFieldValue == "" {
		writeError(w, http.StatusBadRequest, "orgFieldKey and orgFieldValue are required together: the key names the claim that selects a tenant, the value is this mapping's own org")
		return
	}
	issuer, err := r.issuers.UpdateOrgScope(req.Context(), input.Issuer, input.OrgFieldKey, input.OrgFieldValue)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, issuer)
}
