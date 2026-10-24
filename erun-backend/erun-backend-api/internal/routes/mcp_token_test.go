package routes

import (
	"bytes"
	"context"
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
	"github.com/sophium/erun/erun-common/infra/mcpauth"
	"github.com/sophium/erun/erun-common/infra/mcptools"
)

// stubEntitlementChecker records what authorizeScope asked for.
type stubEntitlementChecker struct {
	err         error
	askedMethod string
	askedPath   string
	calls       int
}

func (c *stubEntitlementChecker) Authorize(_ context.Context, method, apiPath string) error {
	c.calls++
	c.askedMethod = method
	c.askedPath = apiPath
	return c.err
}

func testSigner(t *testing.T) *mcptoken.Signer {
	t.Helper()
	privatePEM, _, err := mcpauth.GenerateDesktopIdentity()
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

func decodeMCPTokenClaims(t *testing.T, token string) mcpauth.MCPTokenClaims {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments, want 3", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims segment: %v", err)
	}
	var claims mcpauth.MCPTokenClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	return claims
}

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
	if response.Scope != string(mcptools.MCPCapabilityRead) {
		t.Fatalf("scope = %q, want %q", response.Scope, mcptools.MCPCapabilityRead)
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

func TestMintMCPTokenAdminScopeIsAdminEndToEnd(t *testing.T) {
	entitlement := &stubEntitlementChecker{}
	routes := MCPTokenRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "prod"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       testSigner(t),
		entitlement:  entitlement,
	}
	rec := mintMCPTokenWithScope(routes, "user-1", string(mcptools.MCPCapabilityAdmin))
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
	if entitlement.calls != 1 {
		t.Fatalf("expected exactly one entitlement check, got %d", entitlement.calls)
	}
	if entitlement.askedMethod != http.MethodDelete || entitlement.askedPath != "/v1/environments/{environment_id}" {
		t.Fatalf("entitlement check asked (%s %s), want (DELETE /v1/environments/{environment_id}) -- the mapping decision this route enforces", entitlement.askedMethod, entitlement.askedPath)
	}
}

func TestMintMCPTokenRefusesAdminWithoutEntitlement(t *testing.T) {
	routes := MCPTokenRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "prod"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       testSigner(t),
		entitlement:  &stubEntitlementChecker{err: repository.ErrForbidden},
	}
	rec := mintMCPTokenWithScope(routes, "user-1", string(mcptools.MCPCapabilityAdmin))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("expected an error body")
	}
	var response mcpTokenResponse
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&response); err == nil && response.Token != "" {
		t.Fatalf("a refused mint must never carry a token, got %+v", response)
	}
}

func TestMintMCPTokenRefusesAdminWithClearReason(t *testing.T) {
	routes := MCPTokenRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "prod"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       testSigner(t),
		entitlement:  &stubEntitlementChecker{err: repository.ErrForbidden},
	}
	rec := mintMCPTokenWithScope(routes, "user-1", string(mcptools.MCPCapabilityAdmin))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	var envelope errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if envelope.Code != "MCP_ADMIN_SCOPE_FORBIDDEN" {
		t.Fatalf("code = %q, want MCP_ADMIN_SCOPE_FORBIDDEN", envelope.Code)
	}
	if !strings.Contains(envelope.Message, "erun:operate") {
		t.Fatalf("message = %q, want it to name the narrower scope as the alternative", envelope.Message)
	}
}

func TestMintMCPTokenNoEntitlementCheckerRefusesAdmin(t *testing.T) {
	routes := MCPTokenRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "prod"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       testSigner(t),
	}
	rec := mintMCPTokenWithScope(routes, "user-1", string(mcptools.MCPCapabilityAdmin))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestMintMCPTokenDeniedAdminCanStillMintReadEndToEnd(t *testing.T) {
	routes := MCPTokenRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "prod"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       testSigner(t),
		entitlement:  &stubEntitlementChecker{err: repository.ErrForbidden},
	}
	rec := mintMCPTokenWithScope(routes, "user-1", string(mcptools.MCPCapabilityRead))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var response mcpTokenResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	claims := decodeMCPTokenClaims(t, response.Token)
	capabilities := claims.Capabilities()
	if !capabilities.AllowsTool("version") {
		t.Fatal("a caller denied admin entitlement must still be able to mint read")
	}
	if capabilities.AllowsTool("deploy") {
		t.Fatalf("a read mint must never carry admin, got %+v", capabilities)
	}
}

func TestMintMCPTokenAttachScopeIsAttachScopedEndToEnd(t *testing.T) {
	routes := MCPTokenRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "prod"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       testSigner(t),
	}
	rec := mintMCPTokenWithScope(routes, "user-1", string(mcptools.MCPCapabilityAttach))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var response mcpTokenResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	claims := decodeMCPTokenClaims(t, response.Token)
	capabilities := claims.Capabilities()
	if !capabilities.Allows(mcptools.MCPCapabilityAttach) {
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

// erun:operate needs no extra entitlement: it only drives environments the tenant owns.
func TestMintMCPTokenOperateScopeIsOperateScopedEndToEnd(t *testing.T) {
	routes := MCPTokenRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "prod"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       testSigner(t),
	}
	rec := mintMCPTokenWithScope(routes, "user-1", string(mcptools.MCPCapabilityOperate))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var response mcpTokenResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	claims := decodeMCPTokenClaims(t, response.Token)
	capabilities := claims.Capabilities()
	if !capabilities.Allows(mcptools.MCPCapabilityOperate) {
		t.Fatal("an operate-scoped mint must actually carry the operate capability")
	}
	for _, required := range []string{"deploy", "context_start", "context_stop", "resize"} {
		if !capabilities.AllowsTool(required) {
			t.Fatalf("an operate-scoped mint must reach %q, got %+v", required, capabilities)
		}
	}
	for _, forbidden := range []string{"exec_raw", "raw", "build", "push", "delete", "release", "upgrade", "expose", "terraform", "init", "context_init"} {
		if capabilities.AllowsTool(forbidden) {
			t.Fatalf("an operate-scoped mint must not reach %q, got %+v", forbidden, capabilities)
		}
	}
	if capabilities.AllowsTool("version") {
		t.Fatal("operate is a distinct tier, not a wider read; it must not gain observation")
	}
}

func TestMintMCPTokenDeniedAdminCanStillMintOperateEndToEnd(t *testing.T) {
	routes := MCPTokenRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "prod"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       testSigner(t),
		entitlement:  &stubEntitlementChecker{err: repository.ErrForbidden},
	}
	rec := mintMCPTokenWithScope(routes, "user-1", string(mcptools.MCPCapabilityOperate))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var response mcpTokenResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	claims := decodeMCPTokenClaims(t, response.Token)
	if !claims.Capabilities().AllowsTool("deploy") {
		t.Fatal("a caller denied admin entitlement must still be able to mint operate")
	}
}

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

// Falling back to the default would mask the caller's mistake as a narrower token.
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
