package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// ErrBootstrapNameNotConfigured is returned when the platform declares no
// ERUN_TENANT: there is no declared name to reconcile against, so the action
// has nothing to do rather than guessing one.
var ErrBootstrapNameNotConfigured = errors.New("platform declares no ERUN_TENANT to reconcile the tenant name against")

// ErrBootstrapNameHasEnvironments is returned when the caller's tenant already
// has environments: renaming it would break the <tenant>-<env> namespace
// invariant every existing environment's namespace and runtime release name
// already depend on.
var ErrBootstrapNameHasEnvironments = errors.New("tenant has existing environments")

// ErrBootstrapNameConflict is returned when another tenant already holds the
// declared ERUN_TENANT name — the rename cannot proceed until that name is
// freed, since tenants.name is globally unique.
var ErrBootstrapNameConflict = errors.New("another tenant already has the declared platform tenant name")

// TenantNameUpdater is the narrow persistence dependency ReconcileBootstrapName
// needs: renaming one tenant by ID. Satisfied by *repository.TenantRepository.
type TenantNameUpdater interface {
	UpdateName(ctx context.Context, tenantID, name string) (model.Tenant, error)
}

// TenantEnvironmentLister reports the caller's tenant's environments, scoped
// by the caller's own RLS security context. Satisfied by
// *repository.EnvironmentRepository.
type TenantEnvironmentLister interface {
	List(ctx context.Context) ([]model.Environment, error)
}

// TenantService owns the reconcile-bootstrap-name workflow: a
// one-way, operations-only rename of the platform's own OPERATIONS tenant to
// match its declared ERUN_TENANT. This is deliberately not a general
// tenant-rename API — renaming a tenant that already has environments would
// break the <tenant>-<env> namespace invariant those environments depend on
// — so the target name is never caller-supplied (it is this instance's own
// config) and the rename refuses outright when the tenant has any
// environment row at all.
type TenantService struct {
	tenants        TenantNameUpdater
	environments   TenantEnvironmentLister
	platformTenant string
}

// NewTenantService wires the reconcile workflow. platformTenant is this
// instance's own declared tenant identity (ERUN_TENANT); empty disables the
// action entirely (ErrBootstrapNameNotConfigured) rather than reconciling
// against a guessed name.
func NewTenantService(tenants TenantNameUpdater, environments TenantEnvironmentLister, platformTenant string) *TenantService {
	return &TenantService{tenants: tenants, environments: environments, platformTenant: strings.TrimSpace(platformTenant)}
}

// ReconcileBootstrapName renames the caller's own tenant to this instance's
// declared ERUN_TENANT value. tenant must be the caller's own resolved tenant
// (routes.TenantRoutes reads it from TenantRepository.Current after the route
// has already gated the caller to an OPERATIONS tenant) — this method never
// accepts a caller-supplied tenant ID or target name. Already matching is a
// no-op success, not a refusal.
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
