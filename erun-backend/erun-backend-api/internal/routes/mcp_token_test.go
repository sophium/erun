package routes

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/mcptoken"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	eruncommon "github.com/sophium/erun/erun-common"
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
	return mintMCPTokenWithScope(routes, userID, "")
}

// mintMCPTokenWithScope mints with an optional caller-requested scope; an
// empty scope sends a body-less request, exercising the same path a caller
// that doesn't know about scope yet takes.
func mintMCPTokenWithScope(routes MCPTokenRoutes, userID, scope string) *httptest.ResponseRecorder {
	var body *bytes.Reader
	if scope == "" {
		body = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(mintMCPTokenRequest{Scope: scope})
		if err != nil {
			panic(err)
		}
		body = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/environments/env-1/mcp-token", body)
	req.SetPathValue("environment_id", "env-1")
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   "tenant-1",
		ErunUserID: userID,
	}))
	rec := httptest.NewRecorder()
	routes.mintMCPToken(rec, req)
	return rec
}

// decodeMCPTokenClaims extracts the claims segment of a minted JWT so a test
// can assert what capability it actually carries, not just that minting
// succeeded.
func decodeMCPTokenClaims(t *testing.T, token string) eruncommon.MCPTokenClaims {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments, want 3", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims segment: %v", err)
	}
	var claims eruncommon.MCPTokenClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	return claims
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

// TestMintMCPTokenDefaultsToReadNotAdmin is the regression this route exists
// to close (erun#1877): a caller that doesn't ask for a scope -- exactly what
// an unaware or malicious consumer of this endpoint would do -- must not
// receive the desktop's admin-by-default compatibility case. It must receive
// the least capability a token can usefully carry, and its capability set
// must not be able to reach a mutating or execution tool.
func TestMintMCPTokenDefaultsToReadNotAdmin(t *testing.T) {
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
	if response.Scope != string(eruncommon.MCPCapabilityRead) {
		t.Fatalf("scope = %q, want %q", response.Scope, eruncommon.MCPCapabilityRead)
	}
	claims := decodeMCPTokenClaims(t, response.Token)
	capabilities := claims.Capabilities()
	if !capabilities.AllowsTool("version") {
		t.Fatal("a default-scoped token should still permit observation")
	}
	for _, mutating := range []string{"raw", "build", "deploy", "delete", "release"} {
		if capabilities.AllowsTool(mutating) {
			t.Fatalf("a default-scoped token must not reach %q, got %+v", mutating, capabilities)
		}
	}
}

// TestMintMCPTokenAdminScopeIsAdminEndToEnd covers the console's own case, the
// one erun#1877 says is defensible: an operator explicitly asking for a
// full-capability token for their own environment gets one, verified by
// decoding the minted token's claims through the same Capabilities() the
// deployed edge uses to authorize tool calls.
func TestMintMCPTokenAdminScopeIsAdminEndToEnd(t *testing.T) {
	routes := MCPTokenRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "prod"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       testSigner(t),
	}
	rec := mintMCPTokenWithScope(routes, "user-1", string(eruncommon.MCPCapabilityAdmin))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var response mcpTokenResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	claims := decodeMCPTokenClaims(t, response.Token)
	if !claims.Capabilities().AllowsTool("deploy") {
		t.Fatalf("an admin-scoped token must reach deploy, got %+v", claims.Capabilities())
	}
}

// TestMintMCPTokenAttachScopeIsAttachScopedEndToEnd is the test erun#1809's
// isolation guarantee was missing: it proves the *minting path* -- not just
// the capability table in isolation -- can actually produce a token that
// carries the attach tier and nothing else. Before erun#1877 this route could
// not produce such a token at all; every mint was unconditionally admin.
func TestMintMCPTokenAttachScopeIsAttachScopedEndToEnd(t *testing.T) {
	routes := MCPTokenRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "prod"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       testSigner(t),
	}
	rec := mintMCPTokenWithScope(routes, "user-1", string(eruncommon.MCPCapabilityAttach))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var response mcpTokenResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	claims := decodeMCPTokenClaims(t, response.Token)
	capabilities := claims.Capabilities()
	if !capabilities.Allows(eruncommon.MCPCapabilityAttach) {
		t.Fatal("an attach-scoped mint must actually carry the attach capability")
	}
	for _, forbidden := range []string{"exec_raw", "raw", "build", "push", "deploy", "delete", "release", "upgrade", "expose", "terraform", "init", "context_init"} {
		if capabilities.AllowsTool(forbidden) {
			t.Fatalf("an attach-scoped mint must not reach %q, got %+v", forbidden, capabilities)
		}
	}
	if capabilities.AllowsTool("version") {
		t.Fatal("attach is a distinct tier, not a wider read; it must not gain observation")
	}
}

// TestMintMCPTokenRefusesUnrecognizedScope is the entitlement test: a caller
// cannot mint a token carrying a capability string this platform never
// defined. The server is the authority over what a token may claim, not the
// request body -- an unrecognized value must not reach the signed token at
// all, in case some future code path resolves an unknown value more
// permissively than MCPCapabilitiesFromClaims does today.
func TestMintMCPTokenRefusesUnrecognizedScope(t *testing.T) {
	routes := MCPTokenRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "prod"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       testSigner(t),
	}
	rec := mintMCPTokenWithScope(routes, "user-1", "erun:platform-owner")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestMintMCPTokenRefusesMalformedBody proves a body that isn't valid JSON is
// rejected outright rather than silently falling back to the safe default,
// which would mask a caller's mistake as a narrower token than it thought it
// asked for.
func TestMintMCPTokenRefusesMalformedBody(t *testing.T) {
	routes := MCPTokenRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "prod"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       testSigner(t),
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/environments/env-1/mcp-token", bytes.NewReader([]byte("{not json")))
	req.SetPathValue("environment_id", "env-1")
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   "tenant-1",
		ErunUserID: "user-1",
	}))
	rec := httptest.NewRecorder()
	routes.mintMCPToken(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
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
