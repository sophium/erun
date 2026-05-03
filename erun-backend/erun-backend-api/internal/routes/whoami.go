package routes

import (
	"context"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type WhoamiUserRepository interface {
	Get(ctx context.Context, userID string) (model.User, error)
	RoleNames(ctx context.Context, userID string) ([]string, error)
}

type WhoamiRoutes struct {
	users WhoamiUserRepository
}

func RegisterWhoamiRoute(register ProtectedRouteRegistrar, users WhoamiUserRepository) {
	routes := WhoamiRoutes{users: users}
	register(http.MethodGet, "/v1/whoami", http.HandlerFunc(routes.handleWhoami))
}

type whoamiResponse struct {
	TenantID string   `json:"tenantId"`
	UserID   string   `json:"userId"`
	Username string   `json:"username,omitempty"`
	Roles    []string `json:"roles,omitempty"`
	Issuer   string   `json:"issuer"`
	Subject  string   `json:"subject"`
}

func (routes WhoamiRoutes) handleWhoami(w http.ResponseWriter, r *http.Request) {
	securityContext, err := security.RequiredFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
		return
	}

	response := whoamiResponse{
		TenantID: securityContext.TenantID,
		UserID:   securityContext.ErunUserID,
		Issuer:   securityContext.ExternalIssuer,
		Subject:  securityContext.ExternalUserID,
	}
	if routes.users != nil {
		user, err := routes.users.Get(r.Context(), securityContext.ErunUserID)
		if err != nil {
			writeRepositoryError(w, err)
			return
		}
		response.Username = user.Username
		roles, err := routes.users.RoleNames(r.Context(), securityContext.ErunUserID)
		if err != nil {
			writeRepositoryError(w, err)
			return
		}
		response.Roles = roles
	}

	writeJSON(w, http.StatusOK, response)
}
