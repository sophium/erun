package routes

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

func TestCreateEnvironmentStartsDeployWhenWired(t *testing.T) {
	environments := &stubEnvironmentRepository{created: model.Environment{
		EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime, RuntimeVersion: "1.2.3",
	}}
	prov := &stubEnvironmentProvisioner{}
	rec := postCreateEnvironmentWired(t, environments, prov, `{"name":"prod","type":"runtime","runtimeVersion":"1.2.3"}`)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d (body %s), want 202 Accepted", rec.Code, rec.Body.String())
	}
	if len(prov.started) != 1 {
		t.Fatalf("Start called %d times, want 1", len(prov.started))
	}
	if got := prov.started[0]; got.EnvironmentID != "env-1" || got.Tenant != "acme" ||
		got.Environment != "prod" || got.Version != "1.2.3" || got.TenantID != "t1" {
		t.Fatalf("provision input = %+v", got)
	}
}

func TestCreateEnvironmentStartsDeployWithResourceCaps(t *testing.T) {
	environments := &stubEnvironmentRepository{created: model.Environment{
		EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime, RuntimeVersion: "1.2.3",
	}}
	prov := &stubEnvironmentProvisioner{}
	req := httptest.NewRequest(http.MethodPost, "/v1/environments", bytes.NewBufferString(`{"name":"prod","type":"runtime","runtimeVersion":"1.2.3"}`))
	req = req.WithContext(security.WithContext(req.Context(), security.Context{TenantID: "t1", TenantType: string(model.TenantTypeCompany), ErunUserID: "u1"}))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{
		environments: environments,
		contexts:     &stubContextRepository{},
		quotas: stubTenantQuotaRepository{
			maxEnvironments: 10, maxCPUMillicores: minRuntimeCPUMillicores,
			maxMemoryMB: minRuntimeMemoryMB, maxStorageGB: minRuntimeStorageGB,
		},
		tenants:     stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		provisioner: prov,
	}.createEnvironment(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d (body %s), want 202", rec.Code, rec.Body.String())
	}
	if len(prov.started) != 1 {
		t.Fatalf("Start called %d times, want 1", len(prov.started))
	}
	got := prov.started[0]
	if got.MaxCPUMillicores != minRuntimeCPUMillicores || got.MaxMemoryMB != minRuntimeMemoryMB || got.MaxStorageGB != minRuntimeStorageGB {
		t.Fatalf("resource caps = %+v, want %d/%d/%d", got, minRuntimeCPUMillicores, minRuntimeMemoryMB, minRuntimeStorageGB)
	}
}

// Refused at create time rather than the Job later failing pod admission.
func TestCreateEnvironmentRejectsQuotaBelowRuntimeFloor(t *testing.T) {
	environments := &stubEnvironmentRepository{created: model.Environment{
		EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime, RuntimeVersion: "1.2.3",
	}}
	prov := &stubEnvironmentProvisioner{}
	req := httptest.NewRequest(http.MethodPost, "/v1/environments", bytes.NewBufferString(`{"name":"prod","type":"runtime","runtimeVersion":"1.2.3"}`))
	req = req.WithContext(security.WithContext(req.Context(), security.Context{TenantID: "t1", TenantType: string(model.TenantTypeCompany), ErunUserID: "u1"}))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{
		environments: environments,
		contexts:     &stubContextRepository{},
		quotas:       stubTenantQuotaRepository{maxEnvironments: 10, maxCPUMillicores: 500, maxMemoryMB: 9216, maxStorageGB: 80},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		provisioner:  prov,
	}.createEnvironment(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d (body %s), want 409", rec.Code, rec.Body.String())
	}
	if environments.createCalls != 0 || len(prov.started) != 0 {
		t.Fatalf("createCalls=%d Start calls=%d, want 0/0: a quota below the floor must refuse before creating anything", environments.createCalls, len(prov.started))
	}
}

