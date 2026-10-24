package routes

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

func postProvision(t *testing.T, tenants ConfigTenantRepository, environments *stubEnvironmentRepository, quotas stubTenantQuotaRepository, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/provision", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	ProvisionRoutes{tenants: tenants, environments: environments, quotas: quotas}.provision(rec, req)
	return rec
}

// acmeTenant's Name, not ID, forms the <tenant>-<env> namespace.
var acmeTenant = stubConfigTenantRepository{tenant: model.Tenant{TenantID: "tenant-1", Name: "acme", Type: model.TenantTypeCompany}}

func decodeProvisionResponse(t *testing.T, rec *httptest.ResponseRecorder) provisionResponse {
	t.Helper()
	var response provisionResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return response
}

func mustPlanLine(t *testing.T, plan []string, want string, why string) {
	t.Helper()
	if !planContains(plan, want) {
		t.Fatalf("%s: %v", why, plan)
	}
}

func mustNotPlanLine(t *testing.T, plan []string, unwanted string, why string) {
	t.Helper()
	if planContains(plan, unwanted) {
		t.Fatalf("%s: %v", why, plan)
	}
}

func TestProvisionRejectsInvalidEnvironment(t *testing.T) {
	cases := map[string]string{
		"missing environment":  `{}`,
		"missing name":         `{"environment":{"type":"runtime"}}`,
		"missing type":         `{"environment":{"name":"prod"}}`,
		"unknown type":         `{"environment":{"name":"prod","type":"staging"}}`,
		"uppercase name":       `{"environment":{"name":"Prod","type":"runtime"}}`,
		"hyphen-bounded name":  `{"environment":{"name":"-prod","type":"runtime"}}`,
		"space in name":        `{"environment":{"name":"my env","type":"runtime"}}`,
		"malformed json":       `{`,
		"context missing name": `{"environment":{"name":"prod","type":"runtime"},"context":{"cloudProviderAlias":"acme-aws","region":"eu-west-2"}}`,
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			environments := &stubEnvironmentRepository{}
			rec := postProvision(t, acmeTenant, environments, underCapQuota, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("unexpected status: %d", rec.Code)
			}
			if environments.createCalls != 0 {
				t.Fatalf("provision must never call Create, got %d calls", environments.createCalls)
			}
		})
	}
}

