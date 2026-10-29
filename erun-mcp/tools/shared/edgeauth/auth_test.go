package edgeauth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/sophium/erun/erun-common/infra/mcpauth"
)

// tenantEchoHandler echoes the resolved tenant.
func tenantEchoHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(r.Header.Get(authTenantHeader)))
	})
}

func identityWithToken(t *testing.T) (issuer, token string) {
	t.Helper()
	return identityWithScopedToken(t, "")
}

// identityWithScopedToken mints a token with scope; an empty scope resolves to admin.
func identityWithScopedToken(t *testing.T, scope string) (issuer, token string) {
	t.Helper()
	priv, pub, err := mcpauth.GenerateDesktopIdentity()
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	path := filepath.Join(t.TempDir(), "desktopid.pub")
	if err := os.WriteFile(path, pub, 0o600); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	issuer = mcpauth.FileIssuer(path)
	token, err = mcpauth.SignMCPToken(priv, mcpauth.MCPTokenClaims{
		Issuer:    issuer,
		Audience:  "erun-mcp",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
		Scope:     scope,
	})
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return issuer, token
}

func TestAuthMiddleware(t *testing.T) {
	issuer, token := identityWithToken(t)
	// Validly signed, but by an untrusted issuer.
	_, otherToken := identityWithToken(t)
	authed := McpAuthConfig{trustedIssuers: map[string]string{issuer: "acme"}, audience: "erun-mcp"}

	cases := []struct {
		name       string
		cfg        McpAuthConfig
		authHeader string
		wantStatus int
		wantTenant string
	}{
		{name: "no auth configured passes through", cfg: McpAuthConfig{}, wantStatus: http.StatusOK},
		{name: "configured but no token is rejected", cfg: authed, wantStatus: http.StatusUnauthorized},
		{name: "valid bearer is authorized and resolves the tenant", cfg: authed, authHeader: "Bearer " + token, wantStatus: http.StatusOK, wantTenant: "acme"},
		{name: "garbage bearer is rejected", cfg: authed, authHeader: "Bearer not-a-jwt", wantStatus: http.StatusUnauthorized},
		{name: "untrusted issuer is rejected", cfg: authed, authHeader: "Bearer " + otherToken, wantStatus: http.StatusUnauthorized},
		{name: "tenant mismatch is rejected (#657)", cfg: McpAuthConfig{trustedIssuers: map[string]string{issuer: "beta"}, audience: "erun-mcp", tenant: "acme"}, authHeader: "Bearer " + token, wantStatus: http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()
			AuthHTTPMiddleware(tc.cfg, tenantEchoHandler()).ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus == http.StatusOK && tc.wantTenant != "" && rec.Body.String() != tc.wantTenant {
				t.Fatalf("resolved tenant = %q, want %q", rec.Body.String(), tc.wantTenant)
			}
		})
	}
}

func TestAuthMiddlewareMultiTenant(t *testing.T) {
	issuerA, tokenA := identityWithToken(t)
	issuerB, tokenB := identityWithToken(t)
	cfg := McpAuthConfig{
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
		AuthHTTPMiddleware(cfg, tenantEchoHandler()).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != tc.wantTenant {
			t.Fatalf("status=%d tenant=%q, want 200 / %q", rec.Code, rec.Body.String(), tc.wantTenant)
		}
	}
}

func TestMCPAuthConfigFromEnv(t *testing.T) {
	t.Run("key/value map", func(t *testing.T) {
		t.Setenv(EnvMCPTrustedIssuers, `{"file:///etc/erun/mcp-auth/a.pub":"acme","https://idp.example":"beta"}`)
		t.Setenv(EnvMCPAudience, "erun-mcp")
		cfg := McpAuthConfigFromEnv()
		if !cfg.enabled() || cfg.trustedIssuers["file:///etc/erun/mcp-auth/a.pub"] != "acme" || cfg.trustedIssuers["https://idp.example"] != "beta" {
			t.Fatalf("cfg = %+v", cfg)
		}
		if cfg.audience != "erun-mcp" {
			t.Fatalf("audience = %q", cfg.audience)
		}
	})

	t.Run("single-issuer sugar maps to the env tenant", func(t *testing.T) {
		t.Setenv(EnvMCPTrustedIssuers, "")
		t.Setenv(EnvMCPTrustedIssuer, "file:///etc/erun/mcp-auth/desktopid.pub")
		t.Setenv(EnvTenant, "acme")
		cfg := McpAuthConfigFromEnv()
		if cfg.trustedIssuers["file:///etc/erun/mcp-auth/desktopid.pub"] != "acme" {
			t.Fatalf("cfg = %+v", cfg)
		}
	})

	t.Run("disabled when nothing configured", func(t *testing.T) {
		t.Setenv(EnvMCPTrustedIssuers, "")
		t.Setenv(EnvMCPTrustedIssuer, "")
		if McpAuthConfigFromEnv().enabled() {
			t.Fatal("expected auth disabled when no trusted issuer is set")
		}
	})
}

// mockOIDCProvider is an in-process OIDC issuer.
type mockOIDCProvider struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	keyID  string
}

