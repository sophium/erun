package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

type stubConfigTenantRepository struct {
	tenant model.Tenant
	err    error
}

func (r stubConfigTenantRepository) Current(context.Context) (model.Tenant, error) {
	return r.tenant, r.err
}

type stubEnvironmentRepository struct {
	environments []model.Environment
	environment  model.Environment
	created      model.Environment
	createCalls  int
	createInput  model.Environment
	count        int
	countErr     error
	// countByContext maps a contextID to the environment count
	// CountByContext reports for it; an unlisted id reports 0.
	countByContext    map[string]int
	countByContextErr error
	// countByRuntimeType is what CountByType reports for
	// model.EnvironmentTypeRuntime (#1113's aggregate resource-budget check).
	countByRuntimeType int
	countByTypeErr     error
	err                error
	// claimTaken makes ClaimDeploy report the deploy slot as already held, the
	// shape a concurrent deploy produces.
	claimTaken       bool
	claimErr         error
	claimCalls       int
	markFailedCalls  int
	markFailedReason string
}

func (r *stubEnvironmentRepository) ClaimDeploy(context.Context, string, time.Duration) (bool, error) {
	r.claimCalls++
	if r.claimErr != nil {
		return false, r.claimErr
	}
	return !r.claimTaken, nil
}

func (r *stubEnvironmentRepository) MarkDeployFailed(_ context.Context, _ string, reason string) error {
	r.markFailedCalls++
	r.markFailedReason = reason
	return nil
}

func (r *stubEnvironmentRepository) List(context.Context) ([]model.Environment, error) {
	return r.environments, r.err
}

func (r *stubEnvironmentRepository) Count(context.Context) (int, error) {
	return r.count, r.countErr
}

func (r *stubEnvironmentRepository) CountByContext(_ context.Context, contextID string) (int, error) {
	if r.countByContextErr != nil {
		return 0, r.countByContextErr
	}
	return r.countByContext[contextID], nil
}

// CountByType reports countByRuntimeType for model.EnvironmentTypeRuntime and
// 0 otherwise; the aggregate resource-budget check (#1113) is the only
// caller, and it only ever asks about runtime environments.
func (r *stubEnvironmentRepository) CountByType(_ context.Context, envType model.EnvironmentType) (int, error) {
	if r.countByTypeErr != nil {
		return 0, r.countByTypeErr
	}
	if envType == model.EnvironmentTypeRuntime {
		return r.countByRuntimeType, nil
	}
	return 0, nil
}

// stubTenantQuotaRepository reports a fixed quota row for the quota guardrail.
// Resource caps left at their zero value default to the same values an absent
// tenant_quotas row would (repository.DefaultMax*), so existing test
// constructions that only set maxEnvironments keep passing the resource-cap
// floor check added for #605.
type stubTenantQuotaRepository struct {
	maxEnvironments       int
	maxCPUMillicores      int
	maxMemoryMB           int
	maxStorageGB          int
	maxTotalCPUMillicores int
	maxTotalMemoryMB      int
	maxTotalStorageGB     int
	err                   error
}

func (r stubTenantQuotaRepository) Get(context.Context) (model.TenantQuota, error) {
	if r.err != nil {
		return model.TenantQuota{}, r.err
	}
	quota := model.TenantQuota{
		MaxEnvironments:       r.maxEnvironments,
		MaxCPUMillicores:      r.maxCPUMillicores,
		MaxMemoryMB:           r.maxMemoryMB,
		MaxStorageGB:          r.maxStorageGB,
		MaxTotalCPUMillicores: r.maxTotalCPUMillicores,
		MaxTotalMemoryMB:      r.maxTotalMemoryMB,
		MaxTotalStorageGB:     r.maxTotalStorageGB,
	}
	if quota.MaxCPUMillicores == 0 {
		quota.MaxCPUMillicores = repository.DefaultMaxCPUMillicores
	}
	if quota.MaxMemoryMB == 0 {
		quota.MaxMemoryMB = repository.DefaultMaxMemoryMB
	}
	if quota.MaxStorageGB == 0 {
		quota.MaxStorageGB = repository.DefaultMaxStorageGB
	}
	if quota.MaxTotalCPUMillicores == 0 {
		quota.MaxTotalCPUMillicores = repository.DefaultMaxTotalCPUMillicores
	}
	if quota.MaxTotalMemoryMB == 0 {
		quota.MaxTotalMemoryMB = repository.DefaultMaxTotalMemoryMB
	}
	if quota.MaxTotalStorageGB == 0 {
		quota.MaxTotalStorageGB = repository.DefaultMaxTotalStorageGB
	}
	return quota, nil
}

