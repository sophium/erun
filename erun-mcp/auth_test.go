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

// okHandler is a stub the auth middleware wraps; reaching it means the request
// was authorized.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func trustedIssuerWithToken(t *testing.T) (issuer, token string) {
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
	issuer, token := trustedIssuerWithToken(t)
	authed := mcpAuthConfig{trustedIssuer: issuer, audience: "erun-mcp"}

	cases := []struct {
		name       string
		cfg        mcpAuthConfig
		authHeader string
		wantStatus int
	}{
		{name: "no auth configured passes through", cfg: mcpAuthConfig{}, authHeader: "", wantStatus: http.StatusOK},
		{name: "configured but no token is rejected", cfg: authed, authHeader: "", wantStatus: http.StatusUnauthorized},
		{name: "valid bearer is authorized", cfg: authed, authHeader: "Bearer " + token, wantStatus: http.StatusOK},
		{name: "garbage bearer is rejected", cfg: authed, authHeader: "Bearer not-a-jwt", wantStatus: http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()
			authHTTPMiddleware(tc.cfg, okHandler()).ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestMCPAuthConfigFromEnv(t *testing.T) {
	t.Setenv(envMCPTrustedIssuer, "file:///etc/erun/mcp-auth/desktopid.pub")
	t.Setenv(envMCPAudience, "erun-mcp")
	cfg := mcpAuthConfigFromEnv()
	if !cfg.enabled() || cfg.trustedIssuer != "file:///etc/erun/mcp-auth/desktopid.pub" || cfg.audience != "erun-mcp" {
		t.Fatalf("cfg = %+v", cfg)
	}
	t.Setenv(envMCPTrustedIssuer, "")
	if mcpAuthConfigFromEnv().enabled() {
		t.Fatal("expected auth disabled when no trusted issuer is set")
	}
}
