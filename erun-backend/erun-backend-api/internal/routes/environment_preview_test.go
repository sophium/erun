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
			maxEnvironments: 10, maxCPUMillicores: minRuntimeCPUMillicores,
			maxMemoryMB: minRuntimeMemoryMB, maxStorageGB: minRuntimeStorageGB,
			maxTotalCPUMillicores: 2 * minRuntimeCPUMillicores,
			maxTotalMemoryMB:      2 * minRuntimeMemoryMB, maxTotalStorageGB: 2 * minRuntimeStorageGB,
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
