package routes

import (
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

type stubTenantIssuerRepository struct {
	list          []model.TenantIssuer
	updated       model.TenantIssuer
	gotIssuer     string
	gotName       string
	updateErr     error
	listCalled    bool
	gotListFilter repository.TenantIssuerFilter

	gotOrgFieldKey   string
	gotOrgFieldValue string
	orgScopeCalled   bool
}

func (r *stubTenantIssuerRepository) List(_ context.Context, filter repository.TenantIssuerFilter) ([]model.TenantIssuer, error) {
	r.listCalled = true
	r.gotListFilter = filter
	return r.list, nil
}

func (r *stubTenantIssuerRepository) UpdateName(_ context.Context, issuer string, name string) (model.TenantIssuer, error) {
	r.gotIssuer = issuer
	r.gotName = name
	return r.updated, r.updateErr
}

func (r *stubTenantIssuerRepository) UpdateOrgScope(_ context.Context, issuer, orgFieldKey, orgFieldValue string) (model.TenantIssuer, error) {
	r.orgScopeCalled = true
	r.gotIssuer = issuer
	r.gotOrgFieldKey = orgFieldKey
	r.gotOrgFieldValue = orgFieldValue
	return r.updated, r.updateErr
}

func tenantIssuerRequest(body, tenantType string) *http.Request {
	req := httptest.NewRequest(http.MethodPatch, "/v1/tenant-issuers", strings.NewReader(body))
	return req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   "tenant-1",
		TenantType: tenantType,
	}))
}

func tenantIssuerListRequest(tenantType, queryTenantID string) *http.Request {
	url := "/v1/tenant-issuers"
	if queryTenantID != "" {
		url += "?tenantId=" + queryTenantID
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	return req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   "tenant-1",
		TenantType: tenantType,
	}))
}

