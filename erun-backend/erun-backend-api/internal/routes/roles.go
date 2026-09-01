package routes

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// RoleRepository is the persistence dependency for role management and
// role-assignment routes.
type RoleRepository interface {
	List(ctx context.Context) ([]model.Role, error)
	Create(ctx context.Context, name string, permissions []apirepository.RolePermissionInput) (model.Role, error)
	ForUser(ctx context.Context, userID string) ([]model.Role, error)
	Grant(ctx context.Context, userID string, roleID string, tenantID string) (model.UserRole, error)
	Revoke(ctx context.Context, userID string, roleID string) error
}

type RoleRoutes struct {
	roles RoleRepository
}

func RegisterRoleRoutes(register ProtectedRouteRegistrar, roles RoleRepository) {
	routes := RoleRoutes{roles: roles}
	register(http.MethodGet, "/v1/roles", http.HandlerFunc(routes.listRoles))
	register(http.MethodPost, "/v1/roles", http.HandlerFunc(routes.createRole))
	register(http.MethodGet, "/v1/users/{user_id}/roles", http.HandlerFunc(routes.listUserRoles))
	register(http.MethodPost, "/v1/users/{user_id}/roles", http.HandlerFunc(routes.grantUserRole))
	register(http.MethodDelete, "/v1/users/{user_id}/roles/{role_id}", http.HandlerFunc(routes.revokeUserRole))
}

type createRolePermissionRequest struct {
	APIMethod        string `json:"apiMethod,omitempty"`
	APIPath          string `json:"apiPath,omitempty"`
	APIMethodPattern string `json:"apiMethodPattern,omitempty"`
	APIPathPattern   string `json:"apiPathPattern,omitempty"`
}

type createRoleRequest struct {
	Name        string                        `json:"name"`
	Permissions []createRolePermissionRequest `json:"permissions"`
}

func (routes RoleRoutes) listRoles(w http.ResponseWriter, req *http.Request) {
	roles, err := routes.roles.List(req.Context())
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, roles)
}

func (routes RoleRoutes) createRole(w http.ResponseWriter, req *http.Request) {
	var body createRoleRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(body.Permissions) == 0 {
		writeError(w, http.StatusBadRequest, "at least one permission is required")
		return
	}
	permissions := make([]apirepository.RolePermissionInput, 0, len(body.Permissions))
	for _, permission := range body.Permissions {
		permissions = append(permissions, apirepository.RolePermissionInput{
			APIMethod:        strings.TrimSpace(permission.APIMethod),
			APIPath:          strings.TrimSpace(permission.APIPath),
			APIMethodPattern: strings.TrimSpace(permission.APIMethodPattern),
			APIPathPattern:   strings.TrimSpace(permission.APIPathPattern),
		})
	}

	role, err := routes.roles.Create(req.Context(), name, permissions)
	if err != nil {
		if errors.Is(err, apirepository.ErrInvalidInput) {
			writeError(w, http.StatusBadRequest, "each permission needs exactly one of an exact method/path pair or a method-pattern/path-pattern pair, with a valid HTTP method and, for patterns, a compilable regular expression")
			return
		}
		if errors.Is(err, apirepository.ErrConflict) {
			writeError(w, http.StatusConflict, "a role with this name, or a permission on it, already exists in this tenant")
			return
		}
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, role)
}

func (routes RoleRoutes) listUserRoles(w http.ResponseWriter, req *http.Request) {
	roles, err := routes.roles.ForUser(req.Context(), req.PathValue("user_id"))
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, roles)
}

type grantUserRoleRequest struct {
	RoleID string `json:"roleId"`
	// TenantID targets another tenant and is honored only for an
	// operations-tenant caller, the same rule POST /v1/users applies. It is
	// what recovers a tenant whose only grant-capable user cannot
	// authenticate: role management is role-gated, so without this the
	// identity that can sign in could never be given the role it needs.
	TenantID string `json:"tenantId"`
}

func (routes RoleRoutes) grantUserRole(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
		return
	}
	var body grantUserRoleRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	roleID := strings.TrimSpace(body.RoleID)
	if roleID == "" {
		writeError(w, http.StatusBadRequest, "roleId is required")
		return
	}
	targetTenantID, err := resolveTargetTenant(securityContext, body.TenantID)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	// Passed through only when it differs from the caller's own session
	// tenant; the common case relies on the tenant_id column default.
	overrideTenantID := ""
	if targetTenantID != securityContext.TenantID {
		overrideTenantID = targetTenantID
	}

	grant, err := routes.roles.Grant(req.Context(), req.PathValue("user_id"), roleID, overrideTenantID)
	if err != nil {
		if errors.Is(err, apirepository.ErrConflict) {
			writeError(w, http.StatusConflict, "the user already holds this role")
			return
		}
		if errors.Is(err, apirepository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "the user or role does not exist in this tenant")
			return
		}
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, grant)
}

func (routes RoleRoutes) revokeUserRole(w http.ResponseWriter, req *http.Request) {
	err := routes.roles.Revoke(req.Context(), req.PathValue("user_id"), req.PathValue("role_id"))
	if err != nil {
		if errors.Is(err, apirepository.ErrLastGrantCapableRole) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeRepositoryError(w, req, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
