package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/provision"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

func postCreateEnvironment(t *testing.T, environments *stubEnvironmentRepository, quotas stubTenantQuotaRepository, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/environments", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{environments: environments, quotas: quotas, contexts: &stubContextRepository{}}.createEnvironment(rec, req)
	return rec
}

type stubEnvironmentProvisioner struct {
	started  []provision.EnvProvisionInput
	deployed []provision.EnvProvisionInput
	err      error
}

func (s *stubEnvironmentProvisioner) Start(in provision.EnvProvisionInput) error {
	s.started = append(s.started, in)
	return s.err
}

func (s *stubEnvironmentProvisioner) StartDeploy(in provision.EnvProvisionInput) error {
	s.deployed = append(s.deployed, in)
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
		contexts:     &stubContextRepository{},
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
	// Only remote-agent/local-agent environments accept a context reference on
	// create: they are never server-side deployed, so the v1 single-cluster
	// placement refusal (TestCreateEnvironmentRejectsCrossClusterPlacement)
	// does not apply to them.
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

// TestCreateEnvironmentRejectsRawKubernetesContext: naming an existing
// cluster is by contextId (#1112) — a bare kubernetesContext string has no
// known credential to authenticate with, so it stays refused for a runtime
// environment even after contextId placement exists.
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

// TestCreateEnvironmentPlacesOnExplicitContext: a runtime environment naming
// a registered, running contextId with room is placed there (#1112) — the
// v1 refusal (TestCreateEnvironmentRejectsRawKubernetesContext) only ever
// covered a bare kubernetesContext string, not a real contextId reference.
func TestCreateEnvironmentPlacesOnExplicitContext(t *testing.T) {
	environments := &stubEnvironmentRepository{created: model.Environment{EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime, ContextID: "ctx-1"}}
	contexts := &stubContextRepository{cloudContext: model.Context{
		ContextID: "ctx-1", Name: "prod-cluster", Status: "running",
		PublicIP: "203.0.113.10", KubernetesContext: "prod-cluster", MaxEnvironments: 5,
	}}
	req := httptest.NewRequest(http.MethodPost, "/v1/environments", bytes.NewBufferString(`{"name":"prod","type":"runtime","contextId":"ctx-1","runtimeVersion":"1.2.3"}`))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{environments: environments, quotas: underCapQuota, contexts: contexts}.createEnvironment(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d (body %s), want 201 Created", rec.Code, rec.Body.String())
	}
	if environments.createInput.ContextID != "ctx-1" {
		t.Fatalf("createInput.ContextID = %q, want ctx-1", environments.createInput.ContextID)
	}
}

// TestCreateEnvironmentRejectsUnknownContext: a contextId that does not
// resolve for the caller's tenant (unregistered, or belonging to another
// tenant — PlacementContextRepository's reads are RLS-scoped) is a caller
// mistake, not a server fault.
func TestCreateEnvironmentRejectsUnknownContext(t *testing.T) {
	environments := &stubEnvironmentRepository{created: model.Environment{EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime}}
	contexts := &stubContextRepository{err: repository.ErrNotFound}
	req := httptest.NewRequest(http.MethodPost, "/v1/environments", bytes.NewBufferString(`{"name":"prod","type":"runtime","contextId":"ctx-missing","runtimeVersion":"1.2.3"}`))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{environments: environments, quotas: underCapQuota, contexts: contexts}.createEnvironment(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d (body %s), want 400 Bad Request", rec.Code, rec.Body.String())
	}
	if environments.createCalls != 0 {
		t.Fatal("an unknown context must not persist a row")
	}
}

// TestCreateEnvironmentRejectsContextAtCapacity: an explicit contextId with
// no room fails clearly (409, naming the context and its ceiling) rather
// than persisting a row and failing inside the Job (#1112).
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
	rec := httptest.NewRecorder()
	EnvironmentRoutes{environments: environments, quotas: underCapQuota, contexts: contexts}.createEnvironment(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d (body %s), want 409 Conflict", rec.Code, rec.Body.String())
	}
	if environments.createCalls != 0 {
		t.Fatal("a context at capacity must not persist a row")
	}
}

// TestCreateEnvironmentAutoSelectsRunningContextWithRoom: a runtime
// environment naming no context auto-selects the tenant's own registered
// context when one is running and has room (#1112).
func TestCreateEnvironmentAutoSelectsRunningContextWithRoom(t *testing.T) {
	environments := &stubEnvironmentRepository{created: model.Environment{EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime, ContextID: "ctx-1"}}
	contexts := &stubContextRepository{contexts: []model.Context{
		{ContextID: "ctx-1", Name: "prod-cluster", Status: "running", PublicIP: "203.0.113.10", KubernetesContext: "prod-cluster", MaxEnvironments: 5},
	}}
	req := httptest.NewRequest(http.MethodPost, "/v1/environments", bytes.NewBufferString(`{"name":"prod","type":"runtime","runtimeVersion":"1.2.3"}`))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{environments: environments, quotas: underCapQuota, contexts: contexts}.createEnvironment(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d (body %s), want 201 Created", rec.Code, rec.Body.String())
	}
	if environments.createInput.ContextID != "ctx-1" {
		t.Fatalf("createInput.ContextID = %q, want auto-selected ctx-1", environments.createInput.ContextID)
	}
}

