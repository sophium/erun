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
	eruncommon "github.com/sophium/erun/erun-common"
)

// This file exercises the registry token endpoint against the *real*
// BearerTokenVerifier and the *real* mcptoken.Signer — the same components a
// deployed instance wires — because this endpoint is the security boundary of
// the whole hosted-registry feature and deserves proof against production
// code, not a hand-rolled stub of it.

// stubTenantResolver maps exactly one issuer to one tenant, and rejects every
// other issuer — standing in for the database-backed resolver so these tests
// do not need PostgreSQL to prove the HTTP-layer contract.
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

// testHarness wires a registrytoken.Handler behind httptest with a real
// desktop-style file:// issuer as the trusted erun-api issuer, a real
// mcptoken.Signer as the registry token signer, and a tenant resolver fixed to
// tenant "frs".
type testHarness struct {
	server         *httptest.Server
	apiPrivatePEM  []byte
	apiIssuer      string
	registrySigner *mcptoken.Signer
}

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()
	apiPriv, apiPub, err := eruncommon.GenerateDesktopIdentity()
	if err != nil {
		t.Fatalf("generate api identity: %v", err)
	}
	pubPath := filepath.Join(t.TempDir(), "api.pub")
	if err := os.WriteFile(pubPath, apiPub, 0o600); err != nil {
		t.Fatalf("write api public key: %v", err)
	}
	apiIssuer := eruncommon.FileIssuer(pubPath)

	verifier := backendapi.NewBearerTokenVerifier(backendapi.BearerTokenVerifierOptions{DesktopPublicKeyPath: pubPath})

	registryPriv, _, err := eruncommon.GenerateDesktopIdentity()
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
	token, err := eruncommon.SignMCPToken(h.apiPrivatePEM, eruncommon.MCPTokenClaims{
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

// TestValidTenantScopeIsGranted proves the golden path: a valid tenant token
// requesting its own repository scope is granted exactly that scope.
func TestValidTenantScopeIsGranted(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	now := time.Now()
	token := h.apiToken(t, eruncommon.APITokenAudience, now.Add(-time.Minute), now.Add(time.Hour))

	resp := h.requestToken(t, token, "repository:frs/hello:pull,push")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	access := decodeAccess(t, resp)
	if len(access) != 1 || access[0].Name != "frs/hello" {
		t.Fatalf("access = %#v, want the requested frs/hello scope", access)
	}
}

// TestCrossTenantScopeGrantsNothing is the exact security boundary the issue
// calls out: a valid tenant-A token requesting tenant-B's repository scope
// still authenticates (200), but is granted nothing.
func TestCrossTenantScopeGrantsNothing(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	now := time.Now()
	token := h.apiToken(t, eruncommon.APITokenAudience, now.Add(-time.Minute), now.Add(time.Hour))

	resp := h.requestToken(t, token, "repository:other-tenant/secret:pull,push")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (authenticated, granted nothing)", resp.StatusCode)
	}
	if access := decodeAccess(t, resp); len(access) != 0 {
		t.Fatalf("access = %#v, want empty for a cross-tenant scope", access)
	}
}

// TestExpiredTokenIsRejected proves an expired erun-api token is rejected.
func TestExpiredTokenIsRejected(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	now := time.Now()
	token := h.apiToken(t, eruncommon.APITokenAudience, now.Add(-2*time.Hour), now.Add(-time.Hour))

	resp := h.requestToken(t, token, "repository:frs/hello:pull")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for an expired token", resp.StatusCode)
	}
}

// TestWrongAudienceTokenIsRejected proves a token minted for a different
// capability (here, an MCP-audience token) is rejected as the Basic password —
// it must not be usable to mint a registry token just because it was signed by
// the same trusted issuer.
func TestWrongAudienceTokenIsRejected(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	now := time.Now()
	token := h.apiToken(t, eruncommon.MCPTokenAudience("frs", "prod"), now.Add(-time.Minute), now.Add(time.Hour))

	resp := h.requestToken(t, token, "repository:frs/hello:pull")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a wrong-audience token", resp.StatusCode)
	}
}

// TestUnknownIssuerIsRejected proves a token signed by a key the verifier does
// not trust is rejected, even though it is otherwise well-formed.
func TestUnknownIssuerIsRejected(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	otherPriv, otherPub, err := eruncommon.GenerateDesktopIdentity()
	if err != nil {
		t.Fatalf("generate other identity: %v", err)
	}
	otherPath := filepath.Join(t.TempDir(), "other.pub")
	if err := os.WriteFile(otherPath, otherPub, 0o600); err != nil {
		t.Fatalf("write other public key: %v", err)
	}
	now := time.Now()
	token, err := eruncommon.SignMCPToken(otherPriv, eruncommon.MCPTokenClaims{
		Issuer:    eruncommon.FileIssuer(otherPath),
		Subject:   "user-1",
		Audience:  eruncommon.APITokenAudience,
		IssuedAt:  now.Add(-time.Minute).Unix(),
		ExpiresAt: now.Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign token from unknown issuer: %v", err)
	}

	resp := h.requestToken(t, token, "repository:frs/hello:pull")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for an unknown issuer", resp.StatusCode)
	}
}

// TestMissingCredentialsIsRejected proves the endpoint refuses a request with
// no Basic auth at all, rather than falling through to an anonymous grant.
func TestMissingCredentialsIsRejected(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	resp := h.requestToken(t, "", "repository:frs/hello:pull")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for missing credentials", resp.StatusCode)
	}
}

// TestMissingServiceParameterIsRejected proves a request with no `service`
// parameter is rejected before anything is signed — there is no registry
// service to scope the token's audience to.
func TestMissingServiceParameterIsRejected(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	now := time.Now()
	token := h.apiToken(t, eruncommon.APITokenAudience, now.Add(-time.Minute), now.Add(time.Hour))

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
