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

// postCreateEnvironment posts through the route with a default company
// security context — every protected route assumes one exists (it does in
// production, stamped by AuthMiddleware before route code ever runs), and
// resolveTargetTenant/scopedContextForTenant need it to decide
// whether a request is scoped to another tenant.
func postCreateEnvironment(t *testing.T, environments *stubEnvironmentRepository, quotas stubTenantQuotaRepository, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/environments", bytes.NewBufferString(body))
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   "t1",
		TenantType: string(model.TenantTypeCompany),
		ErunUserID: "u1",
	}))
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

// stubEnvironmentAdmin fakes EnvironmentAdminCreator, capturing the tenant
// each of scopedCtx/homeCtx carried so a test can prove a cross-tenant create
// ran against the target tenant while still attributing the audit trail to
// the caller's real home tenant, without a database's RLS to observe either.
type stubEnvironmentAdmin struct {
	created        model.Environment
	err            error
	calls          int
	scopedTenantID string
	homeTenantID   string
	targetTenantID string
	environment    model.Environment
}

func (s *stubEnvironmentAdmin) CreateForTenant(scopedCtx, homeCtx context.Context, targetTenantID string, environment model.Environment) (model.Environment, error) {
	s.calls++
	s.targetTenantID = targetTenantID
	s.environment = environment
	if securityContext, ok := security.FromContext(scopedCtx); ok {
		s.scopedTenantID = securityContext.TenantID
	}
	if securityContext, ok := security.FromContext(homeCtx); ok {
		s.homeTenantID = securityContext.TenantID
	}
	if s.err != nil {
		return model.Environment{}, s.err
	}
	created := s.created
	if created.EnvironmentID == "" {
		created = environment
		created.EnvironmentID = "env-admin-created"
	}
	return created, nil
}

// postCreateEnvironmentAs posts through a fully-wired route with an explicit
// tenant type/ID, the shape postCreateEnvironmentWired always fixed to
// COMPANY — needed to exercise the operations-only cross-tenant path.
func postCreateEnvironmentAs(t *testing.T, tenantType model.TenantType, tenantID string, environments *stubEnvironmentRepository, admin *stubEnvironmentAdmin, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/environments", bytes.NewBufferString(body))
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   tenantID,
		TenantType: string(tenantType),
		ErunUserID: "u1",
	}))
	rec := httptest.NewRecorder()
	var adminInterface EnvironmentAdminCreator
	if admin != nil {
		adminInterface = admin
	}
	EnvironmentRoutes{
		environments: environments,
		contexts:     &stubContextRepository{},
		quotas:       underCapQuota,
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		admin:        adminInterface,
	}.createEnvironment(rec, req)
	return rec
}

func TestCreateEnvironmentRejectsCrossTenantScopeForNonOperationsCaller(t *testing.T) {
	environments := &stubEnvironmentRepository{}
	admin := &stubEnvironmentAdmin{}
	rec := postCreateEnvironmentAs(t, model.TenantTypeCompany, "t1", environments, admin,
		`{"name":"prod","type":"remote-agent","tenantId":"other-tenant"}`)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("unexpected status: %d, body: %s", rec.Code, rec.Body.String())
	}
	if environments.createCalls != 0 {
		t.Fatalf("Create should not run for a refused cross-tenant request, got %d calls", environments.createCalls)
	}
	if admin.calls != 0 {
		t.Fatalf("EnvironmentAdminCreator should not run for a refused cross-tenant request, got %d calls", admin.calls)
	}
}

