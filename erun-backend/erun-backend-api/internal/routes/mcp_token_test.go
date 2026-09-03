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
	eruncommon "github.com/sophium/erun/erun-common"
)

// stubEntitlementChecker stands in for the real permission authorizer.
// grantedMethod/Path record what authorizeScope actually asked for, so a test
// can pin the exact mapping decision (erun#1891) rather than only its outcome.
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
// one erun#1877 says is defensible: an operator who genuinely holds the
// mapped entitlement (erun#1891) and explicitly asks for a full-capability
// token for their own environment gets one, verified by decoding the minted
// token's claims through the same Capabilities() the deployed edge uses to
// authorize tool calls.
func TestMintMCPTokenAdminScopeIsAdminEndToEnd(t *testing.T) {
	entitlement := &stubEntitlementChecker{}
	routes := MCPTokenRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "prod"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       testSigner(t),
		entitlement:  entitlement,
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
	if entitlement.calls != 1 {
		t.Fatalf("expected exactly one entitlement check, got %d", entitlement.calls)
	}
	if entitlement.askedMethod != http.MethodDelete || entitlement.askedPath != "/v1/environments/{environment_id}" {
		t.Fatalf("entitlement check asked (%s %s), want (DELETE /v1/environments/{environment_id}) -- the mapping decision this route enforces", entitlement.askedMethod, entitlement.askedPath)
	}
}

// TestMintMCPTokenRefusesAdminWithoutEntitlement is the entitlement gate this
// PR closes (erun#1891): a TenantUserClass caller who is not entitled to
// delete this environment must not receive an erun:admin token, even though
// the route itself is reachable by any tenant user. 403, and no token is
// minted at all.
func TestMintMCPTokenRefusesAdminWithoutEntitlement(t *testing.T) {
	routes := MCPTokenRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "prod"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       testSigner(t),
		entitlement:  &stubEntitlementChecker{err: repository.ErrForbidden},
	}
	rec := mintMCPTokenWithScope(routes, "user-1", string(eruncommon.MCPCapabilityAdmin))
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

// TestMintMCPTokenNoEntitlementCheckerRefusesAdmin proves the fail-closed
// default: a deployment with no permission backend wired (entitlement is nil)
// must refuse erun:admin rather than mint it unconditionally, the exact
// regression this issue exists to close.
func TestMintMCPTokenNoEntitlementCheckerRefusesAdmin(t *testing.T) {
	routes := MCPTokenRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "prod"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       testSigner(t),
	}
	rec := mintMCPTokenWithScope(routes, "user-1", string(eruncommon.MCPCapabilityAdmin))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// TestMintMCPTokenDeniedAdminCanStillMintReadEndToEnd proves the refusal above
// is scoped to erun:admin, not the whole route: the same caller, still denied
// admin entitlement, can mint the read tier the route's own TenantUserClass
// gate already entitles every caller to -- decoded end to end, the same
// standard TestMintMCPTokenAdminScopeIsAdminEndToEnd holds the positive case
// to, so a caller entitled to none of the escalated tiers is never left with
// no way to obtain a usable token at all.
func TestMintMCPTokenDeniedAdminCanStillMintReadEndToEnd(t *testing.T) {
	routes := MCPTokenRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "prod"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       testSigner(t),
		entitlement:  &stubEntitlementChecker{err: repository.ErrForbidden},
	}
	rec := mintMCPTokenWithScope(routes, "user-1", string(eruncommon.MCPCapabilityRead))
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

// TestMintMCPTokenOperateScopeIsOperateScopedEndToEnd is erun#1107's Phase 3
// mint path: erun:operate needs no entitlement beyond reaching this route
// (the same as erun:read/erun:attach), because it only ever drives the
// lifecycle of an environment the caller's tenant already owns -- unlike
// erun:admin, it grants nothing a TenantUser could not already do through
// the API.
func TestMintMCPTokenOperateScopeIsOperateScopedEndToEnd(t *testing.T) {
	routes := MCPTokenRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "prod"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       testSigner(t),
	}
	rec := mintMCPTokenWithScope(routes, "user-1", string(eruncommon.MCPCapabilityOperate))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var response mcpTokenResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	claims := decodeMCPTokenClaims(t, response.Token)
	capabilities := claims.Capabilities()
	if !capabilities.Allows(eruncommon.MCPCapabilityOperate) {
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

// TestMintMCPTokenDeniedAdminCanStillMintOperateEndToEnd proves the
// entitlement refusal above is scoped to erun:admin, the same way
// TestMintMCPTokenDeniedAdminCanStillMintReadEndToEnd already proves it for
// erun:read: a caller denied the admin entitlement can still mint operate.
func TestMintMCPTokenDeniedAdminCanStillMintOperateEndToEnd(t *testing.T) {
	routes := MCPTokenRoutes{
		environments: &stubEnvironmentRepository{environment: model.Environment{EnvironmentID: "env-1", Name: "prod"}},
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		signer:       testSigner(t),
		entitlement:  &stubEntitlementChecker{err: repository.ErrForbidden},
	}
	rec := mintMCPTokenWithScope(routes, "user-1", string(eruncommon.MCPCapabilityOperate))
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
