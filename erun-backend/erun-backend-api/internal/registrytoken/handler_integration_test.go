package registrytoken_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backendapi "github.com/sophium/erun/erun-backend/erun-backend-api"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/mcptoken"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/registrytoken"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-common/infra/mcpauth"
)

// These tests use the real BearerTokenVerifier and mcptoken.Signer because this
// endpoint is a security boundary.

type stubTenantResolver struct {
	issuer string
	tenant model.Tenant
}

func (s stubTenantResolver) ResolveTenantByIssuer(_ context.Context, claims security.Claims) (model.Tenant, error) {
	if claims.Issuer != s.issuer {
		return model.Tenant{}, fmt.Errorf("unknown issuer %q", claims.Issuer)
	}
	return s.tenant, nil
}

type testHarness struct {
	server         *httptest.Server
	apiPrivatePEM  []byte
	apiIssuer      string
	registrySigner *mcptoken.Signer
}

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()
	apiPriv, apiPub, err := mcpauth.GenerateDesktopIdentity()
	if err != nil {
		t.Fatalf("generate api identity: %v", err)
	}
	pubPath := filepath.Join(t.TempDir(), "api.pub")
	if err := os.WriteFile(pubPath, apiPub, 0o600); err != nil {
		t.Fatalf("write api public key: %v", err)
	}
	apiIssuer := mcpauth.FileIssuer(pubPath)

	verifier := backendapi.NewBearerTokenVerifier(backendapi.BearerTokenVerifierOptions{DesktopPublicKeyPath: pubPath})

	registryPriv, _, err := mcpauth.GenerateDesktopIdentity()
	if err != nil {
		t.Fatalf("generate registry signing key: %v", err)
	}
	signer, err := mcptoken.NewSigner(registryPriv)
	if err != nil {
		t.Fatalf("new registry signer: %v", err)
	}

	tenants := stubTenantResolver{issuer: apiIssuer, tenant: model.Tenant{TenantID: "tenant-1", Name: "frs"}}
	handler := registrytoken.NewHandler(verifier, tenants, signer)
	mux := http.NewServeMux()
	handler.Register(mux)

	return &testHarness{
		server:         httptest.NewServer(mux),
		apiPrivatePEM:  apiPriv,
		apiIssuer:      apiIssuer,
		registrySigner: signer,
	}
}

func (h *testHarness) close() { h.server.Close() }

func (h *testHarness) apiToken(t *testing.T, audience string, issuedAt, expiresAt time.Time) string {
	t.Helper()
	token, err := mcpauth.SignMCPToken(h.apiPrivatePEM, mcpauth.MCPTokenClaims{
		Issuer:    h.apiIssuer,
		Subject:   "user-1",
		Audience:  audience,
		IssuedAt:  issuedAt.Unix(),
		ExpiresAt: expiresAt.Unix(),
	})
	if err != nil {
		t.Fatalf("sign api token: %v", err)
	}
	return token
}

func (h *testHarness) requestToken(t *testing.T, basicPassword string, scopes ...string) *http.Response {
	t.Helper()
	values := url.Values{"service": {"registry.erunpaas.com"}}
	for _, scope := range scopes {
		values.Add("scope", scope)
	}
	req, err := http.NewRequest(http.MethodGet, h.server.URL+"/v2/token?"+values.Encode(), nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if basicPassword != "" {
		req.SetBasicAuth("erun", basicPassword)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func decodeAccess(t *testing.T, resp *http.Response) []mcptoken.RegistryAccessScope {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	segments := strings.Split(body.Token, ".")
	if len(segments) != 3 {
		t.Fatalf("token has %d segments, want 3", len(segments))
	}
	raw, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		t.Fatalf("decode claims segment: %v", err)
	}
	var claims struct {
		Access []mcptoken.RegistryAccessScope `json:"access"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	return claims.Access
}

func TestValidTenantScopeIsGranted(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	now := time.Now()
	token := h.apiToken(t, mcpauth.APITokenAudience, now.Add(-time.Minute), now.Add(time.Hour))

	resp := h.requestToken(t, token, "repository:frs/hello:pull,push")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	access := decodeAccess(t, resp)
	if len(access) != 1 || access[0].Name != "frs/hello" {
		t.Fatalf("access = %#v, want the requested frs/hello scope", access)
	}
}

// A cross-tenant scope request still authenticates (200) but is granted nothing.
func TestCrossTenantScopeGrantsNothing(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	now := time.Now()
	token := h.apiToken(t, mcpauth.APITokenAudience, now.Add(-time.Minute), now.Add(time.Hour))

	resp := h.requestToken(t, token, "repository:other-tenant/secret:pull,push")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (authenticated, granted nothing)", resp.StatusCode)
	}
	if access := decodeAccess(t, resp); len(access) != 0 {
		t.Fatalf("access = %#v, want empty for a cross-tenant scope", access)
	}
}

func TestExpiredTokenIsRejected(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	now := time.Now()
	token := h.apiToken(t, mcpauth.APITokenAudience, now.Add(-2*time.Hour), now.Add(-time.Hour))

	resp := h.requestToken(t, token, "repository:frs/hello:pull")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for an expired token", resp.StatusCode)
	}
}

func TestWrongAudienceTokenIsRejected(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	now := time.Now()
	token := h.apiToken(t, mcpauth.MCPTokenAudience("frs", "prod"), now.Add(-time.Minute), now.Add(time.Hour))

	resp := h.requestToken(t, token, "repository:frs/hello:pull")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a wrong-audience token", resp.StatusCode)
	}
}

func TestUnknownIssuerIsRejected(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	otherPriv, otherPub, err := mcpauth.GenerateDesktopIdentity()
	if err != nil {
		t.Fatalf("generate other identity: %v", err)
	}
	otherPath := filepath.Join(t.TempDir(), "other.pub")
	if err := os.WriteFile(otherPath, otherPub, 0o600); err != nil {
		t.Fatalf("write other public key: %v", err)
	}
	now := time.Now()
	token, err := mcpauth.SignMCPToken(otherPriv, mcpauth.MCPTokenClaims{
		Issuer:    mcpauth.FileIssuer(otherPath),
		Subject:   "user-1",
		Audience:  mcpauth.APITokenAudience,
		IssuedAt:  now.Add(-time.Minute).Unix(),
		ExpiresAt: now.Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign token from unknown issuer: %v", err)
	}

	resp := h.requestToken(t, token, "repository:frs/hello:pull")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for an unknown issuer", resp.StatusCode)
	}
}

func TestMissingCredentialsIsRejected(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	resp := h.requestToken(t, "", "repository:frs/hello:pull")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for missing credentials", resp.StatusCode)
	}
}

func TestMissingServiceParameterIsRejected(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	now := time.Now()
	token := h.apiToken(t, mcpauth.APITokenAudience, now.Add(-time.Minute), now.Add(time.Hour))

	req, err := http.NewRequest(http.MethodGet, h.server.URL+"/v2/token?scope=repository:frs/hello:pull", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.SetBasicAuth("erun", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a missing service parameter", resp.StatusCode)
	}
}
