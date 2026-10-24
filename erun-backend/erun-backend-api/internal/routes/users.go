package routes

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// UserEnrollmentRepository is the persistence dependency for POST/GET /v1/users.
type UserEnrollmentRepository interface {
	Create(ctx context.Context, params repository.CreateUserParams) (model.User, bool, error)
	List(ctx context.Context, filter repository.UserFilter) ([]model.User, error)
}

type UserRoutes struct {
	users UserEnrollmentRepository
}

// enrollUserRequest: omitting Issuer/Subject enrolls a username that cannot sign in yet; TenantID is honored
// only for an operations-scoped caller; a tenant's first user gets ReadAll/WriteAll regardless of RoleIDs.
type enrollUserRequest struct {
	Username string   `json:"username"`
	Issuer   string   `json:"issuer,omitempty"`
	Subject  string   `json:"subject,omitempty"`
	TenantID string   `json:"tenantId,omitempty"`
	RoleIDs  []string `json:"roleIds,omitempty"`
}

// enrollUserResponse.AlreadyEnrolled is true only for a no-op re-enrollment.
type enrollUserResponse struct {
	model.User
	AlreadyEnrolled bool `json:"alreadyEnrolled,omitempty"`
}

func RegisterUserRoutes(register ProtectedRouteRegistrar, users UserEnrollmentRepository) {
	routes := UserRoutes{users: users}
	register(http.MethodPost, "/v1/users", http.HandlerFunc(routes.createUser))
	register(http.MethodGet, "/v1/users", http.HandlerFunc(routes.listUsers))
}

var errCrossTenantScopeForbidden = errors.New("targeting another tenant requires an operations tenant")

// resolveTargetTenant defaults to the caller's tenant; only an operations tenant may name another.
func resolveTargetTenant(securityContext security.Context, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" || requested == securityContext.TenantID {
		return securityContext.TenantID, nil
	}
	if securityContext.TenantType != string(model.TenantTypeOperations) {
		return "", errCrossTenantScopeForbidden
	}
	return requested, nil
}

func (r UserRoutes) createUser(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), errors.New("security context not found in request"))
		return
	}

	var body enrollUserRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	username := strings.TrimSpace(body.Username)
	if username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}

	targetTenantID, err := resolveTargetTenant(securityContext, body.TenantID)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	// Empty for the caller's own tenant, which the tenant_id column default covers.
	overrideTenantID := ""
	if targetTenantID != securityContext.TenantID {
		overrideTenantID = targetTenantID
	}

	created, alreadyEnrolled, err := r.users.Create(req.Context(), repository.CreateUserParams{
		Username: username,
		Issuer:   strings.TrimSpace(body.Issuer),
		Subject:  strings.TrimSpace(body.Subject),
		TenantID: overrideTenantID,
		RoleIDs:  body.RoleIDs,
	})
	if err != nil {
		writeCreateUserError(w, req, err)
		return
	}
	// Re-enrolling an already-enrolled identity is a 200 no-op, not a conflict.
	status := http.StatusCreated
	if alreadyEnrolled {
		status = http.StatusOK
	}
	writeJSON(w, status, enrollUserResponse{User: created, AlreadyEnrolled: alreadyEnrolled})
}

func writeCreateUserError(w http.ResponseWriter, req *http.Request, err error) {
	switch {
	case errors.Is(err, repository.ErrUsernameConflict):
		writeErrorCode(w, http.StatusConflict, "USERNAME_TAKEN", "a user with this username already exists in the target tenant")
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, "one or more requested roles do not exist in this tenant")
	default:
		writeRepositoryError(w, req, err)
	}
}

func (r UserRoutes) listUsers(w http.ResponseWriter, req *http.Request) {
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

	users, err := r.users.List(req.Context(), repository.UserFilter{TenantID: targetTenantID})
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}
