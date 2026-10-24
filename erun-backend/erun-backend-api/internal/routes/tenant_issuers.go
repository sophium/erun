package routes

import (
	"context"
	"errors"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type TenantIssuerRepository interface {
	List(ctx context.Context, filter repository.TenantIssuerFilter) ([]model.TenantIssuer, error)
	UpdateName(ctx context.Context, issuer string, name string) (model.TenantIssuer, error)
	UpdateOrgScope(ctx context.Context, tenantID, issuer, orgFieldKey, orgFieldValue string) (model.TenantIssuer, error)
}

type TenantIssuerRoutes struct {
	issuers TenantIssuerRepository
}

type updateTenantIssuerRequest struct {
	Issuer string `json:"issuer"`
	Name   string `json:"name"`
	// OrgFieldKey and OrgFieldValue must be set together; either alone breaks resolution for the issuer's first tenant.
	OrgFieldKey   string `json:"orgFieldKey"`
	OrgFieldValue string `json:"orgFieldValue"`
	// TenantID targets another tenant's mapping, honored only for an operations-scoped caller.
	TenantID string `json:"tenantId,omitempty"`
}

func RegisterTenantIssuerRoutes(register ProtectedRouteRegistrar, issuers TenantIssuerRepository) {
	routes := TenantIssuerRoutes{issuers: issuers}
	register(http.MethodGet, "/v1/tenant-issuers", http.HandlerFunc(routes.listTenantIssuers))
	register(http.MethodPatch, "/v1/tenant-issuers", http.HandlerFunc(routes.updateTenantIssuerName))
}

func (r TenantIssuerRoutes) listTenantIssuers(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), errors.New("security context not found in request"))
		return
	}
	targetTenantID, err := resolveTargetTenant(securityContext, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	issuers, err := r.issuers.List(req.Context(), repository.TenantIssuerFilter{TenantID: targetTenantID})
	if err != nil {
		writeRepositoryError(w, req, err)
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
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, issuer)
}

// updateTenantIssuerOrgScope is operations-only: org scoping lives on the shared issuers row and
// changes how every tenant on that issuer resolves.
func (r TenantIssuerRoutes) updateTenantIssuerOrgScope(w http.ResponseWriter, req *http.Request, input updateTenantIssuerRequest) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), errors.New("security context not found in request"))
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
	targetTenantID, err := resolveTargetTenant(securityContext, input.TenantID)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	issuer, err := r.issuers.UpdateOrgScope(req.Context(), targetTenantID, input.Issuer, input.OrgFieldKey, input.OrgFieldValue)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, issuer)
}