// TestCreateEnvironmentWithNoRegisteredContextsPlacesOnThePlatformCluster: a
// tenant with no registered contexts keeps the pre-#1112 default — the
// platform's own cluster (empty contextId) — rather than being refused.
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

// TestCreateEnvironmentRefusesWhenAllRegisteredContextsAreFull: once a
// tenant has registered contexts, exhausting all of them fails clearly
// (#1112's "or fail clearly when none qualifies") rather than silently
// falling back to the platform's own cluster, which the caller never asked
// for.
func TestCreateEnvironmentRefusesWhenAllRegisteredContextsAreFull(t *testing.T) {
	environments := &stubEnvironmentRepository{
		created:        model.Environment{EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime},
		countByContext: map[string]int{"ctx-1": 5},
	}
	contexts := &stubContextRepository{contexts: []model.Context{
		{ContextID: "ctx-1", Name: "prod-cluster", Status: "running", PublicIP: "203.0.113.10", KubernetesContext: "prod-cluster", MaxEnvironments: 5},
	}}
	req := httptest.NewRequest(http.MethodPost, "/v1/environments", bytes.NewBufferString(`{"name":"prod","type":"runtime","runtimeVersion":"1.2.3"}`))
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
	// A context_id from another tenant violates the composite foreign key; the
	// repository error is surfaced as a clean HTTP error, not a SQL leak.
	// remote-agent (not runtime) so the request clears the v1 single-cluster
	// placement check, which now refuses a runtime environment's contextId
	// outright before the repository is ever reached.
	environments := &stubEnvironmentRepository{err: errForeignKey{}}
	rec := postCreateEnvironment(t, environments, underCapQuota, `{"name":"prod","type":"remote-agent","contextId":"ctx-other"}`)
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

// TestCreateEnvironmentStartsDeployWithResourceCaps: the tenant's quota row
// (env count plus the per-environment CPU/memory/storage namespace ceiling)
// threads through to the provisioner input unchanged (#605), not just the
// tenant/environment/version coordinates TestCreateEnvironmentStartsDeployWhenWired
// already locks.
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
		quotas:       stubTenantQuotaRepository{maxEnvironments: 10, maxCPUMillicores: 9000, maxMemoryMB: 20000, maxStorageGB: 100},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		provisioner:  prov,
	}.createEnvironment(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d (body %s), want 202", rec.Code, rec.Body.String())
	}
	if len(prov.started) != 1 {
		t.Fatalf("Start called %d times, want 1", len(prov.started))
	}
	got := prov.started[0]
	if got.MaxCPUMillicores != 9000 || got.MaxMemoryMB != 20000 || got.MaxStorageGB != 100 {
		t.Fatalf("resource caps = %+v, want 9000/20000/100", got)
	}
}

// TestCreateEnvironmentRejectsQuotaBelowRuntimeFloor: a tenant quota configured
// below what a stock runtime pod needs is refused at create time with a clear
// 409, instead of the Job later failing to admit the pod under its own
// ResourceQuota (#605).
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

// TestCreateEnvironmentRejectsAggregateBudgetExceeded: a tenant already
// running one runtime environment at the per-environment cap, whose
// aggregate budget has no room for a second, is refused clearly (409) rather
// than persisting a row that would push the tenant over its contracted
// total (#1113).
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
			maxEnvironments: 10, maxCPUMillicores: 8000, maxMemoryMB: 17832, maxStorageGB: 72,
			maxTotalCPUMillicores: 8000, maxTotalMemoryMB: 17832, maxTotalStorageGB: 72,
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

// TestCreateEnvironmentAllowsWithinAggregateBudget: the mirror of the above —
// a second runtime environment that fits within the tenant's aggregate
// budget is admitted normally.
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
			maxEnvironments: 10, maxCPUMillicores: 8000, maxMemoryMB: 17832, maxStorageGB: 72,
			maxTotalCPUMillicores: 16000, maxTotalMemoryMB: 35664, maxTotalStorageGB: 144,
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

