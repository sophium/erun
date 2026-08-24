package routes

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"
)

// IdentityAdminClient is the Zitadel Management API surface these routes
// call directly (list, deactivate, reactivate, org settings); *zitadel.Client
// satisfies it. Enrollment goes through IdentityEnroller instead, since it
// coordinates the IdP call with the erun-side user mapping.
type IdentityAdminClient interface {
	ListUsers(ctx context.Context) ([]zitadel.User, error)
	DeactivateUser(ctx context.Context, userID string) error
	ReactivateUser(ctx context.Context, userID string) error
	GetOrgSettings(ctx context.Context) (zitadel.OrgSettings, error)
	UpdateOrgSettings(ctx context.Context, params zitadel.UpdateOrgSettingsParams) (zitadel.OrgSettings, error)
}

// IdentityEnroller creates an IdP identity and its erun user mapping as one
// enrollment action. *service.IdentityService satisfies it.
type IdentityEnroller interface {
	Enroll(ctx context.Context, params service.EnrollIdentityParams) (service.EnrollIdentityResult, error)
}

type IdentityRoutes struct {
	admin    IdentityAdminClient
	enroller IdentityEnroller
}

// RegisterIdentityRoutes registers the identity-administration surface
// (issue #1209): the erun-zitadel chart provisions an org-owner Management
// API credential on every deployment, and until this, nothing consumed it —
// enrolling a colleague required a second browser tab on Zitadel's own admin
// console. Every operation here is named individually; this is deliberately
// not a generic proxy over the Management API (see the internal/zitadel
// package doc for the least-privilege reasoning). Restricted to an
// OPERATIONS tenant by every handler below: administering the platform's IdP
// is not a company tenant's business, and effective-permission gating still
// applies on top via the normal role_permissions mechanism every registered
// route already gets.
func RegisterIdentityRoutes(register ProtectedRouteRegistrar, admin IdentityAdminClient, enroller IdentityEnroller) {
	routes := IdentityRoutes{admin: admin, enroller: enroller}
	register(http.MethodGet, "/v1/identity/users", http.HandlerFunc(routes.listUsers))
	register(http.MethodPost, "/v1/identity/users", http.HandlerFunc(routes.createUser))
	register(http.MethodPost, "/v1/identity/users/{external_id}/deactivate", http.HandlerFunc(routes.deactivateUser))
	register(http.MethodPost, "/v1/identity/users/{external_id}/reactivate", http.HandlerFunc(routes.reactivateUser))
	register(http.MethodGet, "/v1/identity/org-settings", http.HandlerFunc(routes.getOrgSettings))
	register(http.MethodPatch, "/v1/identity/org-settings", http.HandlerFunc(routes.updateOrgSettings))
}

var errIdentityAdminForbidden = errors.New("identity administration is restricted to an operations tenant")

// requireOperationsTenant is the shared gate every handler below applies
// first. Unlike resolveTargetTenant in users.go (which lets an operations
// caller opt into acting on another tenant), this surface has no
// company-tenant case at all: the IdP behind it is the platform's own, not a
// per-tenant resource an operations caller crosses into.
func requireOperationsTenant(securityContext security.Context) error {
	if securityContext.TenantType != string(model.TenantTypeOperations) {
		return errIdentityAdminForbidden
	}
	return nil
}

func (r IdentityRoutes) securityContext(w http.ResponseWriter, req *http.Request) (security.Context, bool) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
		return security.Context{}, false
	}
	if err := requireOperationsTenant(securityContext); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return security.Context{}, false
	}
	return securityContext, true
}

