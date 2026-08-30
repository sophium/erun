package routes

import (
	"context"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// PlatformRateLimitWriter sets the platform's invite-request rate-limit
// window. *repository.PlatformRateLimitRepository satisfies it.
type PlatformRateLimitWriter interface {
	SetInviteRequestWindow(ctx context.Context, windowSeconds int) (model.PlatformRateLimit, error)
}

type platformRateLimitRoutes struct {
	limits PlatformRateLimitWriter
}

// RegisterPlatformRateLimitRoute registers the first config write route
// (issue §9: "GET /v1/config exists as the console's read model ... and
// nothing writes it — so this needs the first config write route"). GET
// /v1/config already reports the current window (see config.go); this is
// what changes it. Operations-only, and audited like every other protected
// write — the acting identity is exactly what audit_events exists to record
// for a security-relevant change to the only publicly-reachable write's
// gate.
func RegisterPlatformRateLimitRoute(register ProtectedRouteRegistrar, limits PlatformRateLimitWriter) {
	routes := platformRateLimitRoutes{limits: limits}
	register(http.MethodPatch, "/v1/config/invite-request-rate-limit", http.HandlerFunc(routes.setInviteRequestRateLimit))
}

type setInviteRequestRateLimitBody struct {
	WindowSeconds int `json:"windowSeconds"`
}

// setInviteRequestRateLimit changes the window going forward only — requests
// already pending stay queued and approvable regardless of the new window
// (issue §9: "raising it does not destroy pending work").
func (r platformRateLimitRoutes) setInviteRequestRateLimit(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
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
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, limit)
}