// TestDeployEnvironmentRejectsAggregateBudgetExceeded: a redeploy re-checks
// the aggregate budget too, using the runtime count as-is (the environment
// being redeployed is already counted, unlike create's +1).
func TestDeployEnvironmentRejectsAggregateBudgetExceeded(t *testing.T) {
	environments := runtimeEnvironment()
	environments.countByRuntimeType = 1
	prov := &stubEnvironmentProvisioner{}
	req := httptest.NewRequest(http.MethodPost, "/v1/environments/env-1/deploy", bytes.NewBufferString(""))
	req.SetPathValue("environment_id", "env-1")
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   "t1",
		TenantType: string(model.TenantTypeCompany),
		ErunUserID: "u1",
	}))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{
		environments: environments,
		contexts:     &stubContextRepository{},
		quotas: stubTenantQuotaRepository{
			maxEnvironments: 10, maxCPUMillicores: 8000, maxMemoryMB: 17832, maxStorageGB: 72,
			maxTotalCPUMillicores: 4000, maxTotalMemoryMB: 17832, maxTotalStorageGB: 72,
		},
		tenants:     stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		provisioner: prov,
	}.deployEnvironment(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d (body %s), want 409", rec.Code, rec.Body.String())
	}
	if len(prov.deployed) != 0 {
		t.Fatalf("StartDeploy calls = %d, want 0: an exceeded aggregate budget must refuse before claiming", len(prov.deployed))
	}
}

// TestCreateEnvironmentStartFailureLeavesRowRegistered locks
// writeStartProvisioningError: any failure to enqueue the durable workflow
// answers 500, and the row created just before stays registered
// (persistAndMaybeProvision already wrote it) — there is nothing to unwind
// since the deploy Job was never started. A confirmed-missing tenant runtime
// image is no longer this path: it now selects the canonical-image bootstrap
// instead of failing Start.
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

// postDeployEnvironment posts to the explicit deploy endpoint through a
// fully-wired route with a request-scoped security context.
func postDeployEnvironment(t *testing.T, environments *stubEnvironmentRepository, prov EnvironmentProvisioner, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/environments/env-1/deploy", bytes.NewBufferString(body))
	req.SetPathValue("environment_id", "env-1")
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   "t1",
		TenantType: string(model.TenantTypeCompany),
		ErunUserID: "u1",
	}))
	rec := httptest.NewRecorder()
	routes := EnvironmentRoutes{
		environments: environments,
		contexts:     &stubContextRepository{},
		quotas:       underCapQuota,
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
	}
	if prov != nil {
		routes.provisioner = prov
	}
	routes.deployEnvironment(rec, req)
	return rec
}

func runtimeEnvironment() *stubEnvironmentRepository {
	return &stubEnvironmentRepository{environment: model.Environment{
		EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime, RuntimeVersion: "1.2.3",
	}}
}

// TestDeployEnvironmentDeploysPinnedVersion: a body-less deploy is the common
// case — deploy what the environment is already pinned to.
func TestDeployEnvironmentDeploysPinnedVersion(t *testing.T) {
	environments := runtimeEnvironment()
	prov := &stubEnvironmentProvisioner{}
	rec := postDeployEnvironment(t, environments, prov, "")

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d (body %s), want 202 Accepted", rec.Code, rec.Body.String())
	}
	if len(prov.deployed) != 1 {
		t.Fatalf("StartDeploy called %d times, want 1", len(prov.deployed))
	}
	got := prov.deployed[0]
	if got.Version != "1.2.3" || got.EnvironmentID != "env-1" || got.Tenant != "acme" || got.Environment != "prod" {
		t.Fatalf("deploy input = %+v", got)
	}
	if got.DeployID == "" {
		t.Fatal("deploy input carries no attempt id, so a retry would replay the previous deploy")
	}
}

// TestDeployEnvironmentDeploysRequestedVersion: an explicit version is how an
// environment moves between published runtime versions after creation.
func TestDeployEnvironmentDeploysRequestedVersion(t *testing.T) {
	prov := &stubEnvironmentProvisioner{}
	rec := postDeployEnvironment(t, runtimeEnvironment(), prov, `{"version":"1.3.0"}`)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	if prov.deployed[0].Version != "1.3.0" {
		t.Fatalf("deployed version = %q, want the requested 1.3.0", prov.deployed[0].Version)
	}
}

// TestDeployEnvironmentGivesEachAttemptItsOwnID: two deploys must not collapse
// onto one workflow, or the second would be a silent no-op.
func TestDeployEnvironmentGivesEachAttemptItsOwnID(t *testing.T) {
	prov := &stubEnvironmentProvisioner{}
	postDeployEnvironment(t, runtimeEnvironment(), prov, "")
	postDeployEnvironment(t, runtimeEnvironment(), prov, "")

	if len(prov.deployed) != 2 {
		t.Fatalf("StartDeploy called %d times, want 2", len(prov.deployed))
	}
	if prov.deployed[0].DeployID == prov.deployed[1].DeployID {
		t.Fatalf("both deploys share attempt id %q", prov.deployed[0].DeployID)
	}
}