// remote-agent: a runtime env cannot name a new cluster to bootstrap.
func TestProvisionWithNewClusterComposesFullPlan(t *testing.T) {
	environments := &stubEnvironmentRepository{count: 2}
	rec := postProvision(t, acmeTenant, environments, stubTenantQuotaRepository{maxEnvironments: 10}, `{
		"environment": {"name": "prod", "type": "remote-agent"},
		"context": {
			"name": "acme-prod",
			"cloudProviderAlias": "acme-aws",
			"region": "eu-west-2",
			"instanceType": "c8gd.2xlarge",
			"diskType": "gp3",
			"diskSizeGb": 100
		}
	}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if environments.createCalls != 0 {
		t.Fatalf("provision is preview-only and must not call Create, got %d calls", environments.createCalls)
	}

	response := decodeProvisionResponse(t, rec)
	if !response.QuotaOk {
		t.Fatalf("expected quotaOk=true under the cap, got false: %v", response.Plan)
	}

	mustPlanLine(t, response.Plan, "provision: tenant acme (resolved from token)", "plan missing the authz/tenant line")
	mustPlanLine(t, response.Plan, "quota: tenant has 2 of 10 environments — within quota", "plan missing the within-quota line")
	mustPlanLine(t, response.Plan, "context: bootstrap cluster acme-prod via alias acme-aws", "plan missing the context bootstrap header")
	mustPlanLine(t, response.Plan, "ec2 run-instances", "plan missing the EC2 run-instances step from the InitCloudContext dry-run")
	mustPlanLine(t, response.Plan, "namespace: would create acme-prod", "plan missing the <tenant>-<env> namespace line")
	mustPlanLine(t, response.Plan, "register: would persist environment prod (remote-agent) in tenant acme referencing context acme-prod", "plan missing the register line")
	mustNotPlanLine(t, response.Plan, "deploy:", "a remote-agent environment is never server-side deployed, so the plan must not claim a deploy")
}

// Both paths call resolveDeployPlacement, so a preview never promises a refused placement.
func TestProvisionRejectsCrossClusterPlacementForRuntime(t *testing.T) {
	cases := map[string]string{
		"new cluster": `{
			"environment": {"name": "prod", "type": "runtime"},
			"context": {"name": "acme-prod", "cloudProviderAlias": "acme-aws", "region": "eu-west-2"}
		}`,
		"existing kubernetes context": `{
			"environment": {"name": "prod", "type": "runtime"},
			"kubernetesContext": "acme-prod"
		}`,
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			environments := &stubEnvironmentRepository{}
			rec := postProvision(t, acmeTenant, environments, underCapQuota, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 Bad Request", rec.Code)
			}
		})
	}
}

func TestProvisionRuntimeWithNoContextDescribesSingleClusterPlacement(t *testing.T) {
	environments := &stubEnvironmentRepository{count: 2}
	rec := postProvision(t, acmeTenant, environments, stubTenantQuotaRepository{maxEnvironments: 10}, `{
		"environment": {"name": "prod", "type": "runtime"}
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	response := decodeProvisionResponse(t, rec)
	mustPlanLine(t, response.Plan, "context: deploys into this platform's own cluster (v1 single-cluster placement)", "plan missing the single-cluster placement line")
	mustPlanLine(t, response.Plan, "register: would persist environment prod (runtime) in tenant acme referencing context ", "plan missing the register line")
	mustPlanLine(t, response.Plan, "deploy: would helm install the erun-devops runtime chart (release acme-devops) into acme-prod", "plan missing the deploy line")
	mustPlanLine(t, response.Plan, "auth: would inject this backend's MCP-signing public key", "plan missing the auth-edge wiring line")
	mustPlanLine(t, response.Plan, "expose: would wire mcp.acme-prod.<services zone>", "plan missing the exposure wiring line")
	mustPlanLine(t, response.Plan, "tls: would provision a per-env wildcard certificate", "plan missing the tls provisioning line")
}

func TestProvisionReusesExistingContext(t *testing.T) {
	environments := &stubEnvironmentRepository{count: 0}
	rec := postProvision(t, acmeTenant, environments, stubTenantQuotaRepository{maxEnvironments: 10}, `{
		"environment": {"name": "staging", "type": "remote-agent"},
		"kubernetesContext": "acme-prod"
	}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if environments.createCalls != 0 {
		t.Fatalf("provision is preview-only and must not call Create, got %d calls", environments.createCalls)
	}

	response := decodeProvisionResponse(t, rec)
	mustPlanLine(t, response.Plan, "context: reuse existing kubernetes context acme-prod", "plan missing the reuse-context line")
	mustNotPlanLine(t, response.Plan, "ec2 run-instances", "existing-context provision must not emit bootstrap argv")
	mustPlanLine(t, response.Plan, "namespace: would create acme-staging", "plan missing the namespace line")
	mustPlanLine(t, response.Plan, "register: would persist environment staging (remote-agent) in tenant acme referencing context acme-prod", "plan missing the register line")
	mustNotPlanLine(t, response.Plan, "expose:", "a non-runtime environment is never server-side deployed, so it must plan no exposure")
}

func TestProvisionOverCapReturnsPlanWithQuotaBlocked(t *testing.T) {
	environments := &stubEnvironmentRepository{count: 10}
	rec := postProvision(t, acmeTenant, environments, stubTenantQuotaRepository{maxEnvironments: 10}, `{
		"environment": {"name": "prod", "type": "runtime"}
	}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("over-cap provision is still a 200 preview, got status %d", rec.Code)
	}
	if environments.createCalls != 0 {
		t.Fatalf("provision must never call Create, got %d calls", environments.createCalls)
	}

	response := decodeProvisionResponse(t, rec)
	if response.QuotaOk {
		t.Fatalf("expected quotaOk=false at the cap, got true: %v", response.Plan)
	}
	mustPlanLine(t, response.Plan, "quota: tenant has 10 of 10 environments — WOULD EXCEED, provisioning blocked", "plan missing the blocked-quota line")
	mustPlanLine(t, response.Plan, "deploy: would helm install the erun-devops runtime chart (release acme-devops) into acme-prod", "blocked plan should still show the full intended actions")
}
