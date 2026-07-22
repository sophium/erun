package routes

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/provision"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

func postCreateEnvironment(t *testing.T, environments *stubEnvironmentRepository, quotas stubTenantQuotaRepository, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/environments", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{environments: environments, quotas: quotas}.createEnvironment(rec, req)
	return rec
}

type stubEnvironmentProvisioner struct {
	started []provision.EnvProvisionInput
	err     error
}

func (s *stubEnvironmentProvisioner) Start(in provision.EnvProvisionInput) error {
	s.started = append(s.started, in)
	return s.err
}

// postCreateEnvironmentWired posts through a fully-wired route (tenant resolver +
// provisioner) with a request-scoped security context, the shape the live server
// builds.
func postCreateEnvironmentWired(t *testing.T, environments *stubEnvironmentRepository, prov *stubEnvironmentProvisioner, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/environments", bytes.NewBufferString(body))
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   "t1",
		TenantType: string(model.TenantTypeCompany),
		ErunUserID: "u1",
	}))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{
		environments: environments,
		quotas:       underCapQuota,
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		provisioner:  prov,
	}.createEnvironment(rec, req)
	return rec
}

var underCapQuota = stubTenantQuotaRepository{maxEnvironments: 10}

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
	for _, envType := range []string{"runtime", "remote-agent", "local-agent"} {
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
			// Tenant binding comes from RLS, not the request body.
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

func TestCreateEnvironmentSurfacesRepositoryError(t *testing.T) {
	// A context_id from another tenant violates the composite foreign key; the
	// repository error is surfaced as a clean HTTP error, not a SQL leak.
	environments := &stubEnvironmentRepository{err: errForeignKey{}}
	rec := postCreateEnvironment(t, environments, underCapQuota, `{"name":"prod","type":"runtime","contextId":"ctx-other"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if environments.createCalls != 1 {
		t.Fatalf("expected exactly one Create call, got %d", environments.createCalls)
	}
}

// TestCreateEnvironmentRejectsAtQuota enforces the per-tenant environment-count cap.
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

// TestCreateEnvironmentAllowsUnderQuota lets registration proceed below the environment cap.
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

// TestCreateEnvironmentStartsDeployWhenWired: a wired provisioner flips a
// runtime env with a pinned version from an inline 201 to an async deploy
// workflow (202 Accepted), threading the tenant name and version to the deploy.
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

// TestCreateEnvironmentRegistersOnlyWithoutVersion: nothing to deploy without a
// pinned runtime version, so create stays a plain 201 registration.
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

// TestCreateEnvironmentRegistersOnlyForNonRuntime: the deploy executor targets
// runtime envs; an agent env is registered (201) without a deploy.
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

type errForeignKey struct{}

func (errForeignKey) Error() string { return "foreign key violation" }
