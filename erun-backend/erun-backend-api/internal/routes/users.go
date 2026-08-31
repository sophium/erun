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

// enrollUserRequest is the enrollment body. Issuer/Subject link the external
// identity the enrollee signs in with; omitting them enrolls a username with
// no external identity yet (it cannot sign in until one is linked, which this
// API does not yet support outside creation). TenantID targets another
// tenant explicitly and is honored only for an operations-scoped caller.
// RoleIDs grants those roles at enrollment instead of the zero-role default;
// omit it to enroll with no roles (the tenant's first user still gets
// ReadAll/WriteAll regardless, so a new tenant is never unusable).
type enrollUserRequest struct {
	Username string   `json:"username"`
	Issuer   string   `json:"issuer,omitempty"`
	Subject  string   `json:"subject,omitempty"`
	TenantID string   `json:"tenantId,omitempty"`
	RoleIDs  []string `json:"roleIds,omitempty"`
}

// enrollUserResponse is createUser's response body: the enrolled (or
// already-enrolled) user's model shape plus AlreadyEnrolled, which is true
// only for the no-op re-enrollment case (see UserRepository.Create's doc) —
// so a caller can tell "brand new, exactly as requested" apart from
// "already existed, here is what's actually on file" instead of inferring it
// from the HTTP status alone.
type enrollUserResponse struct {
	model.User
	AlreadyEnrolled bool `json:"alreadyEnrolled,omitempty"`
}

func RegisterUserRoutes(register ProtectedRouteRegistrar, users UserEnrollmentRepository) {
	routes := UserRoutes{users: users}
	register(http.MethodPost, "/v1/users", http.HandlerFunc(routes.createUser))
	register(http.MethodGet, "/v1/users", http.HandlerFunc(routes.listUsers))
}

var errCrossTenantUsersForbidden = errors.New("enrolling or listing users in another tenant requires an operations tenant")

// resolveTargetTenant is the operations-scoped cross-tenant precedent from
// tenants.go/tenant_quotas.go: a caller acts on their own resolved tenant by
// default, and only an operations tenant may explicitly name a different one.
func resolveTargetTenant(securityContext security.Context, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" || requested == securityContext.TenantID {
		return securityContext.TenantID, nil
	}
	if securityContext.TenantType != string(model.TenantTypeOperations) {
		return "", errCrossTenantUsersForbidden
	}
	return requested, nil
}

func (r UserRoutes) createUser(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		// Protected routes always run behind authentication middleware that stamps
		// the security context, so a missing context is an internal wiring error,
		// not a client fault.
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
	// TenantID is only passed through when it differs from the caller's own
	// session tenant; the common case relies on the tenant_id column default,
	// matching every other tenant-owned Create in this codebase.
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
	// Re-enrolling an identity already enrolled in the target tenant is a
	// no-op success (200, the existing user), not a 201 — see
	// UserRepository.Create's doc for why this is treated as satisfied
	// intent rather than a conflict.
	status := http.StatusCreated
	if alreadyEnrolled {
		status = http.StatusOK
	}
	writeJSON(w, status, enrollUserResponse{User: created, AlreadyEnrolled: alreadyEnrolled})
}

// writeCreateUserError maps Create's discriminated conflicts to their
// documented codes/messages instead of letting the route fall through to the
// generic conflict response for a cause it actually knows.
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
