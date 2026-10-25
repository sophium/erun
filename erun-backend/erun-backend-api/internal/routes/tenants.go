package routes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
)

type TenantRepository interface {
	Create(ctx context.Context, params repository.CreateTenantParams) (model.Tenant, error)
	List(ctx context.Context) ([]model.Tenant, error)
	Reachable(ctx context.Context) ([]model.Tenant, error)
	Current(ctx context.Context) (model.Tenant, error)
}

// TenantBootstrapNameReconciler is the one-way, operations-only rename workflow.
type TenantBootstrapNameReconciler interface {
	ReconcileBootstrapName(ctx context.Context, tenant model.Tenant) (model.Tenant, error)
}

type TenantRoutes struct {
	tenants    TenantRepository
	reconciler TenantBootstrapNameReconciler
}

type createTenantRequest struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	Issuer        string `json:"issuer"`
	OrgFieldKey   string `json:"orgFieldKey"`
	OrgFieldValue string `json:"orgFieldValue"`
	DisplayName   string `json:"displayName"`
}

func RegisterTenantRoutes(register ProtectedRouteRegistrar, tenants TenantRepository, reconciler TenantBootstrapNameReconciler) {
	routes := TenantRoutes{tenants: tenants, reconciler: reconciler}
	register(http.MethodPost, "/v1/tenants", http.HandlerFunc(routes.createTenant))
	register(http.MethodGet, "/v1/tenants", http.HandlerFunc(routes.listTenants))
	register(http.MethodGet, "/v1/tenants/reachable", http.HandlerFunc(routes.reachableTenants))
	register(http.MethodPatch, "/v1/tenants/reconcile-bootstrap-name", http.HandlerFunc(routes.reconcileBootstrapName))
}

func (r TenantRoutes) listTenants(w http.ResponseWriter, req *http.Request) {
	tenants, err := r.tenants.List(req.Context())
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, tenants)
}

// reachableTenants is the one route allowed to cross tenant scope on the caller's behalf; see TenantRepository.Reachable.
func (r TenantRoutes) reachableTenants(w http.ResponseWriter, req *http.Request) {
	tenants, err := r.tenants.Reachable(req.Context())
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, tenants)
}

// createTenant requires an OPERATIONS tenant: these root resolution tables are writable only by erun_operations.
func (r TenantRoutes) createTenant(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), errors.New("security context not found in request"))
		return
	}
	if securityContext.TenantType != string(model.TenantTypeOperations) {
		writeError(w, http.StatusForbidden, "tenant registration requires an operations tenant")
		return
	}

	params, status, msg := parseCreateTenantParams(req)
	if msg != "" {
		writeError(w, status, msg)
		return
	}

	created, err := r.tenants.Create(req.Context(), params)
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			writeError(w, http.StatusConflict, duplicateIssuerMessage(params.Issuer, params.OrgFieldValue))
			return
		}
		writeRepositoryError(w, req, err)
		return
	}

	writeJSON(w, http.StatusCreated, created)
}

// reconcileBootstrapName renames the caller's tenant to this instance's ERUN_TENANT. It takes no body so it
// cannot become a general rename API.
func (r TenantRoutes) reconcileBootstrapName(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), errors.New("security context not found in request"))
		return
	}
	if securityContext.TenantType != string(model.TenantTypeOperations) {
		writeError(w, http.StatusForbidden, "reconciling the bootstrap tenant name is restricted to an operations tenant")
		return
	}
	if r.reconciler == nil {
		writeError(w, http.StatusNotImplemented, "bootstrap tenant name reconciliation is not configured")
		return
	}

	current, err := r.tenants.Current(req.Context())
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}

	reconciled, err := r.reconciler.ReconcileBootstrapName(req.Context(), current)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrBootstrapNameNotConfigured):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, service.ErrBootstrapNameHasEnvironments):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, service.ErrBootstrapNameConflict):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeRepositoryError(w, req, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, reconciled)
}

func parseCreateTenantParams(req *http.Request) (repository.CreateTenantParams, int, string) {
	var body createTenantRequest
	if err := decodeJSON(req, &body); err != nil {
		return repository.CreateTenantParams{}, http.StatusBadRequest, "invalid request body"
	}

	name := strings.TrimSpace(body.Name)
	issuer := strings.TrimSpace(body.Issuer)
	// The runtime namespace is <tenant>-<env>, so a hyphenated tenant would allow cross-tenant namespace collisions.
	if err := runtimetarget.ValidateTenantName(name); err != nil {
		return repository.CreateTenantParams{}, http.StatusBadRequest, err.Error()
	}
	if issuer == "" {
		return repository.CreateTenantParams{}, http.StatusBadRequest, "issuer is required"
	}
	tenantType := model.TenantType(strings.TrimSpace(body.Type))
	if tenantType == "" {
		tenantType = model.TenantTypeCompany
	}
	if tenantType != model.TenantTypeCompany && tenantType != model.TenantTypeOperations {
		return repository.CreateTenantParams{}, http.StatusBadRequest, "type must be one of COMPANY, OPERATIONS"
	}

	return repository.CreateTenantParams{
		Name:          name,
		Type:          tenantType,
		Issuer:        issuer,
		OrgFieldKey:   strings.TrimSpace(body.OrgFieldKey),
		OrgFieldValue: strings.TrimSpace(body.OrgFieldValue),
		DisplayName:   strings.TrimSpace(body.DisplayName),
	}, 0, ""
}

// duplicateIssuerMessage points at the org-scope conversion route: ON CONFLICT DO NOTHING means retrying
// this create can never change an existing issuer's scoping.
func duplicateIssuerMessage(issuer, orgFieldValue string) string {
	if orgFieldValue == "" {
		return fmt.Sprintf("issuer %q is already mapped to a tenant with no org discriminator; an operations caller must first convert it to org-scoped via PATCH /v1/tenant-issuers (orgFieldKey/orgFieldValue), which backfills the existing tenant's mapping, before a second tenant can be created on it with --org-field-key/--org-field-value", issuer)
	}
	return fmt.Sprintf("issuer %q is already mapped for org value %q", issuer, orgFieldValue)
}
