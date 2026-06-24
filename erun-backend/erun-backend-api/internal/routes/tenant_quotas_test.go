package routes

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type stubTenantQuotaWriter struct {
	setCalls  int
	gotTenant string
	gotMax    int
}

func (r *stubTenantQuotaWriter) Set(_ context.Context, tenantID string, maxEnvironments int) (model.TenantQuota, error) {
	r.setCalls++
	r.gotTenant = tenantID
	r.gotMax = maxEnvironments
	return model.TenantQuota{TenantID: tenantID, MaxEnvironments: maxEnvironments}, nil
}

func putQuota(t *testing.T, quotas *stubTenantQuotaWriter, tenantType, tenantID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/v1/tenants/"+tenantID+"/quota", bytes.NewBufferString(body))
	req.SetPathValue("tenant_id", tenantID)
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   "ops-caller",
		TenantType: tenantType,
		ErunUserID: "user-1",
	}))
	rec := httptest.NewRecorder()
	TenantQuotaRoutes{quotas: quotas}.setQuota(rec, req)
	return rec
}

func TestSetTenantQuotaForbidsNonOperations(t *testing.T) {
	for _, tt := range []string{string(model.TenantTypeCompany), ""} {
		quotas := &stubTenantQuotaWriter{}
		rec := putQuota(t, quotas, tt, "tenant-x", `{"maxEnvironments":50}`)
		if rec.Code != http.StatusForbidden || quotas.setCalls != 0 {
			t.Fatalf("type=%q: status=%d setCalls=%d, want 403 / 0 calls", tt, rec.Code, quotas.setCalls)
		}
	}
}

func TestSetTenantQuotaOperationsPersists(t *testing.T) {
	quotas := &stubTenantQuotaWriter{}
	rec := putQuota(t, quotas, string(model.TenantTypeOperations), "tenant-x", `{"maxEnvironments":50}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if quotas.setCalls != 1 || quotas.gotTenant != "tenant-x" || quotas.gotMax != 50 {
		t.Fatalf("Set called %d times tenant=%q max=%d, want 1 / tenant-x / 50", quotas.setCalls, quotas.gotTenant, quotas.gotMax)
	}
}

func TestSetTenantQuotaRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"negative cap":   `{"maxEnvironments":-1}`,
		"malformed json": `{`,
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			quotas := &stubTenantQuotaWriter{}
			rec := putQuota(t, quotas, string(model.TenantTypeOperations), "tenant-x", body)
			if rec.Code != http.StatusBadRequest || quotas.setCalls != 0 {
				t.Fatalf("status=%d setCalls=%d, want 400 / 0 calls", rec.Code, quotas.setCalls)
			}
		})
	}
}