func (r *stubEnvironmentRepository) Get(context.Context, string) (model.Environment, error) {
	return r.environment, r.err
}

func (r *stubEnvironmentRepository) Create(_ context.Context, environment model.Environment) (model.Environment, error) {
	r.createCalls++
	r.createInput = environment
	if r.err != nil {
		return model.Environment{}, r.err
	}
	created := r.created
	if created.EnvironmentID == "" {
		created = environment
		created.EnvironmentID = "env-created"
	}
	return created, nil
}

type stubContextRepository struct {
	contexts     []model.Context
	cloudContext model.Context
	created      model.Context
	createCalls  int
	createInput  model.Context
	err          error
}

func (r *stubContextRepository) List(context.Context) ([]model.Context, error) {
	return r.contexts, r.err
}

func (r *stubContextRepository) Get(context.Context, string) (model.Context, error) {
	return r.cloudContext, r.err
}

func (r *stubContextRepository) Create(_ context.Context, cloudContext model.Context) (model.Context, error) {
	r.createCalls++
	r.createInput = cloudContext
	if r.err != nil {
		return model.Context{}, r.err
	}
	created := r.created
	if created.ContextID == "" {
		created = cloudContext
		created.ContextID = "ctx-created"
	}
	return created, nil
}

func TestConfigReturnsDenormalizedReadModel(t *testing.T) {
	tenants := stubConfigTenantRepository{tenant: model.Tenant{TenantID: "tenant-1", Name: "acme", Type: model.TenantTypeCompany}}
	environments := &stubEnvironmentRepository{environments: []model.Environment{
		{EnvironmentID: "env-1", Name: "runtime", Type: model.EnvironmentTypeRuntime, ContextID: "ctx-1"},
	}}
	contexts := &stubContextRepository{contexts: []model.Context{
		{ContextID: "ctx-1", Name: "primary", Provider: "aws"},
	}}

	req := httptest.NewRequest(http.MethodGet, "/v1/config", nil)
	rec := httptest.NewRecorder()

	ConfigRoutes{tenants: tenants, environments: environments, contexts: contexts}.getConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var response configResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Tenant.Name != "acme" {
		t.Fatalf("unexpected tenant name: %q", response.Tenant.Name)
	}
	if len(response.Environments) != 1 || response.Environments[0].ContextID != "ctx-1" {
		t.Fatalf("unexpected environments: %+v", response.Environments)
	}
	if len(response.Contexts) != 1 || response.Contexts[0].Provider != "aws" {
		t.Fatalf("unexpected contexts: %+v", response.Contexts)
	}
}

func TestConfigPropagatesRepositoryError(t *testing.T) {
	tenants := stubConfigTenantRepository{err: repository.ErrMissingSecurityContext}

	req := httptest.NewRequest(http.MethodGet, "/v1/config", nil)
	rec := httptest.NewRecorder()

	ConfigRoutes{tenants: tenants, environments: &stubEnvironmentRepository{}, contexts: &stubContextRepository{}}.getConfig(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
}

func TestGetEnvironmentNotFound(t *testing.T) {
	environments := &stubEnvironmentRepository{err: repository.ErrNotFound}

	req := httptest.NewRequest(http.MethodGet, "/v1/environments/missing", nil)
	req.SetPathValue("environment_id", "missing")
	rec := httptest.NewRecorder()

	EnvironmentRoutes{environments: environments}.getEnvironment(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
}

func TestGetContextNotFound(t *testing.T) {
	contexts := &stubContextRepository{err: repository.ErrNotFound}

	req := httptest.NewRequest(http.MethodGet, "/v1/contexts/missing", nil)
	req.SetPathValue("context_id", "missing")
	rec := httptest.NewRecorder()

	ContextRoutes{contexts: contexts}.getContext(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
}
