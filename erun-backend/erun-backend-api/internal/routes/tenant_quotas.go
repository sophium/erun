package routes

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// TenantQuotaAdminSetter sets a tenant's caps (env count plus the
// per-environment CPU/memory/storage namespace ceiling the quota guardrail
// enforces on env registration and provisioning) and audits who set them.
// Setting a tenant quota is operations-only with no "act on your own tenant"
// default, so it is always a cross-tenant write — see
// service.TenantQuotaAdminService.
type TenantQuotaAdminSetter interface {
	SetForTenant(ctx context.Context, targetTenantID string, quota model.TenantQuota) (model.TenantQuota, error)
}

// TenantQuotaReader reads a tenant's quota row (RLS-scoped for a tenant-scoped
// caller; scopedContextForTenant-substitutable for an operations caller
// naming another tenant), defaulted when the tenant has no row yet — the same
// read admission itself uses. Tenant-self-service by default, unlike
// TenantQuotaAdminSetter's SetForTenant: a quota nobody can see is a support
// ticket (#605, #1113).
type TenantQuotaReader interface {
	Get(ctx context.Context) (model.TenantQuota, error)
}

type TenantQuotaRoutes struct {
	admin  TenantQuotaAdminSetter
	reader TenantQuotaReader
}

// setTenantQuotaRequest carries every cap explicitly: a PUT always fully
// replaces the row, never merges. maxEnvironments may be 0 (a real "no
// environments" cap, matching its pre-existing validation); the six resource
// caps must be positive — 0 has no sane operational meaning for a namespace
// ResourceQuota or a tenant-wide budget and almost always means the caller
// omitted the field rather than intending to allow nothing. maxTotal* is the
// aggregate tenant-wide ceiling (#1113), distinct from the per-environment
// ceiling above.
type setTenantQuotaRequest struct {
	MaxEnvironments       int `json:"maxEnvironments"`
	MaxCPUMillicores      int `json:"maxCpuMillicores"`
	MaxMemoryMB           int `json:"maxMemoryMb"`
	MaxStorageGB          int `json:"maxStorageGb"`
	MaxTotalCPUMillicores int `json:"maxTotalCpuMillicores"`
	MaxTotalMemoryMB      int `json:"maxTotalMemoryMb"`
	MaxTotalStorageGB     int `json:"maxTotalStorageGb"`
}

// RegisterTenantQuotaRoute registers both the operations-only write
// (PUT .../quota) and the read (GET /v1/quota, defaulting to the caller's own
// tenant, cross-tenant for an operations caller through the same
// resolveTargetTenant convention environments/users/invites use) — admin and
// reader are separate interfaces because they carry different authorization,
// but both are usually backed by the same repository.TenantQuotaRepository.
func RegisterTenantQuotaRoute(register ProtectedRouteRegistrar, admin TenantQuotaAdminSetter, reader TenantQuotaReader) {
	routes := TenantQuotaRoutes{admin: admin, reader: reader}
	register(http.MethodPut, "/v1/tenants/{tenant_id}/quota", http.HandlerFunc(routes.setQuota))
	register(http.MethodGet, "/v1/quota", http.HandlerFunc(routes.getQuota))
}

// getQuota returns the caller's own tenant's quota row by default — every cap
// admission itself reads, so an Operator can see exactly what they are
// working within (env count, the per-environment resource ceiling, and the
// aggregate tenant-wide budget) without an operations-scoped token. An
// operations caller may pass ?tenantId= to read another tenant's row instead —
// the read half of the same operations-only write below, so a quota can be
// seen before it is set.
func (r TenantQuotaRoutes) getQuota(w http.ResponseWriter, req *http.Request) {
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
	quota, err := r.reader.Get(scopedContextForTenant(req.Context(), securityContext, targetTenantID))
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, quota)
}

// Operations-only: setting another tenant's quota is a cross-tenant write that
// only the operations role's RLS policy permits.
func (r TenantQuotaRoutes) setQuota(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeInternalError(w, req, "missing security context", errors.New("security context not found in request"))
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
	quota, err := r.admin.SetForTenant(req.Context(), tenantID, model.TenantQuota{
		MaxEnvironments:       body.MaxEnvironments,
		MaxCPUMillicores:      body.MaxCPUMillicores,
		MaxMemoryMB:           body.MaxMemoryMB,
		MaxStorageGB:          body.MaxStorageGB,
		MaxTotalCPUMillicores: body.MaxTotalCPUMillicores,
		MaxTotalMemoryMB:      body.MaxTotalMemoryMB,
		MaxTotalStorageGB:     body.MaxTotalStorageGB,
	})
	if err != nil {
		writeRepositoryError(w, req, err)
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
	case body.MaxTotalCPUMillicores <= 0:
		return errors.New("maxTotalCpuMillicores must be > 0: a PUT fully replaces the quota row, so it must be sent explicitly on every request")
	case body.MaxTotalMemoryMB <= 0:
		return errors.New("maxTotalMemoryMb must be > 0: a PUT fully replaces the quota row, so it must be sent explicitly on every request")
	case body.MaxTotalStorageGB <= 0:
		return errors.New("maxTotalStorageGb must be > 0: a PUT fully replaces the quota row, so it must be sent explicitly on every request")
	}
	return nil
}
