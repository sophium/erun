package routes

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// TenantQuotaWriter sets a tenant's caps (env count plus the per-environment
// CPU/memory/storage namespace ceiling the quota guardrail enforces on env
// registration and provisioning).
type TenantQuotaWriter interface {
	Set(ctx context.Context, tenantID string, quota model.TenantQuota) (model.TenantQuota, error)
}

type TenantQuotaRoutes struct {
	quotas TenantQuotaWriter
}

// setTenantQuotaRequest carries every cap explicitly: a PUT always fully
// replaces the row, never merges. maxEnvironments may be 0 (a real "no
// environments" cap, matching its pre-existing validation); the three
// resource caps must be positive — 0 has no sane operational meaning for a
// namespace ResourceQuota and almost always means the caller omitted the
// field rather than intending to allow nothing.
type setTenantQuotaRequest struct {
	MaxEnvironments  int `json:"maxEnvironments"`
	MaxCPUMillicores int `json:"maxCpuMillicores"`
	MaxMemoryMB      int `json:"maxMemoryMb"`
	MaxStorageGB     int `json:"maxStorageGb"`
}

func RegisterTenantQuotaRoute(register ProtectedRouteRegistrar, quotas TenantQuotaWriter) {
	routes := TenantQuotaRoutes{quotas: quotas}
	register(http.MethodPut, "/v1/tenants/{tenant_id}/quota", http.HandlerFunc(routes.setQuota))
}

// Operations-only: setting another tenant's quota is a cross-tenant write that
// only the operations role's RLS policy permits.
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
	if err := validateSetTenantQuotaRequest(body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	quota, err := r.quotas.Set(req.Context(), tenantID, model.TenantQuota{
		MaxEnvironments:  body.MaxEnvironments,
		MaxCPUMillicores: body.MaxCPUMillicores,
		MaxMemoryMB:      body.MaxMemoryMB,
		MaxStorageGB:     body.MaxStorageGB,
	})
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, quota)
}

func validateSetTenantQuotaRequest(body setTenantQuotaRequest) error {
	switch {
	case body.MaxEnvironments < 0:
		return errors.New("maxEnvironments must be >= 0")
	case body.MaxCPUMillicores <= 0:
		return errors.New("maxCpuMillicores must be > 0: a PUT fully replaces the quota row, so it must be sent explicitly on every request")
	case body.MaxMemoryMB <= 0:
		return errors.New("maxMemoryMb must be > 0: a PUT fully replaces the quota row, so it must be sent explicitly on every request")
	case body.MaxStorageGB <= 0:
		return errors.New("maxStorageGb must be > 0: a PUT fully replaces the quota row, so it must be sent explicitly on every request")
	}
	return nil
}
