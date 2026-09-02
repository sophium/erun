package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

type stubHostnameWriter struct {
	upsertFQDN, upsertValue string
	upsertErr               error
	deleteFQDN              string
	deleteErr               error
}

func (w *stubHostnameWriter) UpsertA(fqdn, value string) error {
	w.upsertFQDN, w.upsertValue = fqdn, value
	return w.upsertErr
}

func (w *stubHostnameWriter) DeleteA(fqdn string) error {
	w.deleteFQDN = fqdn
	return w.deleteErr
}

func setHostnameRequest(environmentID, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPut, "/v1/environments/"+environmentID+"/hostname", strings.NewReader(body))
	req.SetPathValue("environment_id", environmentID)
	return req
}

func TestSetEnvironmentHostnameUpsertsWildcardRecord(t *testing.T) {
	writer := &stubHostnameWriter{}
	routes := EnvironmentHostnameRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "dev"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "team"}},
		writer:       writer,
		servicesZone: "services.example.com",
	}
	rec := httptest.NewRecorder()
	routes.setHostname(rec, setHostnameRequest("env-1", `{"targetIp":"127.0.0.1"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if writer.upsertFQDN != "*.team-dev.services.example.com" || writer.upsertValue != "127.0.0.1" {
		t.Fatalf("upsert = (%q,%q), want (*.team-dev.services.example.com,127.0.0.1)", writer.upsertFQDN, writer.upsertValue)
	}
	var response environmentHostnameResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Hostname != "*.team-dev.services.example.com" || response.TargetIP != "127.0.0.1" {
		t.Fatalf("response = %+v", response)
	}
}

func TestSetEnvironmentHostnameAllowsPrivateAndLoopback(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.5", "192.168.1.1", "::1"} {
		writer := &stubHostnameWriter{}
		routes := EnvironmentHostnameRoutes{
			environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "dev"}},
			tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "team"}},
			writer:       writer,
			servicesZone: "services.example.com",
		}
		rec := httptest.NewRecorder()
		routes.setHostname(rec, setHostnameRequest("env-1", `{"targetIp":"`+ip+`"}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("ip %q: status = %d, want 200 (a private/loopback target is allowed on purpose); body=%s", ip, rec.Code, rec.Body.String())
		}
	}
}

func TestSetEnvironmentHostnameRejectsNonIP(t *testing.T) {
	writer := &stubHostnameWriter{}
	routes := EnvironmentHostnameRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "dev"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "team"}},
		writer:       writer,
		servicesZone: "services.example.com",
	}
	rec := httptest.NewRecorder()
	routes.setHostname(rec, setHostnameRequest("env-1", `{"targetIp":"not-an-ip"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if writer.upsertFQDN != "" {
		t.Fatal("writer must not be called for an invalid target")
	}
}

func TestSetEnvironmentHostnameNotConfigured(t *testing.T) {
	routes := EnvironmentHostnameRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "dev"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "team"}},
		writer:       nil,
		servicesZone: "services.example.com",
	}
	rec := httptest.NewRecorder()
	routes.setHostname(rec, setHostnameRequest("env-1", `{"targetIp":"127.0.0.1"}`))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestSetEnvironmentHostnameUnknownEnvironment(t *testing.T) {
	routes := EnvironmentHostnameRoutes{
		environments: &stubEnvironmentRepository{err: repository.ErrNotFound},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "team"}},
		writer:       &stubHostnameWriter{},
		servicesZone: "services.example.com",
	}
	rec := httptest.NewRecorder()
	routes.setHostname(rec, setHostnameRequest("env-1", `{"targetIp":"127.0.0.1"}`))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 -- environments.Get is what scopes this write to the caller's own tenant", rec.Code)
	}
}

func TestDeleteEnvironmentHostnameRemovesWildcardRecord(t *testing.T) {
	writer := &stubHostnameWriter{}
	routes := EnvironmentHostnameRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "dev"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "team"}},
		writer:       writer,
		servicesZone: "services.example.com",
	}
	req := httptest.NewRequest(http.MethodDelete, "/v1/environments/env-1/hostname", nil)
	req.SetPathValue("environment_id", "env-1")
	rec := httptest.NewRecorder()
	routes.deleteHostname(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if writer.deleteFQDN != "*.team-dev.services.example.com" {
		t.Fatalf("delete fqdn = %q, want *.team-dev.services.example.com", writer.deleteFQDN)
	}
}

func TestDeleteEnvironmentHostnameNotConfigured(t *testing.T) {
	routes := EnvironmentHostnameRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "dev"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "team"}},
		writer:       nil,
		servicesZone: "services.example.com",
	}
	req := httptest.NewRequest(http.MethodDelete, "/v1/environments/env-1/hostname", nil)
	req.SetPathValue("environment_id", "env-1")
	rec := httptest.NewRecorder()
	routes.deleteHostname(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}