// TestDeployEnvironmentRejectsConcurrentDeploy: the claim is what keeps a
// double-submit from running two rollouts into the same release.
func TestDeployEnvironmentRejectsConcurrentDeploy(t *testing.T) {
	environments := runtimeEnvironment()
	environments.claimTaken = true
	prov := &stubEnvironmentProvisioner{}
	rec := postDeployEnvironment(t, environments, prov, "")

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 Conflict", rec.Code)
	}
	if len(prov.deployed) != 0 {
		t.Fatalf("StartDeploy called %d times, want 0 when the claim is held", len(prov.deployed))
	}
}

// TestDeployEnvironmentRejectsQuotaBelowRuntimeFloor: an operator can lower a
// tenant's quota (PUT .../quota) after the environment already exists; the
// next deploy must refuse before claiming, rather than claim the environment
// and only discover the shortfall as a five-minute rollout timeout (#1061).
func TestDeployEnvironmentRejectsQuotaBelowRuntimeFloor(t *testing.T) {
	environments := runtimeEnvironment()
	prov := &stubEnvironmentProvisioner{}
	req := httptest.NewRequest(http.MethodPost, "/v1/environments/env-1/deploy", bytes.NewBufferString(""))
	req.SetPathValue("environment_id", "env-1")
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   "t1",
		TenantType: string(model.TenantTypeCompany),
		ErunUserID: "u1",
	}))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{
		environments: environments,
		contexts:     &stubContextRepository{},
		quotas:       stubTenantQuotaRepository{maxEnvironments: 10, maxCPUMillicores: 500, maxMemoryMB: 9216, maxStorageGB: 80},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		provisioner:  prov,
	}.deployEnvironment(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d (body %s), want 409", rec.Code, rec.Body.String())
	}
	if environments.claimCalls != 0 || len(prov.deployed) != 0 {
		t.Fatalf("claimCalls=%d StartDeploy calls=%d, want 0/0: a quota below the floor must refuse before claiming anything", environments.claimCalls, len(prov.deployed))
	}
}

// TestDeployEnvironmentClaimsBeforeStarting: the claim must precede the
// workflow, otherwise the race it exists to prevent is still open.
func TestDeployEnvironmentClaimsBeforeStarting(t *testing.T) {
	environments := runtimeEnvironment()
	postDeployEnvironment(t, environments, &stubEnvironmentProvisioner{}, "")
	if environments.claimCalls != 1 {
		t.Fatalf("ClaimDeploy called %d times, want 1", environments.claimCalls)
	}
}

// TestDeployEnvironmentStartFailureMarksFailed locks writeStartDeployError:
// ClaimDeploy already moved the row to provisioning before StartDeploy ran, so
// any failure to enqueue the durable workflow must also mark the environment
// failed — otherwise it would be stranded in provisioning with no workflow
// left to ever move it out. A confirmed-missing tenant runtime image is no
// longer this path: it now selects the canonical-image bootstrap instead of
// failing StartDeploy.
func TestDeployEnvironmentStartFailureMarksFailed(t *testing.T) {
	environments := runtimeEnvironment()
	prov := &stubEnvironmentProvisioner{err: errors.New("dbos: workflow start failed")}
	rec := postDeployEnvironment(t, environments, prov, "")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d (body %s), want 500", rec.Code, rec.Body.String())
	}
	if environments.markFailedCalls != 1 {
		t.Fatalf("MarkDeployFailed called %d times, want 1", environments.markFailedCalls)
	}
	if !strings.Contains(environments.markFailedReason, "dbos: workflow start failed") {
		t.Fatalf("markFailedReason = %q, want it to name the failure", environments.markFailedReason)
	}
}

func TestDeployEnvironmentRejectsInvalidRequests(t *testing.T) {
	cases := map[string]struct {
		environment model.Environment
		body        string
		want        int
	}{
		"non-runtime env": {
			model.Environment{EnvironmentID: "env-1", Name: "agents", Type: model.EnvironmentTypeRemoteAgent, RuntimeVersion: "1.2.3"},
			"", http.StatusBadRequest,
		},
		"no version anywhere": {
			model.Environment{EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime},
			"", http.StatusBadRequest,
		},
		"malformed body": {
			model.Environment{EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime, RuntimeVersion: "1.2.3"},
			`{`, http.StatusBadRequest,
		},
	}
	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			environments := &stubEnvironmentRepository{environment: tc.environment}
			prov := &stubEnvironmentProvisioner{}
			rec := postDeployEnvironment(t, environments, prov, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d (body %s), want %d", rec.Code, rec.Body.String(), tc.want)
			}
			if environments.claimCalls != 0 {
				t.Fatal("an invalid request must not claim the environment's deploy slot")
			}
			if len(prov.deployed) != 0 {
				t.Fatalf("StartDeploy called %d times, want 0", len(prov.deployed))
			}
		})
	}
}

