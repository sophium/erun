package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/mcptoken"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	eruncommon "github.com/sophium/erun/erun-common"
)

func dns01Signer(t *testing.T) *mcptoken.Signer {
	t.Helper()
	privatePEM, _, err := eruncommon.GenerateDesktopIdentity()
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	signer, err := mcptoken.NewSigner(privatePEM)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	return signer
}

func mintDNS01(routes DNS01TokenRoutes) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/environments/env-1/dns01-token", nil)
	req.SetPathValue("environment_id", "env-1")
	rec := httptest.NewRecorder()
	routes.mintDNS01Token(rec, req)
	return rec
}

func TestMintDNS01TokenReturnsPerEnvToken(t *testing.T) {
	signer := dns01Signer(t)
	routes := DNS01TokenRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "prod"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       signer,
	}
	rec := mintDNS01(routes)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var response dns01TokenResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if want := "erun-dns01:acme/prod"; response.Audience != want {
		t.Fatalf("audience = %q, want %q", response.Audience, want)
	}
	// The minted token must self-verify back to the same (tenant, env).
	tenant, environment, err := signer.VerifyDNS01(response.Token, time.Now())
	if err != nil {
		t.Fatalf("verify minted token: %v", err)
	}
	if tenant != "acme" || environment != "prod" {
		t.Fatalf("verify = (%q,%q), want (acme,prod)", tenant, environment)
	}
}

func TestMintDNS01TokenNotConfigured(t *testing.T) {
	routes := DNS01TokenRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{Name: "prod"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       nil,
	}
	if rec := mintDNS01(routes); rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestMintDNS01TokenUnknownEnvironment(t *testing.T) {
	routes := DNS01TokenRoutes{
		environments: &stubEnvironmentRepository{err: repository.ErrNotFound},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       dns01Signer(t),
	}
	if rec := mintDNS01(routes); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
