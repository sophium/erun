package routes

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

func TestCreateEnvironmentAdoptRegistersWithoutDeploy(t *testing.T) {
	environments := &stubEnvironmentRepository{created: model.Environment{
		EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime, KubernetesContext: "primary",
	}}
	prov := &stubEnvironmentProvisioner{}
	rec := postCreateEnvironmentWired(t, environments, prov, `{"name":"prod","type":"runtime","kubernetesContext":"primary","adopt":true}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d (body %s), want 201", rec.Code, rec.Body.String())
	}
	if environments.createCalls != 1 {
		t.Fatalf("Create called %d times, want 1", environments.createCalls)
	}
	if environments.createInput.KubernetesContext != "primary" {
		t.Fatalf("Create saw kubernetesContext = %q, want primary", environments.createInput.KubernetesContext)
	}
	if len(prov.started) != 0 {
		t.Fatalf("Start called %d times, want 0: adopting an existing environment must never dispatch a deploy", len(prov.started))
	}
}

func TestCreateEnvironmentAdoptRequiresKubernetesContext(t *testing.T) {
	environments := &stubEnvironmentRepository{}
	rec := postCreateEnvironment(t, environments, underCapQuota, `{"name":"prod","type":"runtime","adopt":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if environments.createCalls != 0 {
		t.Fatalf("Create should not run, got %d calls", environments.createCalls)
	}
}

func TestCreateEnvironmentAdoptForbidsRuntimeVersion(t *testing.T) {
	environments := &stubEnvironmentRepository{}
	rec := postCreateEnvironment(t, environments, underCapQuota,
		`{"name":"prod","type":"runtime","kubernetesContext":"primary","runtimeVersion":"1.2.3","adopt":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if environments.createCalls != 0 {
		t.Fatalf("Create should not run, got %d calls", environments.createCalls)
	}
}

func TestCreateEnvironmentAdoptForbidsContextID(t *testing.T) {
	environments := &stubEnvironmentRepository{}
	rec := postCreateEnvironment(t, environments, underCapQuota,
		`{"name":"prod","type":"runtime","kubernetesContext":"primary","contextId":"ctx-1","adopt":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if environments.createCalls != 0 {
		t.Fatalf("Create should not run, got %d calls", environments.createCalls)
	}
}

func TestCreateEnvironmentAdoptSkipsRuntimeResourceQuotaFloor(t *testing.T) {
	environments := &stubEnvironmentRepository{created: model.Environment{
		EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime, KubernetesContext: "primary",
	}}
	req := httptest.NewRequest(http.MethodPost, "/v1/environments", bytes.NewBufferString(
		`{"name":"prod","type":"runtime","kubernetesContext":"primary","adopt":true}`))
	req = req.WithContext(security.WithContext(req.Context(), security.Context{TenantID: "t1", TenantType: string(model.TenantTypeCompany), ErunUserID: "u1"}))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{
		environments: environments,
		contexts:     &stubContextRepository{},
		quotas:       stubTenantQuotaRepository{maxEnvironments: 10, maxCPUMillicores: 500, maxMemoryMB: 9216, maxStorageGB: 80},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
	}.createEnvironment(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d (body %s), want 201", rec.Code, rec.Body.String())
	}
	if environments.createCalls != 1 {
		t.Fatalf("Create called %d times, want 1", environments.createCalls)
	}
}

func TestCreateEnvironmentAdoptWithoutIntentStillBehavesAsCreate(t *testing.T) {
	environments := &stubEnvironmentRepository{created: model.Environment{
		EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRemoteAgent, KubernetesContext: "primary",
	}}
	prov := &stubEnvironmentProvisioner{}
	rec := postCreateEnvironmentWired(t, environments, prov, `{"name":"prod","type":"remote-agent","kubernetesContext":"primary"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d (body %s), want 201", rec.Code, rec.Body.String())
	}
	if environments.createCalls != 1 {
		t.Fatalf("Create called %d times, want 1", environments.createCalls)
	}
}

func TestCreateEnvironmentPreviewAdoptRendersAdoptPlanWithoutDeployLine(t *testing.T) {
	environments := &stubEnvironmentRepository{count: 2}
	rec := postCreateEnvironmentPreview(t, environments, stubTenantQuotaRepository{maxEnvironments: 10}, `{
		"name": "prod", "type": "runtime", "kubernetesContext": "primary", "adopt": true, "preview": true
	}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s), want 200", rec.Code, rec.Body.String())
	}
	if environments.createCalls != 0 {
		t.Fatal("preview must never call Create")
	}
	var response provisionResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.QuotaOk {
		t.Fatalf("expected quotaOk=true under the cap: %v", response.Plan)
	}
	mustPlanLine(t, response.Plan, "deploy: skipped", "adopt preview plan must say the deploy is skipped")
	mustNotPlanLine(t, response.Plan, "would helm install", "adopt preview plan must never claim a deploy will happen")
}