// TestDeployEnvironmentReportsUnconfiguredExecutor: without a wired executor the
// endpoint says so, rather than accepting a deploy nothing will run.
func TestDeployEnvironmentReportsUnconfiguredExecutor(t *testing.T) {
	environments := runtimeEnvironment()
	rec := postDeployEnvironment(t, environments, nil, "")
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 Not Implemented", rec.Code)
	}
	if environments.claimCalls != 0 {
		t.Fatal("an unconfigured executor must not claim the environment's deploy slot")
	}
}

type errForeignKey struct{}

func (errForeignKey) Error() string { return "foreign key violation" }

// postCreateEnvironmentPreview posts through a fully-wired route (tenant
// resolver, no provisioner needed since preview never reaches it) with a
// request-scoped security context, the shape the live server builds.
func postCreateEnvironmentPreview(t *testing.T, environments *stubEnvironmentRepository, quotas stubTenantQuotaRepository, body string) *httptest.ResponseRecorder {
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
		contexts:     &stubContextRepository{},
		quotas:       quotas,
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
	}.createEnvironment(rec, req)
	return rec
}

// TestCreateEnvironmentPreviewRendersProvisionPlanWithoutPersisting: the
// executing path's preview flag must resolve and return the same ordered
// plan POST /v1/provision renders, without ever calling Create — the plan an
// operator audits here is the plan a non-preview call then runs.
func TestCreateEnvironmentPreviewRendersProvisionPlanWithoutPersisting(t *testing.T) {
	environments := &stubEnvironmentRepository{count: 2}
	rec := postCreateEnvironmentPreview(t, environments, stubTenantQuotaRepository{maxEnvironments: 10}, `{
		"name": "prod", "type": "runtime", "preview": true
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
	mustPlanLine(t, response.Plan, "context: deploys into this platform's own cluster (v1 single-cluster placement)", "preview plan missing the placement line")
	mustPlanLine(t, response.Plan, "deploy: would helm install the erun-devops runtime chart", "preview plan missing the deploy line")
	mustPlanLine(t, response.Plan, "expose: would wire mcp.", "preview plan missing the exposure wiring line")
}

// TestCreateEnvironmentPreviewStillEnforcesPlacement: preview must apply the
// same placement check as a real create, not a laxer one — a raw
// kubernetesContext stays refused everywhere (#1112).
func TestCreateEnvironmentPreviewStillEnforcesPlacement(t *testing.T) {
	environments := &stubEnvironmentRepository{}
	rec := postCreateEnvironmentPreview(t, environments, underCapQuota, `{
		"name": "prod", "type": "runtime", "kubernetesContext": "primary", "preview": true
	}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 Bad Request", rec.Code)
	}
}

// runtimeFloorQuotaCases are the exact quota shapes
// TestCreateEnvironmentRejectsQuotaBelowRuntimeFloor and
// TestCreateEnvironmentAllowsWithinAggregateBudget already exercise on the
// executing path, reused here so both preview entry points are checked
// against the same table rather than a freshly invented one.
var runtimeFloorQuotaCases = map[string]struct {
	quota   stubTenantQuotaRepository
	floorOK bool
}{
	"below runtime floor": {
		quota:   stubTenantQuotaRepository{maxEnvironments: 10, maxCPUMillicores: 500, maxMemoryMB: 9216, maxStorageGB: 80},
		floorOK: false,
	},
	"meets runtime floor": {
		quota: stubTenantQuotaRepository{
			maxEnvironments: 10, maxCPUMillicores: 8000, maxMemoryMB: 17832, maxStorageGB: 72,
			maxTotalCPUMillicores: 16000, maxTotalMemoryMB: 35664, maxTotalStorageGB: 144,
		},
		floorOK: true,
	},
}