func newMockOIDCProvider(t *testing.T) *mockOIDCProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	p := &mockOIDCProvider{key: key, keyID: "test-key-1"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeOIDCJSON(w, map[string]any{
			"issuer":                                p.issuer(),
			"jwks_uri":                              p.issuer() + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		writeOIDCJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key:       p.key.Public(),
			KeyID:     p.keyID,
			Algorithm: string(jose.RS256),
			Use:       "sig",
		}}})
	})
	p.server = httptest.NewServer(mux)
	t.Cleanup(p.server.Close)
	return p
}

func (p *mockOIDCProvider) issuer() string { return p.server.URL }

func (p *mockOIDCProvider) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	return signOIDCToken(t, p.key, p.keyID, claims)
}

func signOIDCToken(t *testing.T, key *rsa.PrivateKey, keyID string, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", keyID),
	)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatalf("serialize token: %v", err)
	}
	return token
}

func writeOIDCJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func oidcClaims(issuer, audience string) map[string]any {
	now := time.Now()
	return map[string]any{
		"iss": issuer,
		"sub": "user-1",
		"aud": audience,
		"iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
	}
}

func TestAuthMiddlewareOIDCDispatch(t *testing.T) {
	provider := newMockOIDCProvider(t)
	const audience = "erun-mcp:acme/prod"
	cfg := McpAuthConfig{
		trustedIssuers: map[string]string{provider.issuer(): "acme"},
		audience:       audience,
		oidc:           mcpauth.NewOIDCVerifier(),
	}

	t.Run("valid OIDC token is authorized and resolves the tenant", func(t *testing.T) {
		token := provider.sign(t, oidcClaims(provider.issuer(), audience))
		rec := serveAuth(t, cfg, "Bearer "+token)
		if rec.Code != http.StatusOK || rec.Body.String() != "acme" {
			t.Fatalf("status=%d tenant=%q, want 200 / acme", rec.Code, rec.Body.String())
		}
	})

	t.Run("untrusted OIDC issuer is rejected", func(t *testing.T) {
		other := newMockOIDCProvider(t)
		token := other.sign(t, oidcClaims(other.issuer(), audience))
		rec := serveAuth(t, cfg, "Bearer "+token)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d, want 401", rec.Code)
		}
	})

	t.Run("bad signature is rejected", func(t *testing.T) {
		otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("generate other key: %v", err)
		}
		// Signed by a key outside the provider's JWKS.
		token := signOIDCToken(t, otherKey, provider.keyID, oidcClaims(provider.issuer(), audience))
		rec := serveAuth(t, cfg, "Bearer "+token)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d, want 401", rec.Code)
		}
	})

	t.Run("wrong audience is rejected", func(t *testing.T) {
		token := provider.sign(t, oidcClaims(provider.issuer(), "erun-mcp:acme/dev"))
		rec := serveAuth(t, cfg, "Bearer "+token)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d, want 401", rec.Code)
		}
	})
}

func serveAuth(t *testing.T, cfg McpAuthConfig, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	AuthHTTPMiddleware(cfg, tenantEchoHandler()).ServeHTTP(rec, req)
	return rec
}

// The header-only resolver must still reject a subprotocol-only token.
func TestWSAttachAuthAcceptsSubprotocolToken(t *testing.T) {
	issuer, token := identityWithToken(t)
	cfg := McpAuthConfig{trustedIssuers: map[string]string{issuer: "acme"}, audience: "erun-mcp"}

	req := httptest.NewRequest(http.MethodGet, "/mcp/attach/session-1", nil)
	req.Header.Set("Sec-WebSocket-Protocol", AttachAuthSubprotocol+", "+token)
	rec := httptest.NewRecorder()
	WsAttachAuthHTTPMiddleware(cfg, tenantEchoHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "acme" {
		t.Fatalf("status=%d tenant=%q, want 200 / acme", rec.Code, rec.Body.String())
	}

	reqOtherRoute := httptest.NewRequest(http.MethodGet, "/mcp/attach/session-1", nil)
	reqOtherRoute.Header.Set("Sec-WebSocket-Protocol", AttachAuthSubprotocol+", "+token)
	recOtherRoute := httptest.NewRecorder()
	AuthHTTPMiddleware(cfg, tenantEchoHandler()).ServeHTTP(recOtherRoute, reqOtherRoute)
	if recOtherRoute.Code != http.StatusUnauthorized {
		t.Fatalf("ordinary resolver status = %d, want 401 (subprotocol fallback must not apply here)", recOtherRoute.Code)
	}
}

// The scheme name without a token must not resolve to an empty token.
func TestAttachSubprotocolBearerTokenMalformedOffers(t *testing.T) {
	cases := []struct {
		name   string
		header string
	}{
		{"no header", ""},
		{"wrong scheme", "some-other-protocol, abc123"},
		{"scheme with no token", AttachAuthSubprotocol},
		{"three entries", AttachAuthSubprotocol + ", abc, def"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/mcp/attach/session-1", nil)
			if tc.header != "" {
				req.Header.Set("Sec-WebSocket-Protocol", tc.header)
			}
			if got := attachSubprotocolBearerToken(req); got != "" {
				t.Fatalf("token = %q, want empty", got)
			}
		})
	}
}
