package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// ErrBootstrapNameNotConfigured is returned when the platform declares no ERUN_TENANT.
var ErrBootstrapNameNotConfigured = errors.New("platform declares no ERUN_TENANT to reconcile the tenant name against")

// ErrBootstrapNameHasEnvironments is returned when renaming would break existing
// <tenant>-<env> namespaces.
var ErrBootstrapNameHasEnvironments = errors.New("tenant has existing environments")

// ErrBootstrapNameConflict is returned when another tenant holds the declared name.
var ErrBootstrapNameConflict = errors.New("another tenant already has the declared platform tenant name")

type TenantNameUpdater interface {
	UpdateName(ctx context.Context, tenantID, name string) (model.Tenant, error)
}

type TenantEnvironmentLister interface {
	List(ctx context.Context) ([]model.Environment, error)
}

// TenantService renames the OPERATIONS tenant to its declared ERUN_TENANT. It is
// not a general rename API: the name is never caller-supplied and any
// environment row refuses the rename.
type TenantService struct {
	tenants        TenantNameUpdater
	environments   TenantEnvironmentLister
	platformTenant string
}

// NewTenantService wires the reconcile workflow; an empty platformTenant disables it.
func NewTenantService(tenants TenantNameUpdater, environments TenantEnvironmentLister, platformTenant string) *TenantService {
	return &TenantService{tenants: tenants, environments: environments, platformTenant: strings.TrimSpace(platformTenant)}
}

// ReconcileBootstrapName renames the caller's own already-gated OPERATIONS tenant;
// an already matching name is a no-op.
func (s *TenantService) ReconcileBootstrapName(ctx context.Context, tenant model.Tenant) (model.Tenant, error) {
	if s.platformTenant == "" {
		return model.Tenant{}, ErrBootstrapNameNotConfigured
	}
	if tenant.Name == s.platformTenant {
		return tenant, nil
	}
	environments, err := s.environments.List(ctx)
	if err != nil {
		return model.Tenant{}, err
	}
	if len(environments) > 0 {
		return model.Tenant{}, fmt.Errorf("%w: cannot rename tenant %q to %q: it has %d existing environment(s), which would break their <tenant>-<env> namespace invariant", ErrBootstrapNameHasEnvironments, tenant.Name, s.platformTenant, len(environments))
	}
	updated, err := s.tenants.UpdateName(ctx, tenant.TenantID, s.platformTenant)
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return model.Tenant{}, fmt.Errorf("%w: %q is already taken by another tenant", ErrBootstrapNameConflict, s.platformTenant)
		}
		return model.Tenant{}, err
	}
	return updated, nil
}