func TestCreateEnvironmentUsesAdminForOperationsCallerWithTenantScope(t *testing.T) {
	environments := &stubEnvironmentRepository{}
	admin := &stubEnvironmentAdmin{}
	rec := postCreateEnvironmentAs(t, model.TenantTypeOperations, "ops-tenant", environments, admin,
		`{"name":"prod","type":"remote-agent","tenantId":"target-tenant"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("unexpected status: %d, body: %s", rec.Code, rec.Body.String())
	}
	if environments.createCalls != 0 {
		t.Fatalf("the plain repository Create should not run for a cross-tenant create, got %d calls", environments.createCalls)
	}
	if admin.calls != 1 {
		t.Fatalf("expected exactly one EnvironmentAdminCreator.CreateForTenant call, got %d", admin.calls)
	}
	if admin.targetTenantID != "target-tenant" {
		t.Fatalf("targetTenantID = %q, want target-tenant", admin.targetTenantID)
	}
	if admin.scopedTenantID != "target-tenant" {
		t.Fatalf("scopedCtx TenantID = %q, want target-tenant (the write scope)", admin.scopedTenantID)
	}
	if admin.homeTenantID != "ops-tenant" {
		t.Fatalf("homeCtx TenantID = %q, want ops-tenant (the operator's real home tenant)", admin.homeTenantID)
	}

	var response model.Environment
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.EnvironmentID != "env-admin-created" {
		t.Fatalf("unexpected persisted environment: %+v", response)
	}
}

func TestCreateEnvironmentIgnoresEmptyTenantScope(t *testing.T) {
	environments := &stubEnvironmentRepository{created: model.Environment{EnvironmentID: "env-1"}}
	admin := &stubEnvironmentAdmin{}
	rec := postCreateEnvironmentAs(t, model.TenantTypeCompany, "t1", environments, admin,
		`{"name":"prod","type":"remote-agent"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("unexpected status: %d, body: %s", rec.Code, rec.Body.String())
	}
	if environments.createCalls != 1 {
		t.Fatalf("expected the plain Create path for an unscoped request, got %d calls", environments.createCalls)
	}
	if admin.calls != 0 {
		t.Fatalf("EnvironmentAdminCreator should not run for an unscoped request, got %d calls", admin.calls)
	}
}

// TestCreateEnvironmentTreatsCallersOwnTenantIDAsANoOp: naming your own
// tenant explicitly is not "cross-tenant" (resolveTargetTenant's own
// definition, shared with users.go/invites.go/tenant_issuers.go) — a company
// caller doing this must not be refused just for being explicit.
func TestCreateEnvironmentTreatsCallersOwnTenantIDAsANoOp(t *testing.T) {
	environments := &stubEnvironmentRepository{created: model.Environment{EnvironmentID: "env-1"}}
	admin := &stubEnvironmentAdmin{}
	rec := postCreateEnvironmentAs(t, model.TenantTypeCompany, "t1", environments, admin,
		`{"name":"prod","type":"remote-agent","tenantId":"t1"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("unexpected status: %d, body: %s", rec.Code, rec.Body.String())
	}
	if environments.createCalls != 1 {
		t.Fatalf("expected the plain Create path for a same-tenant request, got %d calls", environments.createCalls)
	}
	if admin.calls != 0 {
		t.Fatalf("EnvironmentAdminCreator should not run for a same-tenant request, got %d calls", admin.calls)
	}
}

func TestCreateEnvironmentRefusesCrossTenantWhenAdminNotConfigured(t *testing.T) {
	environments := &stubEnvironmentRepository{}
	rec := postCreateEnvironmentAs(t, model.TenantTypeOperations, "ops-tenant", environments, nil,
		`{"name":"prod","type":"remote-agent","tenantId":"target-tenant"}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected status: %d, body: %s", rec.Code, rec.Body.String())
	}
	if environments.createCalls != 0 {
		t.Fatalf("Create should not run when cross-tenant administration is not configured, got %d calls", environments.createCalls)
	}
}

// getEnvironmentsAs issues GET /v1/environments (with an optional ?tenantId)
// under an explicit caller identity.
func getEnvironmentsAs(t *testing.T, tenantType model.TenantType, tenantID string, environments *stubEnvironmentRepository, requestedTenantID string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/v1/environments"
	if requestedTenantID != "" {
		url += "?tenantId=" + requestedTenantID
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   tenantID,
		TenantType: string(tenantType),
		ErunUserID: "u1",
	}))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{environments: environments}.listEnvironments(rec, req)
	return rec
}

func TestListEnvironmentsDefaultsToCallersOwnTenantWithNoTenantIDParam(t *testing.T) {
	environments := &stubEnvironmentRepository{environments: []model.Environment{{EnvironmentID: "env-1"}}}
	rec := getEnvironmentsAs(t, model.TenantTypeCompany, "t1", environments, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body: %s", rec.Code, rec.Body.String())
	}
	if environments.lastListTenantID != "t1" {
		t.Fatalf("List saw TenantID = %q, want t1 (unchanged default behavior)", environments.lastListTenantID)
	}
}

func TestListEnvironmentsRejectsTenantScopeForNonOperationsCaller(t *testing.T) {
	environments := &stubEnvironmentRepository{environments: []model.Environment{{EnvironmentID: "env-1"}}}
	rec := getEnvironmentsAs(t, model.TenantTypeCompany, "t1", environments, "other-tenant")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("unexpected status: %d, body: %s", rec.Code, rec.Body.String())
	}
	if environments.lastListTenantID != "" {
		t.Fatalf("List should not have run for a refused cross-tenant request, saw TenantID = %q", environments.lastListTenantID)
	}
}

// TestListEnvironmentsTreatsCallersOwnTenantIDAsANoOp mirrors
// TestCreateEnvironmentTreatsCallersOwnTenantIDAsANoOp for the read side.
func TestListEnvironmentsTreatsCallersOwnTenantIDAsANoOp(t *testing.T) {
	environments := &stubEnvironmentRepository{environments: []model.Environment{{EnvironmentID: "env-1"}}}
	rec := getEnvironmentsAs(t, model.TenantTypeCompany, "t1", environments, "t1")

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body: %s", rec.Code, rec.Body.String())
	}
	if environments.lastListTenantID != "t1" {
		t.Fatalf("List saw TenantID = %q, want t1", environments.lastListTenantID)
	}
}

func TestListEnvironmentsScopesToRequestedTenantForOperationsCaller(t *testing.T) {
	environments := &stubEnvironmentRepository{}
	rec := getEnvironmentsAs(t, model.TenantTypeOperations, "ops-tenant", environments, "other-tenant")

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body: %s", rec.Code, rec.Body.String())
	}
	if environments.lastListTenantID != "other-tenant" {
		t.Fatalf("List saw TenantID = %q, want other-tenant (the requested scope)", environments.lastListTenantID)
	}
}
