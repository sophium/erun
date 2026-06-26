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

// acmeTenant is the caller's resolved tenant for provision tests; its Name is
// what forms the <tenant>-<env> namespace and runtime release name.
var acmeTenant = stubConfigTenantRepository{tenant: model.Tenant{TenantID: "tenant-1", Name: "acme", Type: model.TenantTypeCompany}}

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
			// Provision is preview-only: no env row is ever created, even on the
			// happy path, so it must never run on the invalid-input path either.
			if environments.createCalls != 0 {
				t.Fatalf("provision must never call Create, got %d calls", environments.createCalls)
			}
		})
	}
}

// TestProvisionWithNewClusterComposesFullPlan proves the ordered plan for a NEW
// cluster: it pulls the quota line, the real InitCloudContext dry-run argv (the
// ec2 run-instances command), the <tenant>-<env> namespace line, the register
// line, and the deploy line — all without persisting anything.
func TestProvisionWithNewClusterComposesFullPlan(t *testing.T) {
	environments := &stubEnvironmentRepository{count: 2}
	rec := postProvision(t, acmeTenant, environments, stubTenantQuotaRepository{maxEnvironments: 10}, `{
		"environment": {"name": "prod", "type": "runtime"},
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

	var response provisionResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.QuotaOk {
		t.Fatalf("expected quotaOk=true under the cap, got false: %v", response.Plan)
	}

	// authz/tenant line, resolved from the token.
	if !planContains(response.Plan, "provision: tenant acme (resolved from token)") {
		t.Fatalf("plan missing the authz/tenant line: %v", response.Plan)
	}
	// quota line: 2 of 10, within quota.
	if !planContains(response.Plan, "quota: tenant has 2 of 10 environments — within quota") {
		t.Fatalf("plan missing the within-quota line: %v", response.Plan)
	}
	// context bootstrap header + the real InitCloudContext dry-run argv.
	if !planContains(response.Plan, "context: bootstrap cluster acme-prod via alias acme-aws") {
		t.Fatalf("plan missing the context bootstrap header: %v", response.Plan)
	}
	if !planContains(response.Plan, "ec2 run-instances") {
		t.Fatalf("plan missing the EC2 run-instances step from the InitCloudContext dry-run: %v", response.Plan)
	}
	// namespace: <tenant>-<env>.
	if !planContains(response.Plan, "namespace: would create acme-prod") {
		t.Fatalf("plan missing the <tenant>-<env> namespace line: %v", response.Plan)
	}
	// register line, referencing the new cluster's context.
	if !planContains(response.Plan, "register: would persist environment prod (runtime) in tenant acme referencing context acme-prod") {
		t.Fatalf("plan missing the register line: %v", response.Plan)
	}
	// deploy line: the runtime chart at the tenant's release name into the namespace.
	if !planContains(response.Plan, "deploy: would helm install the erun-devops runtime chart (release acme-devops) into acme-prod") {
		t.Fatalf("plan missing the deploy line: %v", response.Plan)
	}
}

// TestProvisionReusesExistingContext proves the existing-context path: no
// bootstrap argv, just the reuse line plus the rest of the plan.
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

	var response provisionResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !planContains(response.Plan, "context: reuse existing kubernetes context acme-prod") {
		t.Fatalf("plan missing the reuse-context line: %v", response.Plan)
	}
	if planContains(response.Plan, "ec2 run-instances") {
		t.Fatalf("existing-context provision must not emit bootstrap argv: %v", response.Plan)
	}
	if !planContains(response.Plan, "namespace: would create acme-staging") {
		t.Fatalf("plan missing the namespace line: %v", response.Plan)
	}
	if !planContains(response.Plan, "register: would persist environment staging (remote-agent) in tenant acme referencing context acme-prod") {
		t.Fatalf("plan missing the register line: %v", response.Plan)
	}
}

// TestProvisionOverCapReturnsPlanWithQuotaBlocked proves that at/over the cap
// the full plan is still returned (it is a preview, not a 4xx), but quotaOk is
// false and the quota line names the block. No Create runs.
func TestProvisionOverCapReturnsPlanWithQuotaBlocked(t *testing.T) {
	environments := &stubEnvironmentRepository{count: 10}
	rec := postProvision(t, acmeTenant, environments, stubTenantQuotaRepository{maxEnvironments: 10}, `{
		"environment": {"name": "prod", "type": "runtime"},
		"kubernetesContext": "acme-prod"
	}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("over-cap provision is still a 200 preview, got status %d", rec.Code)
	}
	if environments.createCalls != 0 {
		t.Fatalf("provision must never call Create, got %d calls", environments.createCalls)
	}

	var response provisionResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.QuotaOk {
		t.Fatalf("expected quotaOk=false at the cap, got true: %v", response.Plan)
	}
	if !planContains(response.Plan, "quota: tenant has 10 of 10 environments — WOULD EXCEED, provisioning blocked") {
		t.Fatalf("plan missing the blocked-quota line: %v", response.Plan)
	}
	// Even when blocked, the rest of the plan is shown so the operator sees the
	// full intended work alongside the blocking reason.
	if !planContains(response.Plan, "deploy: would helm install the erun-devops runtime chart (release acme-devops) into acme-prod") {
		t.Fatalf("blocked plan should still show the full intended actions: %v", response.Plan)
	}
}
