package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/mcptoken"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

func testSigner(t *testing.T) *mcptoken.Signer {
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

func mintMCPToken(routes MCPTokenRoutes, userID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/environments/env-1/mcp-token", nil)
	req.SetPathValue("environment_id", "env-1")
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   "tenant-1",
		ErunUserID: userID,
	}))
	rec := httptest.NewRecorder()
	routes.mintMCPToken(rec, req)
	return rec
}

// TestMintMCPTokenReturnsPerEnvToken mints a token for the caller's env and
// returns the per-env audience; the token carries the ERun-user sub, so the
// deployed edge can attribute the call.
func TestMintMCPTokenReturnsPerEnvToken(t *testing.T) {
	routes := MCPTokenRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "prod"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       testSigner(t),
	}
	rec := mintMCPToken(routes, "user-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var response mcpTokenResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if want := "erun-mcp:acme/prod"; response.Audience != want {
		t.Fatalf("audience = %q, want %q", response.Audience, want)
	}
	if response.Token == "" {
		t.Fatal("expected a non-empty token")
	}
}

// TestMintMCPTokenNotConfigured reports 501 when no backend signing key is set,
// rather than minting a token no edge can verify.
func TestMintMCPTokenNotConfigured(t *testing.T) {
	routes := MCPTokenRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{Name: "prod"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       nil,
	}
	rec := mintMCPToken(routes, "user-1")
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

// TestMintMCPTokenUnknownEnvironment surfaces a 404 for an env the caller's
// tenant does not own (RLS returns not-found), never leaking cross-tenant state.
func TestMintMCPTokenUnknownEnvironment(t *testing.T) {
	routes := MCPTokenRoutes{
		environments: &stubEnvironmentRepository{err: repository.ErrNotFound},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       testSigner(t),
	}
	rec := mintMCPToken(routes, "user-1")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
