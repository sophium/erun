package routes

import (
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

func RegisterWhoamiRoute(register ProtectedRouteRegistrar) {
	register(http.MethodGet, "/v1/whoami", http.HandlerFunc(handleWhoami))
}

type whoamiResponse struct {
	TenantID string `json:"tenantId"`
	UserID   string `json:"userId"`
	Issuer   string `json:"issuer"`
	Subject  string `json:"subject"`
}

func handleWhoami(w http.ResponseWriter, r *http.Request) {
	securityContext, err := security.RequiredFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
		return
	}

	writeJSON(w, http.StatusOK, whoamiResponse{
		TenantID: securityContext.TenantID,
		UserID:   securityContext.ErunUserID,
		Issuer:   securityContext.ExternalIssuer,
		Subject:  securityContext.ExternalUserID,
	})
}
