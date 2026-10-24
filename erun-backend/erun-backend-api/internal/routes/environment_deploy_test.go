package routes

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// The redeployed environment is already counted, unlike create's +1.
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
			maxEnvironments: 10, maxCPUMillicores: minRuntimeCPUMillicores,
			maxMemoryMB: minRuntimeMemoryMB, maxStorageGB: minRuntimeStorageGB,
			maxTotalCPUMillicores: minRuntimeCPUMillicores / 2,
			maxTotalMemoryMB:      minRuntimeMemoryMB, maxTotalStorageGB: minRuntimeStorageGB,
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

// Must refuse before claiming, not surface as a rollout timeout.
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

func TestDeployEnvironmentClaimsBeforeStarting(t *testing.T) {
	environments := runtimeEnvironment()
	postDeployEnvironment(t, environments, &stubEnvironmentProvisioner{}, "")
	if environments.claimCalls != 1 {
		t.Fatalf("ClaimDeploy called %d times, want 1", environments.claimCalls)
	}
}

// ClaimDeploy already moved the row to provisioning, so a failed enqueue must mark it failed.
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
