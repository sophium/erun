package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type stubTenantQuotaWriter struct {
	setCalls  int
	gotTenant string
	gotQuota  model.TenantQuota
}

func (r *stubTenantQuotaWriter) Set(_ context.Context, tenantID string, quota model.TenantQuota) (model.TenantQuota, error) {
	r.setCalls++
	r.gotTenant = tenantID
	r.gotQuota = quota
	quota.TenantID = tenantID
	return quota, nil
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

const fullQuotaBody = `{"maxEnvironments":50,"maxCpuMillicores":4000,"maxMemoryMb":9216,"maxStorageGb":80,` +
	`"maxTotalCpuMillicores":40000,"maxTotalMemoryMb":92160,"maxTotalStorageGb":800}`

func TestSetTenantQuotaForbidsNonOperations(t *testing.T) {
	for _, tt := range []string{string(model.TenantTypeCompany), ""} {
		quotas := &stubTenantQuotaWriter{}
		rec := putQuota(t, quotas, tt, "tenant-x", fullQuotaBody)
		if rec.Code != http.StatusForbidden || quotas.setCalls != 0 {
			t.Fatalf("type=%q: status=%d setCalls=%d, want 403 / 0 calls", tt, rec.Code, quotas.setCalls)
		}
	}
}

func TestSetTenantQuotaOperationsPersists(t *testing.T) {
	quotas := &stubTenantQuotaWriter{}
	rec := putQuota(t, quotas, string(model.TenantTypeOperations), "tenant-x", fullQuotaBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	want := model.TenantQuota{
		MaxEnvironments: 50, MaxCPUMillicores: 4000, MaxMemoryMB: 9216, MaxStorageGB: 80,
		MaxTotalCPUMillicores: 40000, MaxTotalMemoryMB: 92160, MaxTotalStorageGB: 800,
	}
	if quotas.setCalls != 1 || quotas.gotTenant != "tenant-x" || quotas.gotQuota != want {
		t.Fatalf("Set called %d times tenant=%q quota=%+v, want 1 / tenant-x / %+v", quotas.setCalls, quotas.gotTenant, quotas.gotQuota, want)
	}
}

func TestSetTenantQuotaRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"negative env cap":       `{"maxEnvironments":-1,"maxCpuMillicores":4000,"maxMemoryMb":9216,"maxStorageGb":80,"maxTotalCpuMillicores":40000,"maxTotalMemoryMb":92160,"maxTotalStorageGb":800}`,
		"zero cpu cap":           `{"maxEnvironments":50,"maxCpuMillicores":0,"maxMemoryMb":9216,"maxStorageGb":80,"maxTotalCpuMillicores":40000,"maxTotalMemoryMb":92160,"maxTotalStorageGb":800}`,
		"omitted cpu cap":        `{"maxEnvironments":50,"maxMemoryMb":9216,"maxStorageGb":80,"maxTotalCpuMillicores":40000,"maxTotalMemoryMb":92160,"maxTotalStorageGb":800}`,
		"zero memory cap":        `{"maxEnvironments":50,"maxCpuMillicores":4000,"maxMemoryMb":0,"maxStorageGb":80,"maxTotalCpuMillicores":40000,"maxTotalMemoryMb":92160,"maxTotalStorageGb":800}`,
		"zero storage cap":       `{"maxEnvironments":50,"maxCpuMillicores":4000,"maxMemoryMb":9216,"maxStorageGb":0,"maxTotalCpuMillicores":40000,"maxTotalMemoryMb":92160,"maxTotalStorageGb":800}`,
		"zero total cpu cap":     `{"maxEnvironments":50,"maxCpuMillicores":4000,"maxMemoryMb":9216,"maxStorageGb":80,"maxTotalCpuMillicores":0,"maxTotalMemoryMb":92160,"maxTotalStorageGb":800}`,
		"omitted total cpu cap":  `{"maxEnvironments":50,"maxCpuMillicores":4000,"maxMemoryMb":9216,"maxStorageGb":80,"maxTotalMemoryMb":92160,"maxTotalStorageGb":800}`,
		"zero total memory cap":  `{"maxEnvironments":50,"maxCpuMillicores":4000,"maxMemoryMb":9216,"maxStorageGb":80,"maxTotalCpuMillicores":40000,"maxTotalMemoryMb":0,"maxTotalStorageGb":800}`,
		"zero total storage cap": `{"maxEnvironments":50,"maxCpuMillicores":4000,"maxMemoryMb":9216,"maxStorageGb":80,"maxTotalCpuMillicores":40000,"maxTotalMemoryMb":92160,"maxTotalStorageGb":0}`,
		"malformed json":         `{`,
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

type stubTenantQuotaReader struct {
	quota model.TenantQuota
	err   error
}

func (r stubTenantQuotaReader) Get(context.Context) (model.TenantQuota, error) {
	return r.quota, r.err
}

// TestGetQuotaReturnsTheCallersOwnRow: an Operator can see their own tenant's
// quota — the same read admission itself uses — without an operations-scoped
// token (#605, #1113).
func TestGetQuotaReturnsTheCallersOwnRow(t *testing.T) {
	want := model.TenantQuota{
		MaxEnvironments: 10, MaxCPUMillicores: 8000, MaxMemoryMB: 17832, MaxStorageGB: 72,
		MaxTotalCPUMillicores: 80000, MaxTotalMemoryMB: 178320, MaxTotalStorageGB: 720,
	}
	reader := stubTenantQuotaReader{quota: want}
	req := httptest.NewRequest(http.MethodGet, "/v1/quota", nil)
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   "tenant-x",
		TenantType: string(model.TenantTypeCompany),
		ErunUserID: "user-1",
	}))
	rec := httptest.NewRecorder()
	TenantQuotaRoutes{reader: reader}.getQuota(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got model.TenantQuota
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got != want {
		t.Fatalf("quota = %+v, want %+v", got, want)
	}
}
