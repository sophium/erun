package routes

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/deploy"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

func postCreateEnvironment(t *testing.T, environments *stubEnvironmentRepository, quotas stubTenantQuotaRepository, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/environments", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{environments: environments, quotas: quotas}.createEnvironment(rec, req)
	return rec
}

// underCapQuota is a quota that always admits another environment, so tests that
// are not exercising the guardrail itself get past the quota check.
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
			// The handler must thread the operator-authored fields into the
			// persisted model; tenant binding is left to RLS, not the body.
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

// TestCreateEnvironmentRejectsAtQuota proves the environment-count guardrail:
// once the tenant is at its cap (count == max), registration is rejected with
// 409 and Create never runs. The input is valid, so only the quota stops it.
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

// TestCreateEnvironmentAllowsUnderQuota proves that with room under the cap
// (count < max) registration proceeds and Create runs exactly once.
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

// errForeignKey stands in for a database foreign-key-violation error returned by
// the repository when the referenced context is not the caller's.
type errForeignKey struct{}

func (errForeignKey) Error() string { return "foreign key violation" }

type stubEnvironmentDeployer struct {
	started []deploy.DeployInput
	err     error
}

func (s *stubEnvironmentDeployer) Start(in deploy.DeployInput) error {
	s.started = append(s.started, in)
	return s.err
}

// postDeployEnvironment drives the deploy action directly against the handler.
// A nil securityContext exercises the pre-auth-context paths (501/400/409); the
// running-context paths need one because the handler threads tenant identity
// into the deploy input.
func postDeployEnvironment(t *testing.T, environments *stubEnvironmentRepository, contexts ContextRepository, deployer EnvironmentDeployer, envID, body string, securityContext *security.Context) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/environments/"+envID+"/deploy", bytes.NewBufferString(body))
	req.SetPathValue("environment_id", envID)
	if securityContext != nil {
		req = req.WithContext(security.WithContext(req.Context(), *securityContext))
	}
	rec := httptest.NewRecorder()
	EnvironmentRoutes{environments: environments, contexts: contexts, deployer: deployer}.deployEnvironment(rec, req)
	return rec
}

func runningContext() *stubContextRepository {
	return &stubContextRepository{cloudContext: model.Context{ContextID: "ctx-1", Name: "primary", Status: "running"}}
}

func deployableEnv() *stubEnvironmentRepository {
	return &stubEnvironmentRepository{
		environment: model.Environment{
			EnvironmentID:  "env-1",
			Name:           "prod",
			ContextID:      "ctx-1",
			RuntimeVersion: "1.2.3",
		},
		// The deploy claim succeeds by default (no in-flight deploy).
		claimDeployResult: true,
	}
}

// TestDeployEnvironmentNotConfigured: with no deployer wired (no DBOS/secrets),
// the deploy action is unavailable and returns 501.
func TestDeployEnvironmentNotConfigured(t *testing.T) {
	rec := postDeployEnvironment(t, deployableEnv(), runningContext(), nil, "env-1", "", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

// TestDeployEnvironmentRequiresVersion: an env with no persisted runtimeVersion
// and no body override cannot be deployed (deploy never mints a version), so the
// request is rejected and the deploy never starts.
func TestDeployEnvironmentRequiresVersion(t *testing.T) {
	environments := &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "prod", ContextID: "ctx-1"}}
	deployer := &stubEnvironmentDeployer{}
	rec := postDeployEnvironment(t, environments, runningContext(), deployer, "env-1", "", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if len(deployer.started) != 0 || len(environments.deployStatuses) != 0 {
		t.Fatalf("deploy must not start without a version: started=%d statuses=%v", len(deployer.started), environments.deployStatuses)
	}
}

// TestDeployEnvironmentRequiresContext: an env with a version but no linked
// context has nothing to deploy into, so the request is rejected.
func TestDeployEnvironmentRequiresContext(t *testing.T) {
	environments := &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "prod", RuntimeVersion: "1.2.3"}}
	deployer := &stubEnvironmentDeployer{}
	rec := postDeployEnvironment(t, environments, runningContext(), deployer, "env-1", "", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if len(deployer.started) != 0 {
		t.Fatalf("deploy must not start without a context")
	}
}

