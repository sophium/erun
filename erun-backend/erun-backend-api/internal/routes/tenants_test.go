package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type stubTenantRepository struct {
	created      model.Tenant
	createCalls  int
	createParams repository.CreateTenantParams
	list         []model.Tenant
	current      model.Tenant
	currentErr   error
	err          error
}

func (r *stubTenantRepository) Create(_ context.Context, params repository.CreateTenantParams) (model.Tenant, error) {
	r.createCalls++
	r.createParams = params
	if r.err != nil {
		return model.Tenant{}, r.err
	}
	created := r.created
	if created.TenantID == "" {
		created = model.Tenant{TenantID: "tenant-created", Name: params.Name, Type: params.Type}
	}
	return created, nil
}

func (r *stubTenantRepository) List(_ context.Context) ([]model.Tenant, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.list, nil
}

func (r *stubTenantRepository) Reachable(_ context.Context) ([]model.Tenant, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.list, nil
}

func (r *stubTenantRepository) Current(_ context.Context) (model.Tenant, error) {
	if r.currentErr != nil {
		return model.Tenant{}, r.currentErr
	}
	return r.current, nil
}

type stubBootstrapNameReconciler struct {
	calls  int
	result model.Tenant
	err    error
}

func (r *stubBootstrapNameReconciler) ReconcileBootstrapName(_ context.Context, _ model.Tenant) (model.Tenant, error) {
	r.calls++
	return r.result, r.err
}

func postCreateTenant(t *testing.T, tenants *stubTenantRepository, tenantType string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants", bytes.NewBufferString(body))
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   "tenant-caller",
		TenantType: tenantType,
		ErunUserID: "user-1",
	}))
	rec := httptest.NewRecorder()
	TenantRoutes{tenants: tenants}.createTenant(rec, req)
	return rec
}

func TestCreateTenantForbidsNonOperationsCaller(t *testing.T) {
	for _, tenantType := range []string{string(model.TenantTypeCompany), ""} {
		t.Run("type="+tenantType, func(t *testing.T) {
			tenants := &stubTenantRepository{}
			rec := postCreateTenant(t, tenants, tenantType, `{"name":"acme","type":"COMPANY","issuer":"https://idp.example"}`)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("unexpected status: %d", rec.Code)
			}
			if tenants.createCalls != 0 {
				t.Fatalf("non-operations caller must not reach Create, got %d calls", tenants.createCalls)
			}
		})
	}
}

func patchReconcileBootstrapName(t *testing.T, tenants *stubTenantRepository, reconciler *stubBootstrapNameReconciler, tenantType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/v1/tenants/reconcile-bootstrap-name", nil)
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   "tenant-caller",
		TenantType: tenantType,
		ErunUserID: "user-1",
	}))
	rec := httptest.NewRecorder()
	TenantRoutes{tenants: tenants, reconciler: reconciler}.reconcileBootstrapName(rec, req)
	return rec
}

func TestReconcileBootstrapNameForbidsNonOperationsCaller(t *testing.T) {
	for _, tenantType := range []string{string(model.TenantTypeCompany), ""} {
		t.Run("type="+tenantType, func(t *testing.T) {
			tenants := &stubTenantRepository{current: model.Tenant{TenantID: "tenant-caller", Name: "acme"}}
			reconciler := &stubBootstrapNameReconciler{result: model.Tenant{TenantID: "tenant-caller", Name: "frs"}}
			rec := patchReconcileBootstrapName(t, tenants, reconciler, tenantType)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("unexpected status: %d", rec.Code)
			}
			if reconciler.calls != 0 {
				t.Fatalf("non-operations caller must not reach ReconcileBootstrapName, got %d calls", reconciler.calls)
			}
		})
	}
}

func TestReconcileBootstrapNameOperationsCallerReachesReconciler(t *testing.T) {
	tenants := &stubTenantRepository{current: model.Tenant{TenantID: "tenant-caller", Name: "operations"}}
	reconciler := &stubBootstrapNameReconciler{result: model.Tenant{TenantID: "tenant-caller", Name: "frs"}}
	rec := patchReconcileBootstrapName(t, tenants, reconciler, string(model.TenantTypeOperations))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body: %s", rec.Code, rec.Body.String())
	}
	if reconciler.calls != 1 {
		t.Fatalf("expected exactly one ReconcileBootstrapName call, got %d", reconciler.calls)
	}
	var got model.Tenant
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Name != "frs" {
		t.Fatalf("response tenant name = %q, want %q", got.Name, "frs")
	}
}

func TestCreateTenantRejectsInvalidInput(t *testing.T) {
	cases := map[string]string{
		"missing name":    `{"issuer":"https://idp.example"}`,
		"missing issuer":  `{"name":"acme"}`,
		"unknown type":    `{"name":"acme","issuer":"https://idp.example","type":"PARTNER"}`,
		"malformed json":  `{`,
		"hyphenated name": `{"name":"ac-me","issuer":"https://idp.example"}`,
		"uppercase name":  `{"name":"Acme","issuer":"https://idp.example"}`,
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			tenants := &stubTenantRepository{}
			rec := postCreateTenant(t, tenants, string(model.TenantTypeOperations), body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("unexpected status: %d", rec.Code)
			}
			if tenants.createCalls != 0 {
				t.Fatalf("Create should not run on invalid input, got %d calls", tenants.createCalls)
			}
		})
	}
}

