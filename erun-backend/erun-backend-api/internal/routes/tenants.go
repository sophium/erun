package routes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type TenantRepository interface {
	Create(ctx context.Context, params repository.CreateTenantParams) (model.Tenant, error)
	List(ctx context.Context) ([]model.Tenant, error)
}

type TenantRoutes struct {
	tenants TenantRepository
}

// createTenantRequest is the operations-only tenant-registration body.
// orgFieldKey/orgFieldValue are set only for an org-scoped (shared) issuer;
// a single-tenant issuer leaves both empty.
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
	register(http.MethodGet, "/v1/tenants", http.HandlerFunc(routes.listTenants))
}

// listTenants returns every tenant for an operations-scoped caller, or a
// single-item list containing only the caller's own tenant otherwise.
func (r TenantRoutes) listTenants(w http.ResponseWriter, req *http.Request) {
	tenants, err := r.tenants.List(req.Context())
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tenants)
}

// createTenant gates on an OPERATIONS tenant beyond the WriteAll permission POST
// already requires, because tenants/issuers/tenant_issuers are root resolution
// tables writable only by erun_operations.
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
		writeRepositoryError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, created)
}

// parseCreateTenantParams decodes and validates the tenant-registration body,
// returning a non-empty message (with its HTTP status) when the input must be
// rejected before ever reaching the repository.
func parseCreateTenantParams(req *http.Request) (repository.CreateTenantParams, int, string) {
	var body createTenantRequest
	if err := decodeJSON(req, &body); err != nil {
		return repository.CreateTenantParams{}, http.StatusBadRequest, "invalid request body"
	}

	name := strings.TrimSpace(body.Name)
	issuer := strings.TrimSpace(body.Issuer)
	// Enforce the no-hyphen tenant-name rule: the runtime namespace is
	// <tenant>-<env>, so a hyphen in the tenant would make the mapping
	// non-injective (a-b + c and a + b-c both collapse to a-b-c), a
	// cross-tenant namespace-collision/takeover vector on a public
	// provisioning surface.
	if err := eruncommon.ValidateTenantName(name); err != nil {
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

// duplicateIssuerMessage names the (issuer, org) mapping a create collided
// with, since a caller re-registering an already-mapped issuer needs to know
// whether to pick a different org discriminator or that the issuer is simply
// taken.
func duplicateIssuerMessage(issuer, orgFieldValue string) string {
	if orgFieldValue == "" {
		return fmt.Sprintf("issuer %q is already mapped to a tenant with no org discriminator; pass --org-field-key/--org-field-value to scope a shared issuer to a different org", issuer)
	}
	return fmt.Sprintf("issuer %q is already mapped for org value %q", issuer, orgFieldValue)
}