// TestDeployEnvironmentRejectsUnprovisionedContext: deploying into a context
// that is not yet running fails fast with 409, and the env is not flipped to
// deploying.
func TestDeployEnvironmentRejectsUnprovisionedContext(t *testing.T) {
	environments := deployableEnv()
	contexts := &stubContextRepository{cloudContext: model.Context{ContextID: "ctx-1", Status: "provisioning"}}
	deployer := &stubEnvironmentDeployer{}
	rec := postDeployEnvironment(t, environments, contexts, deployer, "env-1", "", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if len(deployer.started) != 0 || len(environments.deployStatuses) != 0 {
		t.Fatalf("deploy must not start against an unprovisioned context")
	}
}

// TestDeployEnvironmentStartsDeploy: a deployable env with a running context
// flips to deploying, starts the durable deploy with the threaded identity +
// version, and returns 202.
func TestDeployEnvironmentStartsDeploy(t *testing.T) {
	environments := deployableEnv()
	deployer := &stubEnvironmentDeployer{}
	rec := postDeployEnvironment(t, environments, runningContext(), deployer, "env-1", "", &security.Context{
		TenantID:   "t1",
		TenantType: string(model.TenantTypeCompany),
		ErunUserID: "u1",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d (body %s), want 202", rec.Code, rec.Body.String())
	}
	if len(deployer.started) != 1 {
		t.Fatalf("Start called %d times, want 1", len(deployer.started))
	}
	if got := deployer.started[0]; got.EnvironmentID != "env-1" || got.TenantID != "t1" || got.Version != "1.2.3" {
		t.Fatalf("deploy input = %+v", got)
	}
	// The deploy is gated on an atomic claim, not an unconditional flip.
	if environments.claimDeployCalls != 1 {
		t.Fatalf("ClaimDeploy called %d times, want 1", environments.claimDeployCalls)
	}
	var response model.Environment
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.DeployStatus != "deploying" {
		t.Fatalf("response deploy status = %q, want deploying", response.DeployStatus)
	}
}

// TestDeployEnvironmentVersionOverride: an explicit body version is threaded to
// the deploy in preference to the env's persisted runtimeVersion.
func TestDeployEnvironmentVersionOverride(t *testing.T) {
	environments := deployableEnv()
	deployer := &stubEnvironmentDeployer{}
	rec := postDeployEnvironment(t, environments, runningContext(), deployer, "env-1", `{"version":"2.0.0"}`, &security.Context{TenantID: "t1"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	if got := deployer.started[0].Version; got != "2.0.0" {
		t.Fatalf("deploy version = %q, want 2.0.0 (body override)", got)
	}
}

// TestDeployEnvironmentRollsBackOnStartFailure: if the durable workflow fails to
// start after the env was claimed, the env is not left stuck in "deploying" — it
// is flipped to "failed".
func TestDeployEnvironmentRollsBackOnStartFailure(t *testing.T) {
	environments := deployableEnv()
	deployer := &stubEnvironmentDeployer{err: errors.New("dbos down")}
	rec := postDeployEnvironment(t, environments, runningContext(), deployer, "env-1", "", &security.Context{TenantID: "t1"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	// The env was claimed (-> deploying via ClaimDeploy); the only UpdateDeployResult
	// is the rollback to failed.
	if len(environments.deployStatuses) != 1 || environments.deployStatuses[0] != "failed" {
		t.Fatalf("deploy statuses = %v, want [failed]", environments.deployStatuses)
	}
}

// TestDeployEnvironmentRejectsConcurrent: when a deploy is already in flight
// (the claim fails), a second request is rejected with 409 and no second
// rollout is started.
func TestDeployEnvironmentRejectsConcurrent(t *testing.T) {
	environments := deployableEnv()
	environments.claimDeployResult = false // a deploy is already in progress
	deployer := &stubEnvironmentDeployer{}
	rec := postDeployEnvironment(t, environments, runningContext(), deployer, "env-1", "", &security.Context{TenantID: "t1"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d (body %s), want 409", rec.Code, rec.Body.String())
	}
	if len(deployer.started) != 0 {
		t.Fatalf("a second concurrent deploy must not start, got %d", len(deployer.started))
	}
}