// TestProvisionPreviewAgreesWithCreateOnRuntimeQuotaFloor locks the fix for
// the provision preview skipping validateNamespaceQuotaFloor: both preview
// entry points (POST /v1/provision and POST /v1/environments?preview=true)
// must discharge the same runtime namespace-quota floor the executing create
// path enforces, across the same quota shapes, so a plan that previews as
// fine can never then 409 on the real request.
func TestProvisionPreviewAgreesWithCreateOnRuntimeQuotaFloor(t *testing.T) {
	for label, tc := range runtimeFloorQuotaCases {
		t.Run(label, func(t *testing.T) {
			previewRec := postCreateEnvironmentPreview(t, &stubEnvironmentRepository{}, tc.quota, `{"name":"prod","type":"runtime","preview":true}`)
			if previewRec.Code != http.StatusOK {
				t.Fatalf("preview status = %d, want 200", previewRec.Code)
			}
			previewResponse := decodeProvisionResponse(t, previewRec)
			if previewResponse.QuotaOk != tc.floorOK {
				t.Fatalf("preview quotaOk = %v, want %v: %v", previewResponse.QuotaOk, tc.floorOK, previewResponse.Plan)
			}

			provisionRec := postProvision(t, acmeTenant, &stubEnvironmentRepository{}, tc.quota, `{"environment":{"name":"prod","type":"runtime"}}`)
			if provisionRec.Code != http.StatusOK {
				t.Fatalf("provision status = %d, want 200", provisionRec.Code)
			}
			provisionResponse := decodeProvisionResponse(t, provisionRec)
			if provisionResponse.QuotaOk != tc.floorOK {
				t.Fatalf("provision quotaOk = %v, want %v: %v", provisionResponse.QuotaOk, tc.floorOK, provisionResponse.Plan)
			}

			if !tc.floorOK {
				mustPlanLine(t, previewResponse.Plan, "BELOW RUNTIME MINIMUM, provisioning blocked: tenant CPU quota", "preview plan must name the quota and the shortfall")
				mustPlanLine(t, provisionResponse.Plan, "BELOW RUNTIME MINIMUM, provisioning blocked: tenant CPU quota", "provision plan must name the quota and the shortfall")
			}

			environments := &stubEnvironmentRepository{created: model.Environment{EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime}}
			createRec := postCreateEnvironment(t, environments, tc.quota, `{"name":"prod","type":"runtime"}`)
			wantCreateStatus := http.StatusConflict
			if tc.floorOK {
				wantCreateStatus = http.StatusCreated
			}
			if createRec.Code != wantCreateStatus {
				t.Fatalf("create status = %d (body %s), want %d", createRec.Code, createRec.Body.String(), wantCreateStatus)
			}
			if previewResponse.QuotaOk != (createRec.Code != http.StatusConflict) {
				t.Fatalf("preview quotaOk (%v) disagrees with whether the real create actually succeeded (status %d)", previewResponse.QuotaOk, createRec.Code)
			}
		})
	}
}

type stubEnvironmentLifecycle struct {
	stopped []provision.EnvLifecycleInput
	err     error
}

func (s *stubEnvironmentLifecycle) Stop(_ context.Context, in provision.EnvLifecycleInput) error {
	s.stopped = append(s.stopped, in)
	return s.err
}

type stubEnvironmentDeleter struct {
	started []provision.EnvDeleteInput
	err     error
}

func (s *stubEnvironmentDeleter) Start(in provision.EnvDeleteInput) error {
	s.started = append(s.started, in)
	return s.err
}

func postStopEnvironment(t *testing.T, environments *stubEnvironmentRepository, lifecycle EnvironmentLifecycle) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/environments/env-1/stop", nil)
	req.SetPathValue("environment_id", "env-1")
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   "t1",
		TenantType: string(model.TenantTypeCompany),
		ErunUserID: "u1",
	}))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{
		environments: environments,
		contexts:     &stubContextRepository{},
		quotas:       underCapQuota,
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		lifecycle:    lifecycle,
	}.stopEnvironment(rec, req)
	return rec
}

func deleteEnvironmentRequest(t *testing.T, environments *stubEnvironmentRepository, deleter EnvironmentDeleter) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/v1/environments/env-1", nil)
	req.SetPathValue("environment_id", "env-1")
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   "t1",
		TenantType: string(model.TenantTypeCompany),
		ErunUserID: "u1",
	}))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{
		environments: environments,
		contexts:     &stubContextRepository{},
		quotas:       underCapQuota,
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		deleter:      deleter,
	}.deleteEnvironment(rec, req)
	return rec
}

func TestStopEnvironmentRunsLifecycleStop(t *testing.T) {
	lifecycle := &stubEnvironmentLifecycle{}
	rec := postStopEnvironment(t, runtimeEnvironment(), lifecycle)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s), want 200", rec.Code, rec.Body.String())
	}
	if len(lifecycle.stopped) != 1 {
		t.Fatalf("Stop called %d times, want 1", len(lifecycle.stopped))
	}
	got := lifecycle.stopped[0]
	if got.Tenant != "acme" || got.Environment != "prod" || got.EnvironmentID != "env-1" || got.RunningVersion != "1.2.3" {
		t.Fatalf("stop input = %+v", got)
	}
}

func TestStopEnvironmentRejectsNonRuntime(t *testing.T) {
	environments := &stubEnvironmentRepository{environment: model.Environment{
		EnvironmentID: "env-1", Name: "agents", Type: model.EnvironmentTypeRemoteAgent,
	}}
	lifecycle := &stubEnvironmentLifecycle{}
	rec := postStopEnvironment(t, environments, lifecycle)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 Bad Request", rec.Code)
	}
	if len(lifecycle.stopped) != 0 {
		t.Fatal("a non-runtime environment must never reach the stop lifecycle")
	}
}