func TestTenantIssuerRoutesListTenantIssuers(t *testing.T) {
	repo := &stubTenantIssuerRepository{list: []model.TenantIssuer{{
		TenantID: "tenant-1",
		Issuer:   "https://issuer.example",
		Name:     "AWS production",
	}}}
	rec := httptest.NewRecorder()

	TenantIssuerRoutes{issuers: repo}.listTenantIssuers(rec, tenantIssuerListRequest(string(model.TenantTypeCompany), ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if !repo.listCalled {
		t.Fatal("expected repository list call")
	}
	if repo.gotListFilter.TenantID != "tenant-1" {
		t.Fatalf("expected the caller's own tenant by default, got %q", repo.gotListFilter.TenantID)
	}
	var issuers []model.TenantIssuer
	if err := json.NewDecoder(rec.Body).Decode(&issuers); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(issuers) != 1 || issuers[0].Name != "AWS production" || issuers[0].Issuer != "https://issuer.example" {
		t.Fatalf("unexpected issuers: %+v", issuers)
	}
}

// An operations-scoped caller may read another tenant's issuer mappings —
// the console's enroll-into-another-org flow resolves a target tenant's org
// this way before creating an identity provider account in it.
func TestTenantIssuerRoutesListTenantIssuersOperationsCanReadAnotherTenant(t *testing.T) {
	repo := &stubTenantIssuerRepository{}
	rec := httptest.NewRecorder()

	TenantIssuerRoutes{issuers: repo}.listTenantIssuers(rec, tenantIssuerListRequest(string(model.TenantTypeOperations), "tenant-2"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if repo.gotListFilter.TenantID != "tenant-2" {
		t.Fatalf("expected the requested tenant, got %q", repo.gotListFilter.TenantID)
	}
}

// A non-operations caller naming a tenant other than its own is refused
// before the repository is ever called, mirroring the same
// resolveTargetTenant gate on cross-tenant users/invites reads.
func TestTenantIssuerRoutesListTenantIssuersRefusesCrossTenantForNonOperations(t *testing.T) {
	repo := &stubTenantIssuerRepository{}
	rec := httptest.NewRecorder()

	TenantIssuerRoutes{issuers: repo}.listTenantIssuers(rec, tenantIssuerListRequest(string(model.TenantTypeCompany), "tenant-2"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if repo.listCalled {
		t.Fatal("a non-operations caller naming another tenant must not reach the repository")
	}
}

func TestTenantIssuerRoutesUpdateTenantIssuerName(t *testing.T) {
	repo := &stubTenantIssuerRepository{updated: model.TenantIssuer{
		TenantID: "tenant-1",
		Issuer:   "https://issuer.example",
		Name:     "AWS production",
	}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/tenant-issuers", strings.NewReader(`{"issuer":"https://issuer.example","name":"AWS production"}`))

	TenantIssuerRoutes{issuers: repo}.updateTenantIssuerName(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if repo.gotIssuer != "https://issuer.example" || repo.gotName != "AWS production" {
		t.Fatalf("unexpected update input issuer=%q name=%q", repo.gotIssuer, repo.gotName)
	}
	var issuer model.TenantIssuer
	if err := json.NewDecoder(rec.Body).Decode(&issuer); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if issuer.Name != "AWS production" {
		t.Fatalf("unexpected issuer response: %+v", issuer)
	}
}

// Converting an issuer to org-scoped rewrites the shared issuers row, so it
// changes how every tenant's tokens on that issuer resolve — not just the
// caller's own mapping the way a rename does. It carries the same
// operations-only gate POST /v1/tenants applies to these root resolution
// tables.
func TestTenantIssuerRoutesOrgScopeRequiresOperationsTenant(t *testing.T) {
	repo := &stubTenantIssuerRepository{}
	rec := httptest.NewRecorder()
	body := `{"issuer":"https://issuer.example","orgFieldKey":"urn:zitadel:iam:user:resourceowner:id","orgFieldValue":"123"}`

	TenantIssuerRoutes{issuers: repo}.updateTenantIssuerName(rec, tenantIssuerRequest(body, string(model.TenantTypeCompany)))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a company tenant", rec.Code)
	}
	if repo.orgScopeCalled {
		t.Fatal("a company tenant must not reach the repository")
	}
}

// Either field alone leaves resolution broken: a key with no value orphans the
// issuer's existing tenant, a value with no key is read by nothing.
func TestTenantIssuerRoutesOrgScopeRequiresBothFields(t *testing.T) {
	for _, body := range []string{
		`{"issuer":"https://issuer.example","orgFieldKey":"urn:zitadel:iam:user:resourceowner:id"}`,
		`{"issuer":"https://issuer.example","orgFieldValue":"123"}`,
	} {
		repo := &stubTenantIssuerRepository{}
		rec := httptest.NewRecorder()

		TenantIssuerRoutes{issuers: repo}.updateTenantIssuerName(rec, tenantIssuerRequest(body, string(model.TenantTypeOperations)))

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d for %s, want 400", rec.Code, body)
		}
		if repo.orgScopeCalled {
			t.Fatalf("repository must not be called for %s", body)
		}
	}
}

func TestTenantIssuerRoutesOrgScopeConverts(t *testing.T) {
	repo := &stubTenantIssuerRepository{updated: model.TenantIssuer{
		TenantID:      "tenant-1",
		Issuer:        "https://issuer.example",
		Name:          "Platform IdP",
		OrgFieldKey:   "urn:zitadel:iam:user:resourceowner:id",
		OrgFieldValue: "123",
	}}
	rec := httptest.NewRecorder()
	body := `{"issuer":"https://issuer.example","orgFieldKey":"urn:zitadel:iam:user:resourceowner:id","orgFieldValue":"123"}`

	TenantIssuerRoutes{issuers: repo}.updateTenantIssuerName(rec, tenantIssuerRequest(body, string(model.TenantTypeOperations)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !repo.orgScopeCalled {
		t.Fatal("expected the org-scope conversion, not a rename")
	}
	if repo.gotOrgFieldKey != "urn:zitadel:iam:user:resourceowner:id" || repo.gotOrgFieldValue != "123" {
		t.Fatalf("key=%q value=%q", repo.gotOrgFieldKey, repo.gotOrgFieldValue)
	}
	var issuer model.TenantIssuer
	if err := json.NewDecoder(rec.Body).Decode(&issuer); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if issuer.OrgFieldKey == "" || issuer.OrgFieldValue == "" {
		t.Fatalf("response must report the resulting scope: %+v", issuer)
	}
}

// A rename must keep working untouched — it is tenant-scoped and carries no
// operations gate.
func TestTenantIssuerRoutesRenameStillWorksForCompanyTenant(t *testing.T) {
	repo := &stubTenantIssuerRepository{updated: model.TenantIssuer{Issuer: "https://issuer.example", Name: "Renamed"}}
	rec := httptest.NewRecorder()

	TenantIssuerRoutes{issuers: repo}.updateTenantIssuerName(rec, tenantIssuerRequest(`{"issuer":"https://issuer.example","name":"Renamed"}`, string(model.TenantTypeCompany)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if repo.orgScopeCalled {
		t.Fatal("a rename must not take the org-scope path")
	}
}
