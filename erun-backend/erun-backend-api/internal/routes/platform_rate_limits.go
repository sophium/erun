package routes

import (
	"context"
	"errors"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// PlatformRateLimitWriter sets the invite-request rate-limit window.
type PlatformRateLimitWriter interface {
	SetInviteRequestWindow(ctx context.Context, windowSeconds int) (model.PlatformRateLimit, error)
}

type platformRateLimitRoutes struct {
	limits PlatformRateLimitWriter
}

// RegisterPlatformRateLimitRoute registers the operations-only, audited write of
// the invite-request window.
func RegisterPlatformRateLimitRoute(register ProtectedRouteRegistrar, limits PlatformRateLimitWriter) {
	routes := platformRateLimitRoutes{limits: limits}
	register(http.MethodPatch, "/v1/config/invite-request-rate-limit", http.HandlerFunc(routes.setInviteRequestRateLimit))
}

type setInviteRequestRateLimitBody struct {
	WindowSeconds int `json:"windowSeconds"`
}

// setInviteRequestRateLimit applies going forward; pending requests stay approvable.
func (r platformRateLimitRoutes) setInviteRequestRateLimit(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), errors.New("security context not found in request"))
		return
	}
	if securityContext.TenantType != string(model.TenantTypeOperations) {
		writeError(w, http.StatusForbidden, "changing the invite-request rate limit requires an operations tenant")
		return
	}
	var body setInviteRequestRateLimitBody
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.WindowSeconds < 1 {
		writeError(w, http.StatusBadRequest, "windowSeconds must be at least 1: the limiter cannot be disabled by setting it to zero")
		return
	}
	limit, err := r.limits.SetInviteRequestWindow(req.Context(), body.WindowSeconds)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, limit)
}
