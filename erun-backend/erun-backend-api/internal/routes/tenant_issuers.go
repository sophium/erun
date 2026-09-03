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
	// OrgFieldKey and OrgFieldValue convert a single-tenant issuer to an
	// org-scoped one. Both are required together: the key names the token
	// claim that selects a tenant, the value is this mapping's own org, and
	// setting either alone breaks resolution for the issuer's first tenant.
	OrgFieldKey   string `json:"orgFieldKey"`
	OrgFieldValue string `json:"orgFieldValue"`
	// TenantID targets another tenant's mapping instead of the caller's own,
	// honored only alongside OrgFieldKey/OrgFieldValue and only for an
	// operations-scoped caller — the repair path for a tenant already stuck
	// with a dead (issuer, org) mapping (see resolveTargetTenant).
	TenantID string `json:"tenantId,omitempty"`
}

func RegisterTenantIssuerRoutes(register ProtectedRouteRegistrar, issuers TenantIssuerRepository) {
	routes := TenantIssuerRoutes{issuers: issuers}
	register(http.MethodGet, "/v1/tenant-issuers", http.HandlerFunc(routes.listTenantIssuers))
	register(http.MethodPatch, "/v1/tenant-issuers", http.HandlerFunc(routes.updateTenantIssuerName))
}

// listTenantIssuers defaults to the caller's own tenant, mirroring
// resolveTargetTenant's use in users.go/invites.go: an operations-scoped
// caller may pass tenantId to read another tenant's issuer mappings (the
// console uses this to resolve a target tenant's org before enrolling into
// it), and any other caller naming a different tenant is refused.
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

// updateTenantIssuerOrgScope converts an issuer to org-scoped and backfills
// the target mapping's own org value — the repair path for a tenant already
// stuck with an unresolvable mapping (assertResolvableIssuerMapping refuses
// producing a new one, but cannot undo one written before that refusal
// existed). The org-scoping mode lives on the shared issuers row and
// therefore changes how every tenant's tokens on that issuer resolve — so
// this is operations-only, the same gate POST /v1/tenants applies for writing
// these root resolution tables — and tenantId (via resolveTargetTenant) lets
// that operations caller repair a tenant other than its own, not just the
// bootstrap-mode case of fixing its own single-tenant-to-org-scoped issuer.
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