func (r IdentityRoutes) listUsers(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.securityContext(w, req); !ok {
		return
	}
	users, err := r.admin.ListUsers(req.Context())
	if err != nil {
		writeIdentityAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

type enrollIdentityUserRequest struct {
	Username  string `json:"username"`
	Email     string `json:"email"`
	FirstName string `json:"firstName,omitempty"`
	LastName  string `json:"lastName,omitempty"`
}

// enrollIdentityUserResponse always carries IdPUser once the IdP half
// succeeded. ErunUser is nil and Error is set when the erun-side mapping
// failed after the IdP user was created — the "which half landed" report
// the enrollment flow must give rather than either silently swallowing the
// failure or claiming full success.
type enrollIdentityUserResponse struct {
	IdPUser  zitadel.User `json:"idpUser"`
	ErunUser *model.User  `json:"erunUser,omitempty"`
	Error    string       `json:"error,omitempty"`
}

func (r IdentityRoutes) createUser(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := r.securityContext(w, req)
	if !ok {
		return
	}
	var body enrollIdentityUserRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	username := strings.TrimSpace(body.Username)
	email := strings.TrimSpace(body.Email)
	if username == "" || email == "" {
		writeError(w, http.StatusBadRequest, "username and email are required")
		return
	}

	result, err := r.enroller.Enroll(req.Context(), service.EnrollIdentityParams{
		Username:  username,
		Email:     email,
		FirstName: strings.TrimSpace(body.FirstName),
		LastName:  strings.TrimSpace(body.LastName),
		Issuer:    securityContext.ExternalIssuer,
	})
	if err != nil {
		if errors.Is(err, service.ErrIdentityMappingFailed) {
			// The IdP identity is real; only the erun mapping failed. Report
			// both halves rather than a bare 500, so the operator can see the
			// orphaned IdP user id and retry the mapping (POST /v1/users with
			// that id as subject) instead of enrolling a duplicate.
			writeJSON(w, http.StatusCreated, enrollIdentityUserResponse{IdPUser: result.IdPUser, Error: err.Error()})
			return
		}
		writeIdentityAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, enrollIdentityUserResponse{IdPUser: result.IdPUser, ErunUser: &result.ErunUser})
}

func (r IdentityRoutes) deactivateUser(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.securityContext(w, req); !ok {
		return
	}
	if err := r.admin.DeactivateUser(req.Context(), req.PathValue("external_id")); err != nil {
		writeIdentityAdminError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r IdentityRoutes) reactivateUser(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.securityContext(w, req); !ok {
		return
	}
	if err := r.admin.ReactivateUser(req.Context(), req.PathValue("external_id")); err != nil {
		writeIdentityAdminError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r IdentityRoutes) getOrgSettings(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.securityContext(w, req); !ok {
		return
	}
	settings, err := r.admin.GetOrgSettings(req.Context())
	if err != nil {
		writeIdentityAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// updateOrgSettingsRequest mirrors zitadel.UpdateOrgSettingsParams field for
// field: a route-local request struct (per this module's convention for
// partial-update inputs) rather than decoding straight into the client
// package's params type, keeping the HTTP contract owned by this layer.
type updateOrgSettingsRequest struct {
	ForceMFA                  *bool   `json:"forceMfa,omitempty"`
	MinPasswordLength         *uint64 `json:"minPasswordLength,omitempty"`
	PasswordRequiresUppercase *bool   `json:"passwordRequiresUppercase,omitempty"`
	PasswordRequiresLowercase *bool   `json:"passwordRequiresLowercase,omitempty"`
	PasswordRequiresNumber    *bool   `json:"passwordRequiresNumber,omitempty"`
	PasswordRequiresSymbol    *bool   `json:"passwordRequiresSymbol,omitempty"`
}

func (r IdentityRoutes) updateOrgSettings(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.securityContext(w, req); !ok {
		return
	}
	var body updateOrgSettingsRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	settings, err := r.admin.UpdateOrgSettings(req.Context(), zitadel.UpdateOrgSettingsParams{
		ForceMFA:                  body.ForceMFA,
		MinPasswordLength:         body.MinPasswordLength,
		PasswordRequiresUppercase: body.PasswordRequiresUppercase,
		PasswordRequiresLowercase: body.PasswordRequiresLowercase,
		PasswordRequiresNumber:    body.PasswordRequiresNumber,
		PasswordRequiresSymbol:    body.PasswordRequiresSymbol,
	})
	if err != nil {
		writeIdentityAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// writeIdentityAdminError forwards a Zitadel Management API error's real
// status and message when there is one (identity-state text like "User with
// state initial can only be deleted not deactivated" is actionable for an
// operator), falling back to 502 for a transport-level failure that never
// got a Zitadel response at all.
func writeIdentityAdminError(w http.ResponseWriter, err error) {
	var apiErr *zitadel.APIError
	if errors.As(err, &apiErr) {
		status := apiErr.StatusCode
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		writeError(w, status, apiErr.Body)
		return
	}
	writeError(w, http.StatusBadGateway, "identity provider request failed")
}