func TestStopEnvironmentReportsUnconfiguredExecutor(t *testing.T) {
	rec := postStopEnvironment(t, runtimeEnvironment(), nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 Not Implemented", rec.Code)
	}
}

func TestStopEnvironmentSurfacesLifecycleFailure(t *testing.T) {
	lifecycle := &stubEnvironmentLifecycle{err: errForeignKey{}}
	rec := postStopEnvironment(t, runtimeEnvironment(), lifecycle)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 Bad Gateway", rec.Code)
	}
}

// TestDeleteEnvironmentStartsDeleteAsynchronously pins #1140's bounded-delete
// contract: the handler claims the delete (moving the row to `deleting`) and
// starts the durable workflow, then returns 202 immediately rather than
// waiting on the teardown itself — which could run long or wedge entirely.
func TestDeleteEnvironmentStartsDeleteAsynchronously(t *testing.T) {
	environments := runtimeEnvironment()
	deleter := &stubEnvironmentDeleter{}
	rec := deleteEnvironmentRequest(t, environments, deleter)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d (body %s), want 202 Accepted", rec.Code, rec.Body.String())
	}
	if environments.claimDeleteCalls != 1 {
		t.Fatalf("ClaimDelete called %d times, want 1", environments.claimDeleteCalls)
	}
	if len(deleter.started) != 1 {
		t.Fatalf("Start called %d times, want 1", len(deleter.started))
	}

	var response model.Environment
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != model.EnvironmentStatusDeleting {
		t.Fatalf("response status = %q, want %q — `running` must not survive a delete attempt", response.Status, model.EnvironmentStatusDeleting)
	}
}

// TestDeleteEnvironmentResolvesTheDeleteInput checks the resolved
// provision.EnvDeleteInput's fields separately from the response-shape
// assertions above, so each assertion failure names exactly what went wrong.
func TestDeleteEnvironmentResolvesTheDeleteInput(t *testing.T) {
	deleter := &stubEnvironmentDeleter{}
	deleteEnvironmentRequest(t, runtimeEnvironment(), deleter)

	if len(deleter.started) != 1 {
		t.Fatalf("Start called %d times, want 1", len(deleter.started))
	}
	got := deleter.started[0]
	assertEqual(t, "Tenant", got.Tenant, "acme")
	assertEqual(t, "Environment", got.Environment, "prod")
	assertEqual(t, "EnvironmentID", got.EnvironmentID, "env-1")
	assertEqual(t, "RunningVersion", got.RunningVersion, "1.2.3")
	if got.DeleteID == "" {
		t.Fatal("a fresh delete attempt must get its own delete id, or a retry would replay this attempt's cached result")
	}
}

func assertEqual(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %q, want %q", field, got, want)
	}
}

// TestDeleteEnvironmentAllowsNonRuntime: a remote-agent/local-agent row was
// never server-side deployed, so deleting it is a plain row removal — still
// routed through the durable workflow, which skips the Job when
// RunningVersion is empty.
func TestDeleteEnvironmentAllowsNonRuntime(t *testing.T) {
	environments := &stubEnvironmentRepository{environment: model.Environment{
		EnvironmentID: "env-1", Name: "agents", Type: model.EnvironmentTypeRemoteAgent,
	}}
	deleter := &stubEnvironmentDeleter{}
	rec := deleteEnvironmentRequest(t, environments, deleter)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d (body %s), want 202 Accepted", rec.Code, rec.Body.String())
	}
	if len(deleter.started) != 1 {
		t.Fatalf("Start called %d times, want 1", len(deleter.started))
	}
	if deleter.started[0].RunningVersion != "" {
		t.Fatalf("running version = %q, want empty for a never-deployed environment", deleter.started[0].RunningVersion)
	}
}

func TestDeleteEnvironmentReportsUnconfiguredExecutor(t *testing.T) {
	rec := deleteEnvironmentRequest(t, runtimeEnvironment(), nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 Not Implemented", rec.Code)
	}
}

// TestDeleteEnvironmentRejectsConcurrentDelete mirrors
// TestDeployEnvironmentRejectsConcurrentDeploy: a double-submit must not
// start two delete Jobs against the same namespace.
func TestDeleteEnvironmentRejectsConcurrentDelete(t *testing.T) {
	environments := runtimeEnvironment()
	environments.claimDeleteTaken = true
	deleter := &stubEnvironmentDeleter{}
	rec := deleteEnvironmentRequest(t, environments, deleter)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d (body %s), want 409 Conflict", rec.Code, rec.Body.String())
	}
	if len(deleter.started) != 0 {
		t.Fatal("a delete already in progress must not start a second attempt")
	}
}

