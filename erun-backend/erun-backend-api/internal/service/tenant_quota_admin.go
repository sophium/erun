package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type TenantQuotaSetter interface {
	Set(ctx context.Context, tenantID string, quota model.TenantQuota) (model.TenantQuota, error)
}

// TenantQuotaAdminAuditLogger records a tenant quota set.
type TenantQuotaAdminAuditLogger interface {
	LogAuditEvent(ctx context.Context, event model.AuditEvent) error
}

// TenantQuotaAdminService sets tenant quotas, an always cross-tenant,
// operations-only write that must leave an attributable audit record.
type TenantQuotaAdminService struct {
	quotas TenantQuotaSetter
	audit  TenantQuotaAdminAuditLogger
}

func NewTenantQuotaAdminService(quotas TenantQuotaSetter, audit TenantQuotaAdminAuditLogger) *TenantQuotaAdminService {
	return &TenantQuotaAdminService{quotas: quotas, audit: audit}
}

type auditSetTenantQuotaParameters struct {
	TargetTenantID        string `json:"targetTenantId"`
	MaxEnvironments       int    `json:"maxEnvironments"`
	MaxCPUMillicores      int    `json:"maxCpuMillicores"`
	MaxMemoryMB           int    `json:"maxMemoryMb"`
	MaxStorageGB          int    `json:"maxStorageGb"`
	MaxTotalCPUMillicores int    `json:"maxTotalCpuMillicores"`
	MaxTotalMemoryMB      int    `json:"maxTotalMemoryMb"`
	MaxTotalStorageGB     int    `json:"maxTotalStorageGb"`
}

// SetForTenant audits before writing, failing closed without an audit logger.
func (s *TenantQuotaAdminService) SetForTenant(ctx context.Context, targetTenantID string, quota model.TenantQuota) (model.TenantQuota, error) {
	if s.audit == nil {
		return model.TenantQuota{}, errors.New("setting a tenant's quota requires audit logging, which is not configured on this control plane")
	}
	if err := s.auditSetTenantQuota(ctx, targetTenantID, quota); err != nil {
		return model.TenantQuota{}, err
	}
	return s.quotas.Set(ctx, targetTenantID, quota)
}

func (s *TenantQuotaAdminService) auditSetTenantQuota(ctx context.Context, targetTenantID string, quota model.TenantQuota) error {
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		return repository.ErrMissingSecurityContext
	}
	parameters, err := json.Marshal(auditSetTenantQuotaParameters{
		TargetTenantID:        targetTenantID,
		MaxEnvironments:       quota.MaxEnvironments,
		MaxCPUMillicores:      quota.MaxCPUMillicores,
		MaxMemoryMB:           quota.MaxMemoryMB,
		MaxStorageGB:          quota.MaxStorageGB,
		MaxTotalCPUMillicores: quota.MaxTotalCPUMillicores,
		MaxTotalMemoryMB:      quota.MaxTotalMemoryMB,
		MaxTotalStorageGB:     quota.MaxTotalStorageGB,
	})
	if err != nil {
		return err
	}
	return s.audit.LogAuditEvent(ctx, model.AuditEvent{
		TenantID:         securityContext.TenantID,
		ErunUserID:       securityContext.ErunUserID,
		ExternalUserID:   securityContext.ExternalUserID,
		ExternalIssuerID: securityContext.ExternalIssuer,
		ExternalOrgID:    securityContext.ExternalOrgID,
		Type:             model.AuditEventTypeAPI,
		APIMethod:        http.MethodPut,
		APIPath:          "/v1/tenants/{tenant_id}/quota",
		APIParameters:    string(parameters),
	})
}
