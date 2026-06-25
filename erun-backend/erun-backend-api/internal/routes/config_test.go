package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
	err          error
	// deployStatuses records, in order, the deploy_status values passed to
	// UpdateDeployResult; deployUpdateErr fails those writes when set.
	deployStatuses  []string
	deployUpdateErr error
}

func (r *stubEnvironmentRepository) List(context.Context) ([]model.Environment, error) {
	return r.environments, r.err
}

func (r *stubEnvironmentRepository) UpdateDeployResult(_ context.Context, _, status, _, _ string) error {
	r.deployStatuses = append(r.deployStatuses, status)
	return r.deployUpdateErr
}

func (r *stubEnvironmentRepository) Count(context.Context) (int, error) {
	return r.count, r.countErr
}

// stubTenantQuotaRepository reports a fixed environment-count cap for the
// quota guardrail; maxEnvironments is the cap the handler compares against.
type stubTenantQuotaRepository struct {
	maxEnvironments int
	err             error
}

func (r stubTenantQuotaRepository) MaxEnvironments(context.Context) (int, error) {
	return r.maxEnvironments, r.err
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