func TestCreateEnvironmentRejectsAggregateBudgetExceeded(t *testing.T) {
	environments := &stubEnvironmentRepository{
		created:            model.Environment{EnvironmentID: "env-2", Name: "staging", Type: model.EnvironmentTypeRuntime, RuntimeVersion: "1.2.3"},
		countByRuntimeType: 1,
	}
	prov := &stubEnvironmentProvisioner{}
	req := httptest.NewRequest(http.MethodPost, "/v1/environments", bytes.NewBufferString(`{"name":"staging","type":"runtime","runtimeVersion":"1.2.3"}`))
	req = req.WithContext(security.WithContext(req.Context(), security.Context{TenantID: "t1", TenantType: string(model.TenantTypeCompany), ErunUserID: "u1"}))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{
		environments: environments,
		contexts:     &stubContextRepository{},
		quotas: stubTenantQuotaRepository{
			maxEnvironments: 10, maxCPUMillicores: minRuntimeCPUMillicores,
			maxMemoryMB: minRuntimeMemoryMB, maxStorageGB: minRuntimeStorageGB,
			maxTotalCPUMillicores: minRuntimeCPUMillicores,
			maxTotalMemoryMB:      minRuntimeMemoryMB, maxTotalStorageGB: minRuntimeStorageGB,
		},
		tenants:     stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		provisioner: prov,
	}.createEnvironment(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d (body %s), want 409", rec.Code, rec.Body.String())
	}
	if environments.createCalls != 0 || len(prov.started) != 0 {
		t.Fatalf("createCalls=%d Start calls=%d, want 0/0: an exceeded aggregate budget must refuse before creating anything", environments.createCalls, len(prov.started))
	}
}

func TestCreateEnvironmentAllowsWithinAggregateBudget(t *testing.T) {
	environments := &stubEnvironmentRepository{
		created:            model.Environment{EnvironmentID: "env-2", Name: "staging", Type: model.EnvironmentTypeRuntime, RuntimeVersion: "1.2.3"},
		countByRuntimeType: 1,
	}
	prov := &stubEnvironmentProvisioner{}
	req := httptest.NewRequest(http.MethodPost, "/v1/environments", bytes.NewBufferString(`{"name":"staging","type":"runtime","runtimeVersion":"1.2.3"}`))
	req = req.WithContext(security.WithContext(req.Context(), security.Context{TenantID: "t1", TenantType: string(model.TenantTypeCompany), ErunUserID: "u1"}))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{
		environments: environments,
		contexts:     &stubContextRepository{},
		quotas: stubTenantQuotaRepository{
			maxEnvironments: 10, maxCPUMillicores: minRuntimeCPUMillicores,
			maxMemoryMB: minRuntimeMemoryMB, maxStorageGB: minRuntimeStorageGB,
			maxTotalCPUMillicores: 2 * minRuntimeCPUMillicores,
			maxTotalMemoryMB:      2 * minRuntimeMemoryMB, maxTotalStorageGB: 2 * minRuntimeStorageGB,
		},
		tenants:     stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		provisioner: prov,
	}.createEnvironment(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d (body %s), want 202 Accepted", rec.Code, rec.Body.String())
	}
	if environments.createCalls != 1 {
		t.Fatalf("createCalls = %d, want 1", environments.createCalls)
	}
}

func TestCreateEnvironmentStartFailureLeavesRowRegistered(t *testing.T) {
	environments := &stubEnvironmentRepository{created: model.Environment{
		EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime, RuntimeVersion: "1.2.3",
	}}
	prov := &stubEnvironmentProvisioner{err: errors.New("dbos: workflow start failed")}
	rec := postCreateEnvironmentWired(t, environments, prov, `{"name":"prod","type":"runtime","runtimeVersion":"1.2.3"}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d (body %s), want 500", rec.Code, rec.Body.String())
	}
}

func TestCreateEnvironmentRegistersOnlyWithoutVersion(t *testing.T) {
	environments := &stubEnvironmentRepository{created: model.Environment{
		EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime,
	}}
	prov := &stubEnvironmentProvisioner{}
	rec := postCreateEnvironmentWired(t, environments, prov, `{"name":"prod","type":"runtime"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if len(prov.started) != 0 {
		t.Fatalf("Start called %d times, want 0 (no version to deploy)", len(prov.started))
	}
}

func TestCreateEnvironmentRegistersOnlyForNonRuntime(t *testing.T) {
	environments := &stubEnvironmentRepository{created: model.Environment{
		EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRemoteAgent, RuntimeVersion: "1.2.3",
	}}
	prov := &stubEnvironmentProvisioner{}
	rec := postCreateEnvironmentWired(t, environments, prov, `{"name":"prod","type":"remote-agent","runtimeVersion":"1.2.3"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if len(prov.started) != 0 {
		t.Fatalf("Start called %d times, want 0 (non-runtime env)", len(prov.started))
	}
}
