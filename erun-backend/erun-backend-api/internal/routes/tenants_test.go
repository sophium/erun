package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func postCreateTenant(t *testing.T, tenants *stubTenantRepository, tenantType string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants", bytes.NewBufferString(body))
	// Authentication middleware stamps the resolved security context before route
	// code runs; the OPERATIONS gate reads TenantType from it.
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
	// The handler must thread the issuer mapping through to the repository.
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
	tenants := &stubTenantRepository{err: errForeignKey{}}
	req := httptest.NewRequest(http.MethodGet, "/v1/tenants", nil)
	rec := httptest.NewRecorder()
	TenantRoutes{tenants: tenants}.listTenants(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
