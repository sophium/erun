package routes

import (
	"context"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// TenantQuotaWriter sets a tenant's environment-count cap (the per-tenant
// override the quota guardrail enforces on env registration).
type TenantQuotaWriter interface {
	Set(ctx context.Context, tenantID string, maxEnvironments int) (model.TenantQuota, error)
}

type TenantQuotaRoutes struct {
	quotas TenantQuotaWriter
}

type setTenantQuotaRequest struct {
	MaxEnvironments int `json:"maxEnvironments"`
}

func RegisterTenantQuotaRoute(register ProtectedRouteRegistrar, quotas TenantQuotaWriter) {
	routes := TenantQuotaRoutes{quotas: quotas}
	register(http.MethodPut, "/v1/tenants/{tenant_id}/quota", http.HandlerFunc(routes.setQuota))
}

// setQuota sets a tenant's environment-count cap. Operations-only: like tenant
// registration, the caller must be an OPERATIONS tenant, because it writes
// another tenant's quota row (the operations role's RLS policy permits it).
func (r TenantQuotaRoutes) setQuota(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing security context")
		return
	}
	if securityContext.TenantType != string(model.TenantTypeOperations) {
		writeError(w, http.StatusForbidden, "setting a tenant quota requires an operations tenant")
		return
	}
	tenantID := strings.TrimSpace(req.PathValue("tenant_id"))
	if tenantID == "" {
		writeError(w, http.StatusBadRequest, "tenant_id is required")
		return
	}
	var body setTenantQuotaRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.MaxEnvironments < 0 {
		writeError(w, http.StatusBadRequest, "maxEnvironments must be >= 0")
		return
	}
	quota, err := r.quotas.Set(req.Context(), tenantID, body.MaxEnvironments)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, quota)
}
