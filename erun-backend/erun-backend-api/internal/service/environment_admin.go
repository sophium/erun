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

// EnvironmentCreator is the persistence half CreateForTenant wraps.
type EnvironmentCreator interface {
	Create(ctx context.Context, environment model.Environment) (model.Environment, error)
}

// EnvironmentAdminAuditLogger records a cross-tenant environment create.
type EnvironmentAdminAuditLogger interface {
	LogAuditEvent(ctx context.Context, event model.AuditEvent) error
}

// EnvironmentAdminService makes cross-tenant environment creates attributable, since the row names only the target tenant.
type EnvironmentAdminService struct {
	environments EnvironmentCreator
	audit        EnvironmentAdminAuditLogger
}

func NewEnvironmentAdminService(environments EnvironmentCreator, audit EnvironmentAdminAuditLogger) *EnvironmentAdminService {
	return &EnvironmentAdminService{environments: environments, audit: audit}
}

type auditCrossTenantEnvironmentCreateParameters struct {
	TargetTenantID string `json:"targetTenantId"`
	Name           string `json:"name"`
	Type           string `json:"type"`
}

// CreateForTenant audits before writing and fails closed without an audit logger. scopedCtx is bound to the
// already-validated target tenant; homeCtx is the caller's own, so the audit row names the operator's home tenant.
func (s *EnvironmentAdminService) CreateForTenant(scopedCtx, homeCtx context.Context, targetTenantID string, environment model.Environment) (model.Environment, error) {
	if s.audit == nil {
		return model.Environment{}, errors.New("creating an environment in another tenant requires audit logging, which is not configured on this control plane")
	}
	if err := s.auditCrossTenantCreate(homeCtx, targetTenantID, environment); err != nil {
		return model.Environment{}, err
	}
	return s.environments.Create(scopedCtx, environment)
}

func (s *EnvironmentAdminService) auditCrossTenantCreate(homeCtx context.Context, targetTenantID string, environment model.Environment) error {
	securityContext, err := security.RequiredFromContext(homeCtx)
	if err != nil {
		return repository.ErrMissingSecurityContext
	}
	parameters, err := json.Marshal(auditCrossTenantEnvironmentCreateParameters{
		TargetTenantID: targetTenantID,
		Name:           environment.Name,
		Type:           string(environment.Type),
	})
	if err != nil {
		return err
	}
	return s.audit.LogAuditEvent(homeCtx, model.AuditEvent{
		TenantID:         securityContext.TenantID,
		ErunUserID:       securityContext.ErunUserID,
		ExternalUserID:   securityContext.ExternalUserID,
		ExternalIssuerID: securityContext.ExternalIssuer,
		ExternalOrgID:    securityContext.ExternalOrgID,
		Type:             model.AuditEventTypeAPI,
		APIMethod:        http.MethodPost,
		APIPath:          "/v1/environments",
		APIParameters:    string(parameters),
	})
}
