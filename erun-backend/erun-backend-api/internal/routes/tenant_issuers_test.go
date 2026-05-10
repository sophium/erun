package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

type stubTenantIssuerRepository struct {
	list       []model.TenantIssuer
	updated    model.TenantIssuer
	gotIssuer  string
	gotName    string
	updateErr  error
	listCalled bool
}

func (r *stubTenantIssuerRepository) List(context.Context) ([]model.TenantIssuer, error) {
	r.listCalled = true
	return r.list, nil
}

func (r *stubTenantIssuerRepository) UpdateName(_ context.Context, issuer string, name string) (model.TenantIssuer, error) {
	r.gotIssuer = issuer
	r.gotName = name
	return r.updated, r.updateErr
}

func TestTenantIssuerRoutesListTenantIssuers(t *testing.T) {
	repo := &stubTenantIssuerRepository{list: []model.TenantIssuer{{
		TenantID: "tenant-1",
		Issuer:   "https://issuer.example",
		Name:     "AWS production",
	}}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/tenant-issuers", nil)

	TenantIssuerRoutes{issuers: repo}.listTenantIssuers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if !repo.listCalled {
		t.Fatal("expected repository list call")
	}
	var issuers []model.TenantIssuer
	if err := json.NewDecoder(rec.Body).Decode(&issuers); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(issuers) != 1 || issuers[0].Name != "AWS production" || issuers[0].Issuer != "https://issuer.example" {
		t.Fatalf("unexpected issuers: %+v", issuers)
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
