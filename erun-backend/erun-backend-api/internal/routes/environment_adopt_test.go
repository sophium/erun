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

// TestCreateEnvironmentAdoptRegistersWithoutDeploy: adopting a runtime
// environment that already runs somewhere the operator manages
// records the row and dispatches no deploy, even though the type is
// runtime — the case the ordinary create path would otherwise refuse
// (errCrossClusterPlacementUnsupported) or deploy into (a pinned version).
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

// TestCreateEnvironmentAdoptRequiresKubernetesContext: an adopt request
// names nothing to record without a kubernetesContext.
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

// TestCreateEnvironmentAdoptForbidsRuntimeVersion: a runtimeVersion would
// start a deploy on the ordinary path, exactly what adopt must never do.
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

// TestCreateEnvironmentAdoptForbidsContextID: adopt records a raw
// kubernetesContext, not a platform-managed placement reference.
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

// TestCreateEnvironmentAdoptSkipsRuntimeResourceQuotaFloor: adopting
// consumes no platform-managed cluster capacity, so a quota below the
// runtime namespace floor — which the ordinary create path refuses (see
// TestCreateEnvironmentRejectsQuotaBelowRuntimeFloor) — must not block it.
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

// TestCreateEnvironmentAdoptWithoutIntentStillBehavesAsCreate: a name that
// happens to match an existing local environment, submitted without adopt
// set, still creates a fresh row exactly as before adopt existed — adopting
// is never inferred from a name collision.
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

// TestCreateEnvironmentPreviewAdoptRendersAdoptPlanWithoutDeployLine: the
// preview an operator sees before adopting must never claim a deploy will
// happen — the "preview that cannot model what submit does" defect class.
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
