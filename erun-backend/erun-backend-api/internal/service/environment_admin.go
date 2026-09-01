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

// EnvironmentCreator is the persistence half CreateForTenant wraps: the same
// repository.EnvironmentRepository.Create every ordinary create already uses.
type EnvironmentCreator interface {
	Create(ctx context.Context, environment model.Environment) (model.Environment, error)
}

// EnvironmentAdminAuditLogger records a cross-tenant environment create. It is
// the raw audit repository, the same narrow shape ReviewAuditLogger is above.
type EnvironmentAdminAuditLogger interface {
	LogAuditEvent(ctx context.Context, event model.AuditEvent) error
}

// EnvironmentAdminService owns the one piece of cross-tenant environment
// administration that is more than a single repository call: an
// operations caller placing a row in a tenant other than its own must leave a
// durable, attributable record — which operator, from which home tenant,
// acted on which target — since the write itself, once persisted, only ever
// names the target tenant.
type EnvironmentAdminService struct {
	environments EnvironmentCreator
	audit        EnvironmentAdminAuditLogger
}

func NewEnvironmentAdminService(environments EnvironmentCreator, audit EnvironmentAdminAuditLogger) *EnvironmentAdminService {
	return &EnvironmentAdminService{environments: environments, audit: audit}
}

// auditCrossTenantEnvironmentCreateParameters is the api_parameters payload
// shape for a cross-tenant environment create, machine-readable in the audit
// trail rather than a hand-built string — mirrors auditOverrideParameters in
// reviews.go.
type auditCrossTenantEnvironmentCreateParameters struct {
	TargetTenantID string `json:"targetTenantId"`
	Name           string `json:"name"`
	Type           string `json:"type"`
}

// CreateForTenant creates environment in the tenant scopedCtx is already
// bound to (the caller must already have proven that tenant is a legitimate
// target — see routes.scopeToRequestedTenant) and records the audit event
// first, mirroring OverrideAdvanceMergeQueue's own "audit before the
// consequential write" ordering in this same package: a missing or failing
// audit logger fails the create closed rather than silently placing an
// unattributed row in someone else's tenant. homeCtx is the caller's own,
// unscoped security context, so the audit row names the operator's real home
// tenant rather than the tenant the write is about to land in.
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
