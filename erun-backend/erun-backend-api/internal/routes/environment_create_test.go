package routes

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

func TestCreateEnvironmentRejectsInvalidInput(t *testing.T) {
	cases := map[string]string{
		"missing name":        `{"type":"runtime"}`,
		"missing type":        `{"name":"prod"}`,
		"unknown type":        `{"name":"prod","type":"staging"}`,
		"empty body":          `{}`,
		"malformed json":      `{`,
		"uppercase name":      `{"name":"Prod","type":"runtime"}`,
		"hyphen-bounded name": `{"name":"-prod","type":"runtime"}`,
		"space in name":       `{"name":"my env","type":"runtime"}`,
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			environments := &stubEnvironmentRepository{}
			rec := postCreateEnvironment(t, environments, underCapQuota, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("unexpected status: %d", rec.Code)
			}
			if environments.createCalls != 0 {
				t.Fatalf("Create should not run on invalid input, got %d calls", environments.createCalls)
			}
		})
	}
}

func TestCreateEnvironmentPersistsAndReturnsRow(t *testing.T) {
	// Agent environments accept a context reference: they are never deployed, so
	// runtime placement rules do not apply.
	for _, envType := range []string{"remote-agent", "local-agent"} {
		t.Run(envType, func(t *testing.T) {
			environments := &stubEnvironmentRepository{created: model.Environment{EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentType(envType)}}
			rec := postCreateEnvironment(t, environments, underCapQuota, `{
				"name": "prod",
				"type": "`+envType+`",
				"contextId": "ctx-1",
				"kubernetesContext": "primary",
				"runtimeVersion": "1.2.3"
			}`)

			if rec.Code != http.StatusCreated {
				t.Fatalf("unexpected status: %d", rec.Code)
			}
			if environments.createCalls != 1 {
				t.Fatalf("expected exactly one Create call, got %d", environments.createCalls)
			}
			if environments.createInput.ContextID != "ctx-1" || environments.createInput.RuntimeVersion != "1.2.3" {
				t.Fatalf("unexpected create input: %+v", environments.createInput)
			}

			var response model.Environment
			if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.EnvironmentID != "env-1" {
				t.Fatalf("unexpected persisted environment: %+v", response)
			}
		})
	}
}