// The three tests below cover the refusal *messages* for #1163. The guard that
// produces the refusal lives in ClaimDeploy/ClaimDelete's SQL, which this
// package's stub repository cannot exercise and which no Postgres-backed
// harness exists for here — that half is verified against a live control plane
// instead. What these hold is the half that is testable in Go: that a refusal
// caused by an outstanding teardown is reported as such, rather than as the
// misleading "a deploy is already in progress" a caller used to get.

// TestDeployEnvironmentRefusalNamesAnOutstandingDelete: a deploy refused
// because the environment is mid-teardown must say so. The old message sent
// the caller looking for a concurrent deploy that does not exist.
func TestDeployEnvironmentRefusalNamesAnOutstandingDelete(t *testing.T) {
	for _, tc := range []struct {
		status model.EnvironmentStatus
		want   string
	}{
		{model.EnvironmentStatusDeleting, "being deleted"},
		{model.EnvironmentStatusDeletionBlocked, "delete is blocked"},
	} {
		environments := runtimeEnvironment()
		environments.environment.Status = tc.status
		environments.claimTaken = true
		prov := &stubEnvironmentProvisioner{}
		rec := postDeployEnvironment(t, environments, prov, "")

		if rec.Code != http.StatusConflict {
			t.Fatalf("status %q: code = %d, want 409 Conflict", tc.status, rec.Code)
		}
		if body := rec.Body.String(); !strings.Contains(body, tc.want) {
			t.Fatalf("status %q: body %s does not mention %q", tc.status, body, tc.want)
		}
		if len(prov.deployed) != 0 {
			t.Fatalf("status %q: StartDeploy ran %d times, want 0", tc.status, len(prov.deployed))
		}
	}
}

// TestDeleteEnvironmentRefusalNamesAnInFlightDeploy is the mirror: a delete
// refused because a deploy holds the row should point at the deploy, since
// waiting for it is the actionable step.
func TestDeleteEnvironmentRefusalNamesAnInFlightDeploy(t *testing.T) {
	environments := runtimeEnvironment()
	environments.environment.Status = model.EnvironmentStatusProvisioning
	environments.claimDeleteTaken = true
	deleter := &stubEnvironmentDeleter{}
	rec := deleteEnvironmentRequest(t, environments, deleter)

	if rec.Code != http.StatusConflict {
		t.Fatalf("code = %d, want 409 Conflict", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "a deploy is in progress") {
		t.Fatalf("body %s does not name the in-flight deploy", body)
	}
	if len(deleter.started) != 0 {
		t.Fatalf("Start ran %d times, want 0", len(deleter.started))
	}
}

// TestClaimRefusalMessages pins each mapping directly, including that an
// ordinary status still yields the original concurrent-lifecycle wording — the
// teardown cases must not swallow the common one.
func TestClaimRefusalMessages(t *testing.T) {
	if got := deployClaimRefusal(model.EnvironmentStatusRunning); got != "a deploy is already in progress for this environment" {
		t.Fatalf("running deploy refusal = %q, want the concurrent-deploy message", got)
	}
	if got := deployClaimRefusal(model.EnvironmentStatusDeleting); !strings.Contains(got, "being deleted") {
		t.Fatalf("deleting deploy refusal = %q", got)
	}
	if got := deployClaimRefusal(model.EnvironmentStatusDeletionBlocked); !strings.Contains(got, "delete is blocked") {
		t.Fatalf("deletion-blocked deploy refusal = %q", got)
	}
	if got := deleteClaimRefusal(model.EnvironmentStatusRunning); got != "a delete is already in progress for this environment" {
		t.Fatalf("running delete refusal = %q, want the concurrent-delete message", got)
	}
	if got := deleteClaimRefusal(model.EnvironmentStatusProvisioning); !strings.Contains(got, "a deploy is in progress") {
		t.Fatalf("provisioning delete refusal = %q", got)
	}
}

// TestDeleteEnvironmentStartFailureMarksBlocked mirrors
// TestDeployEnvironmentStartFailureMarksFailed: ClaimDelete already moved the
// row to `deleting` before Start ran, so a failure to even enqueue the
// durable workflow must not strand it there — it moves to deletion-blocked.
func TestDeleteEnvironmentStartFailureMarksBlocked(t *testing.T) {
	environments := runtimeEnvironment()
	deleter := &stubEnvironmentDeleter{err: errForeignKey{}}
	rec := deleteEnvironmentRequest(t, environments, deleter)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d (body %s), want 500", rec.Code, rec.Body.String())
	}
	if environments.markDeleteBlockedCalls != 1 {
		t.Fatalf("MarkDeleteBlocked called %d times, want 1", environments.markDeleteBlockedCalls)
	}
	if environments.markDeleteBlockedReason == "" {
		t.Fatal("MarkDeleteBlocked must record why the workflow could not be started")
	}
}
