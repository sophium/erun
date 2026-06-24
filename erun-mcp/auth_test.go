package erunmcp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// tenantEchoHandler is a stub the auth middleware wraps; reaching it means the
// request was authorized, and it echoes the resolved auth tenant so a test can
// assert per-URL tenant identification.
func tenantEchoHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(r.Header.Get(authTenantHeader)))
	})
}

// identityWithToken generates an Ed25519 identity, writes its public key to a
// temp file, and returns the file:// issuer plus a signed bearer token.
func identityWithToken(t *testing.T) (issuer, token string) {
	t.Helper()
	priv, pub, err := eruncommon.GenerateDesktopIdentity()
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	path := filepath.Join(t.TempDir(), "desktopid.pub")
	if err := os.WriteFile(path, pub, 0o600); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	issuer = eruncommon.FileIssuer(path)
	token, err = eruncommon.SignMCPToken(priv, eruncommon.MCPTokenClaims{
		Issuer:    issuer,
		Audience:  "erun-mcp",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return issuer, token
}

func TestAuthMiddleware(t *testing.T) {
	issuer, token := identityWithToken(t)
	// A second, untrusted identity: validly signed by its own key, but its issuer
	// is not in the trusted map, so it must still be rejected.
	_, otherToken := identityWithToken(t)
	authed := mcpAuthConfig{trustedIssuers: map[string]string{issuer: "acme"}, audience: "erun-mcp"}

	cases := []struct {
		name       string
		cfg        mcpAuthConfig
		authHeader string
		wantStatus int
		wantTenant string
	}{
		{name: "no auth configured passes through", cfg: mcpAuthConfig{}, wantStatus: http.StatusOK},
		{name: "configured but no token is rejected", cfg: authed, wantStatus: http.StatusUnauthorized},
		{name: "valid bearer is authorized and resolves the tenant", cfg: authed, authHeader: "Bearer " + token, wantStatus: http.StatusOK, wantTenant: "acme"},
		{name: "garbage bearer is rejected", cfg: authed, authHeader: "Bearer not-a-jwt", wantStatus: http.StatusUnauthorized},
		{name: "untrusted issuer is rejected", cfg: authed, authHeader: "Bearer " + otherToken, wantStatus: http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()
			authHTTPMiddleware(tc.cfg, tenantEchoHandler()).ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus == http.StatusOK && tc.wantTenant != "" && rec.Body.String() != tc.wantTenant {
				t.Fatalf("resolved tenant = %q, want %q", rec.Body.String(), tc.wantTenant)
			}
		})
	}
}

// TestAuthMiddlewareMultiTenant locks the key/value model: two issuers map to
// two tenants, and a token from one issuer resolves that issuer's tenant.
func TestAuthMiddlewareMultiTenant(t *testing.T) {
	issuerA, tokenA := identityWithToken(t)
	issuerB, tokenB := identityWithToken(t)
	cfg := mcpAuthConfig{
		trustedIssuers: map[string]string{issuerA: "acme", issuerB: "beta"},
		audience:       "erun-mcp",
	}
	for _, tc := range []struct{ token, wantTenant string }{
		{tokenA, "acme"},
		{tokenB, "beta"},
	} {
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.Header.Set("Authorization", "Bearer "+tc.token)
		rec := httptest.NewRecorder()
		authHTTPMiddleware(cfg, tenantEchoHandler()).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != tc.wantTenant {
			t.Fatalf("status=%d tenant=%q, want 200 / %q", rec.Code, rec.Body.String(), tc.wantTenant)
		}
	}
}

func TestMCPAuthConfigFromEnv(t *testing.T) {
	t.Run("key/value map", func(t *testing.T) {
		t.Setenv(envMCPTrustedIssuers, `{"file:///etc/erun/mcp-auth/a.pub":"acme","https://idp.example":"beta"}`)
		t.Setenv(envMCPAudience, "erun-mcp")
		cfg := mcpAuthConfigFromEnv()
		if !cfg.enabled() || cfg.trustedIssuers["file:///etc/erun/mcp-auth/a.pub"] != "acme" || cfg.trustedIssuers["https://idp.example"] != "beta" {
			t.Fatalf("cfg = %+v", cfg)
		}
		if cfg.audience != "erun-mcp" {
			t.Fatalf("audience = %q", cfg.audience)
		}
	})

	t.Run("single-issuer sugar maps to the env tenant", func(t *testing.T) {
		t.Setenv(envMCPTrustedIssuers, "")
		t.Setenv(envMCPTrustedIssuer, "file:///etc/erun/mcp-auth/desktopid.pub")
		t.Setenv(envTenant, "acme")
		cfg := mcpAuthConfigFromEnv()
		if cfg.trustedIssuers["file:///etc/erun/mcp-auth/desktopid.pub"] != "acme" {
			t.Fatalf("cfg = %+v", cfg)
		}
	})

	t.Run("disabled when nothing configured", func(t *testing.T) {
		t.Setenv(envMCPTrustedIssuers, "")
		t.Setenv(envMCPTrustedIssuer, "")
		if mcpAuthConfigFromEnv().enabled() {
			t.Fatal("expected auth disabled when no trusted issuer is set")
		}
	})
}