// A bare kubernetesContext has no known credential, so runtime placement refuses it.
func TestCreateEnvironmentRejectsRawKubernetesContext(t *testing.T) {
	environments := &stubEnvironmentRepository{created: model.Environment{EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime}}
	rec := postCreateEnvironment(t, environments, underCapQuota, `{"name":"prod","type":"runtime","kubernetesContext":"primary","runtimeVersion":"1.2.3"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d (body %s), want 400 Bad Request", rec.Code, rec.Body.String())
	}
	if environments.createCalls != 0 {
		t.Fatal("a rejected placement must not persist a row that can never deploy")
	}
}

func TestCreateEnvironmentPlacesOnExplicitContext(t *testing.T) {
	environments := &stubEnvironmentRepository{created: model.Environment{EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime, ContextID: "ctx-1"}}
	contexts := &stubContextRepository{cloudContext: model.Context{
		ContextID: "ctx-1", Name: "prod-cluster", Status: "running",
		PublicIP: "203.0.113.10", KubernetesContext: "prod-cluster", MaxEnvironments: 5,
	}}
	req := httptest.NewRequest(http.MethodPost, "/v1/environments", bytes.NewBufferString(`{"name":"prod","type":"runtime","contextId":"ctx-1","runtimeVersion":"1.2.3"}`))
	req = req.WithContext(security.WithContext(req.Context(), security.Context{TenantID: "t1", TenantType: string(model.TenantTypeCompany), ErunUserID: "u1"}))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{environments: environments, quotas: underCapQuota, contexts: contexts}.createEnvironment(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d (body %s), want 201 Created", rec.Code, rec.Body.String())
	}
	if environments.createInput.ContextID != "ctx-1" {
		t.Fatalf("createInput.ContextID = %q, want ctx-1", environments.createInput.ContextID)
	}
}

// A contextId from another tenant is RLS-invisible, so it reads as unknown (400).
func TestCreateEnvironmentRejectsUnknownContext(t *testing.T) {
	environments := &stubEnvironmentRepository{created: model.Environment{EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime}}
	contexts := &stubContextRepository{err: repository.ErrNotFound}
	req := httptest.NewRequest(http.MethodPost, "/v1/environments", bytes.NewBufferString(`{"name":"prod","type":"runtime","contextId":"ctx-missing","runtimeVersion":"1.2.3"}`))
	req = req.WithContext(security.WithContext(req.Context(), security.Context{TenantID: "t1", TenantType: string(model.TenantTypeCompany), ErunUserID: "u1"}))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{environments: environments, quotas: underCapQuota, contexts: contexts}.createEnvironment(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d (body %s), want 400 Bad Request", rec.Code, rec.Body.String())
	}
	if environments.createCalls != 0 {
		t.Fatal("an unknown context must not persist a row")
	}
}

func TestCreateEnvironmentRejectsContextAtCapacity(t *testing.T) {
	environments := &stubEnvironmentRepository{
		created:        model.Environment{EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime},
		countByContext: map[string]int{"ctx-1": 5},
	}
	contexts := &stubContextRepository{cloudContext: model.Context{
		ContextID: "ctx-1", Name: "prod-cluster", Status: "running",
		PublicIP: "203.0.113.10", KubernetesContext: "prod-cluster", MaxEnvironments: 5,
	}}
	req := httptest.NewRequest(http.MethodPost, "/v1/environments", bytes.NewBufferString(`{"name":"prod","type":"runtime","contextId":"ctx-1","runtimeVersion":"1.2.3"}`))
	req = req.WithContext(security.WithContext(req.Context(), security.Context{TenantID: "t1", TenantType: string(model.TenantTypeCompany), ErunUserID: "u1"}))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{environments: environments, quotas: underCapQuota, contexts: contexts}.createEnvironment(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d (body %s), want 409 Conflict", rec.Code, rec.Body.String())
	}
	if environments.createCalls != 0 {
		t.Fatal("a context at capacity must not persist a row")
	}
}

func TestCreateEnvironmentAutoSelectsRunningContextWithRoom(t *testing.T) {
	environments := &stubEnvironmentRepository{created: model.Environment{EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime, ContextID: "ctx-1"}}
	contexts := &stubContextRepository{contexts: []model.Context{
		{ContextID: "ctx-1", Name: "prod-cluster", Status: "running", PublicIP: "203.0.113.10", KubernetesContext: "prod-cluster", MaxEnvironments: 5},
	}}
	req := httptest.NewRequest(http.MethodPost, "/v1/environments", bytes.NewBufferString(`{"name":"prod","type":"runtime","runtimeVersion":"1.2.3"}`))
	req = req.WithContext(security.WithContext(req.Context(), security.Context{TenantID: "t1", TenantType: string(model.TenantTypeCompany), ErunUserID: "u1"}))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{environments: environments, quotas: underCapQuota, contexts: contexts}.createEnvironment(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d (body %s), want 201 Created", rec.Code, rec.Body.String())
	}
	if environments.createInput.ContextID != "ctx-1" {
		t.Fatalf("createInput.ContextID = %q, want auto-selected ctx-1", environments.createInput.ContextID)
	}
}

func TestCreateEnvironmentWithNoRegisteredContextsPlacesOnThePlatformCluster(t *testing.T) {
	environments := &stubEnvironmentRepository{created: model.Environment{EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime}}
	rec := postCreateEnvironment(t, environments, underCapQuota, `{"name":"prod","type":"runtime","runtimeVersion":"1.2.3"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d (body %s), want 201 Created", rec.Code, rec.Body.String())
	}
	if environments.createInput.ContextID != "" {
		t.Fatalf("createInput.ContextID = %q, want empty (the platform's own cluster)", environments.createInput.ContextID)
	}
}

// Never silently falls back to the platform cluster once contexts are registered.
func TestCreateEnvironmentRefusesWhenAllRegisteredContextsAreFull(t *testing.T) {
	environments := &stubEnvironmentRepository{
		created:        model.Environment{EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime},
		countByContext: map[string]int{"ctx-1": 5},
	}
	contexts := &stubContextRepository{contexts: []model.Context{
		{ContextID: "ctx-1", Name: "prod-cluster", Status: "running", PublicIP: "203.0.113.10", KubernetesContext: "prod-cluster", MaxEnvironments: 5},
	}}
	req := httptest.NewRequest(http.MethodPost, "/v1/environments", bytes.NewBufferString(`{"name":"prod","type":"runtime","runtimeVersion":"1.2.3"}`))
	req = req.WithContext(security.WithContext(req.Context(), security.Context{TenantID: "t1", TenantType: string(model.TenantTypeCompany), ErunUserID: "u1"}))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{environments: environments, quotas: underCapQuota, contexts: contexts}.createEnvironment(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d (body %s), want 409 Conflict", rec.Code, rec.Body.String())
	}
	if environments.createCalls != 0 {
		t.Fatal("an exhausted inventory must not persist a row")
	}
}

func TestCreateEnvironmentSurfacesRepositoryError(t *testing.T) {
	// remote-agent so the request reaches the repository; the FK error must not leak SQL.
	environments := &stubEnvironmentRepository{err: foreignKeyError{}}
	rec := postCreateEnvironment(t, environments, underCapQuota, `{"name":"prod","type":"remote-agent","contextId":"ctx-other"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if environments.createCalls != 1 {
		t.Fatalf("expected exactly one Create call, got %d", environments.createCalls)
	}
}

func TestCreateEnvironmentRejectsAtQuota(t *testing.T) {
	environments := &stubEnvironmentRepository{count: 10}
	rec := postCreateEnvironment(t, environments, stubTenantQuotaRepository{maxEnvironments: 10}, `{"name":"prod","type":"runtime"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if environments.createCalls != 0 {
		t.Fatalf("Create must not run when the tenant is at its quota, got %d calls", environments.createCalls)
	}
}

func TestCreateEnvironmentAllowsUnderQuota(t *testing.T) {
	environments := &stubEnvironmentRepository{count: 9}
	rec := postCreateEnvironment(t, environments, stubTenantQuotaRepository{maxEnvironments: 10}, `{"name":"prod","type":"runtime"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if environments.createCalls != 1 {
		t.Fatalf("expected exactly one Create call, got %d", environments.createCalls)
	}
}

type foreignKeyError struct{}

func (foreignKeyError) Error() string { return "foreign key violation" }
