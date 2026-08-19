package routes

import (
	"bytes"
	"context"
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

// TestCreateEnvironmentRejectsCrossClusterPlacement: the v1 single-cluster
// placement decision (#605) — the deploy Job has no mechanism to target any
// cluster but the control plane's own, so a runtime environment naming a
// context or kubernetes context must be refused explicitly rather than
// silently accepted and ignored.
func TestCreateEnvironmentRejectsCrossClusterPlacement(t *testing.T) {
	cases := map[string]string{
		"contextId":         `{"name":"prod","type":"runtime","contextId":"ctx-1","runtimeVersion":"1.2.3"}`,
		"kubernetesContext": `{"name":"prod","type":"runtime","kubernetesContext":"primary","runtimeVersion":"1.2.3"}`,
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			environments := &stubEnvironmentRepository{created: model.Environment{EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentTypeRuntime}}
			rec := postCreateEnvironment(t, environments, underCapQuota, body)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d (body %s), want 400 Bad Request", rec.Code, rec.Body.String())
			}
			if environments.createCalls != 0 {
				t.Fatal("a rejected placement must not persist a row that can never deploy")
			}
		})
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

// TestDeployEnvironmentClaimsBeforeStarting: the claim must precede the
// workflow, otherwise the race it exists to prevent is still open.
func TestDeployEnvironmentClaimsBeforeStarting(t *testing.T) {
	environments := runtimeEnvironment()
	postDeployEnvironment(t, environments, &stubEnvironmentProvisioner{}, "")
	if environments.claimCalls != 1 {
		t.Fatalf("ClaimDeploy called %d times, want 1", environments.claimCalls)
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
}

// TestCreateEnvironmentPreviewStillEnforcesPlacement: preview must apply the
// same v1 single-cluster placement check as a real create, not a laxer one.
func TestCreateEnvironmentPreviewStillEnforcesPlacement(t *testing.T) {
	environments := &stubEnvironmentRepository{}
	rec := postCreateEnvironmentPreview(t, environments, underCapQuota, `{
		"name": "prod", "type": "runtime", "contextId": "ctx-1", "preview": true
	}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 Bad Request", rec.Code)
	}
}

type stubEnvironmentLifecycle struct {
	stopped []provision.EnvLifecycleInput
	deleted []provision.EnvLifecycleInput
	err     error
}

func (s *stubEnvironmentLifecycle) Stop(_ context.Context, in provision.EnvLifecycleInput) error {
	s.stopped = append(s.stopped, in)
	return s.err
}

func (s *stubEnvironmentLifecycle) Delete(_ context.Context, in provision.EnvLifecycleInput) error {
	s.deleted = append(s.deleted, in)
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
		quotas:       underCapQuota,
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		lifecycle:    lifecycle,
	}.stopEnvironment(rec, req)
	return rec
}

func deleteEnvironmentRequest(t *testing.T, environments *stubEnvironmentRepository, lifecycle EnvironmentLifecycle) *httptest.ResponseRecorder {
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
		quotas:       underCapQuota,
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		lifecycle:    lifecycle,
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

func TestDeleteEnvironmentRunsLifecycleDelete(t *testing.T) {
	lifecycle := &stubEnvironmentLifecycle{}
	rec := deleteEnvironmentRequest(t, runtimeEnvironment(), lifecycle)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d (body %s), want 204", rec.Code, rec.Body.String())
	}
	if len(lifecycle.deleted) != 1 {
		t.Fatalf("Delete called %d times, want 1", len(lifecycle.deleted))
	}
	got := lifecycle.deleted[0]
	if got.Tenant != "acme" || got.Environment != "prod" || got.EnvironmentID != "env-1" || got.RunningVersion != "1.2.3" {
		t.Fatalf("delete input = %+v", got)
	}
}

// TestDeleteEnvironmentAllowsNonRuntime: a remote-agent/local-agent row was
// never server-side deployed, so deleting it is a plain row removal — still
// routed through the lifecycle, which skips the Job when RunningVersion is
// empty.
func TestDeleteEnvironmentAllowsNonRuntime(t *testing.T) {
	environments := &stubEnvironmentRepository{environment: model.Environment{
		EnvironmentID: "env-1", Name: "agents", Type: model.EnvironmentTypeRemoteAgent,
	}}
	lifecycle := &stubEnvironmentLifecycle{}
	rec := deleteEnvironmentRequest(t, environments, lifecycle)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d (body %s), want 204", rec.Code, rec.Body.String())
	}
	if len(lifecycle.deleted) != 1 {
		t.Fatalf("Delete called %d times, want 1", len(lifecycle.deleted))
	}
	if lifecycle.deleted[0].RunningVersion != "" {
		t.Fatalf("running version = %q, want empty for a never-deployed environment", lifecycle.deleted[0].RunningVersion)
	}
}

func TestDeleteEnvironmentReportsUnconfiguredExecutor(t *testing.T) {
	rec := deleteEnvironmentRequest(t, runtimeEnvironment(), nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 Not Implemented", rec.Code)
	}
}

func TestDeleteEnvironmentSurfacesLifecycleFailure(t *testing.T) {
	lifecycle := &stubEnvironmentLifecycle{err: errForeignKey{}}
	rec := deleteEnvironmentRequest(t, runtimeEnvironment(), lifecycle)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 Bad Gateway", rec.Code)
	}
}