func TestCreateTenantOperationsCallerPersists(t *testing.T) {
	tenants := &stubTenantRepository{created: model.Tenant{TenantID: "tenant-1", Name: "acme", Type: model.TenantTypeCompany}}
	rec := postCreateTenant(t, tenants, string(model.TenantTypeOperations), `{
		"name": "acme",
		"type": "COMPANY",
		"issuer": "https://idp.example",
		"orgFieldKey": "org_id",
		"orgFieldValue": "42",
		"displayName": "Acme IdP"
	}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if tenants.createCalls != 1 {
		t.Fatalf("expected exactly one Create call, got %d", tenants.createCalls)
	}
	if tenants.createParams.Issuer != "https://idp.example" || tenants.createParams.OrgFieldValue != "42" {
		t.Fatalf("unexpected create params: %+v", tenants.createParams)
	}

	var response model.Tenant
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.TenantID != "tenant-1" {
		t.Fatalf("unexpected persisted tenant: %+v", response)
	}
}

func TestCreateTenantDuplicateIssuerReturnsConflict(t *testing.T) {
	tenants := &stubTenantRepository{err: repository.ErrConflict}
	rec := postCreateTenant(t, tenants, string(model.TenantTypeOperations), `{
		"name": "acme",
		"type": "COMPANY",
		"issuer": "https://idp.example"
	}`)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("https://idp.example")) {
		t.Fatalf("expected the conflict message to name the issuer, got %s", rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("PATCH /v1/tenant-issuers")) {
		t.Fatalf("expected the conflict message to name the conversion endpoint that actually works, got %s", rec.Body.String())
	}
}

func TestCreateTenantDuplicateOrgScopedIssuerReturnsConflict(t *testing.T) {
	tenants := &stubTenantRepository{err: repository.ErrConflict}
	rec := postCreateTenant(t, tenants, string(model.TenantTypeOperations), `{
		"name": "acme",
		"type": "COMPANY",
		"issuer": "https://idp.example",
		"orgFieldKey": "org_id",
		"orgFieldValue": "42"
	}`)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("42")) {
		t.Fatalf("expected the conflict message to name the org value, got %s", rec.Body.String())
	}
}

func TestListTenants(t *testing.T) {
	want := []model.Tenant{{TenantID: "tenant-1", Name: "acme"}, {TenantID: "tenant-2", Name: "beta"}}
	tenants := &stubTenantRepository{list: want}
	req := httptest.NewRequest(http.MethodGet, "/v1/tenants", nil)
	rec := httptest.NewRecorder()
	TenantRoutes{tenants: tenants}.listTenants(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var response []model.Tenant
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response) != 2 || response[0].TenantID != "tenant-1" || response[1].TenantID != "tenant-2" {
		t.Fatalf("unexpected tenant list: %+v", response)
	}
}

func TestListTenantsSurfacesRepositoryError(t *testing.T) {
	tenants := &stubTenantRepository{err: foreignKeyError{}}
	req := httptest.NewRequest(http.MethodGet, "/v1/tenants", nil)
	rec := httptest.NewRecorder()
	TenantRoutes{tenants: tenants}.listTenants(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// Scoping itself is enforced by TenantRepository.Reachable's SQL.
func TestReachableTenantsHasNoOperationsGate(t *testing.T) {
	want := []model.Tenant{{TenantID: "tenant-1", Name: "acme", Type: model.TenantTypeCompany}}
	tenants := &stubTenantRepository{list: want}
	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/reachable", nil)
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:       "tenant-1",
		TenantType:     string(model.TenantTypeCompany),
		ErunUserID:     "user-1",
		ExternalIssuer: "https://idp.example",
		ExternalUserID: "sub-1",
	}))
	rec := httptest.NewRecorder()
	TenantRoutes{tenants: tenants}.reachableTenants(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var response []model.Tenant
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response) != 1 || response[0].TenantID != "tenant-1" {
		t.Fatalf("unexpected reachable tenants: %+v", response)
	}
}

func TestReachableTenantsSurfacesRepositoryError(t *testing.T) {
	tenants := &stubTenantRepository{err: foreignKeyError{}}
	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/reachable", nil)
	rec := httptest.NewRecorder()
	TenantRoutes{tenants: tenants}.reachableTenants(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestCreateTenantRefusesAnUnresolvableIssuerMapping(t *testing.T) {
	tenants := &stubTenantRepository{err: &repository.UnresolvableIssuerMappingError{
		Issuer:      "https://auth.example",
		Reason:      model.TenantReachabilityNoOrgMapping,
		OrgFieldKey: "urn:zitadel:iam:user:resourceowner:id",
	}}
	rec := postCreateTenant(t, tenants, string(model.TenantTypeOperations),
		`{"name":"probeco","issuer":"https://auth.example"}`)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if envelope.Code != "UNRESOLVABLE_ISSUER_MAPPING" {
		t.Fatalf("code = %q, want UNRESOLVABLE_ISSUER_MAPPING", envelope.Code)
	}
	if !strings.Contains(envelope.Message, "urn:zitadel:iam:user:resourceowner:id") {
		t.Fatalf("message %q does not name the org claim the issuer resolves by", envelope.Message)
	}
}
