package integration

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

type mcpTokenClaims struct {
	Issuer    string `json:"iss"`
	Subject   string `json:"sub"`
	Audience  string `json:"aud"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

// verifyMCPToken checks the bearer erun minted the way the env's edge does:
// EdDSA over the signing input, against the seeded desktop public key.
func verifyMCPToken(t *testing.T, token string, publicKey ed25519.PublicKey) mcpTokenClaims {
	t.Helper()
	segments := strings.Split(strings.TrimSpace(token), ".")
	if len(segments) != 3 {
		t.Fatalf("token %q does not have 3 JWT segments", token)
	}
	var header struct {
		Algorithm string `json:"alg"`
	}
	decodeJWTSegment(t, segments[0], &header)
	if header.Algorithm != "EdDSA" {
		t.Fatalf("token alg = %q, want EdDSA", header.Algorithm)
	}
	var claims mcpTokenClaims
	decodeJWTSegment(t, segments[1], &claims)
	signature, err := base64.RawURLEncoding.DecodeString(segments[2])
	if err != nil {
		t.Fatalf("decode token signature: %v", err)
	}
	if !ed25519.Verify(publicKey, []byte(segments[0]+"."+segments[1]), signature) {
		t.Fatal("token signature does not verify against the seeded desktop identity")
	}
	return claims
}

func decodeJWTSegment(t *testing.T, segment string, into any) {
	t.Helper()
	decoded, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		t.Fatalf("decode token segment: %v", err)
	}
	if err := json.Unmarshal(decoded, into); err != nil {
		t.Fatalf("unmarshal token segment: %v", err)
	}
}

func TestMCP(t *testing.T) {
	t.Run("help", testMCPHelp)
	t.Run("dry_run_traces_emcp_launch", testMCPDryRunTracesEmcpLaunch)
	t.Run("dry_run_uses_environment_local_port_by_default", testMCPDryRunUsesEnvironmentLocalPortByDefault)
	t.Run("real_run_launches_emcp_stub", testMCPRealRunLaunchesEmcpStub)
	t.Run("real_run_errors_when_emcp_missing", testMCPRealRunErrorsWhenEmcpMissing)
	t.Run("real_run_propagates_emcp_exit_failure", testMCPRealRunPropagatesEmcpExitFailure)
	t.Run("call_help", testMCPCallHelp)
	t.Run("tools_help", testMCPToolsHelp)
	t.Run("proxy_help", testMCPProxyHelp)
	t.Run("token_help", testMCPTokenHelp)
	t.Run("call_dry_run_traces_the_resolved_tool_call", testMCPCallDryRunTracesTheResolvedToolCall)
	t.Run("call_without_a_tool_fails_informatively", testMCPCallWithoutAToolFailsInformatively)
	t.Run("call_with_malformed_args_fails_before_resolving", testMCPCallWithMalformedArgsFailsBeforeResolving)
	t.Run("token_dry_run_traces_the_audience", testMCPTokenDryRunTracesTheAudience)
	t.Run("token_without_a_desktop_identity_says_where_it_comes_from", testMCPTokenWithoutADesktopIdentitySaysWhereItComesFrom)
	t.Run("token_real_run_mints_a_verifiable_bearer", testMCPTokenRealRunMintsAVerifiableBearer)
	t.Run("call_real_run_against_a_json_framed_edge", testMCPCallRealRunAgainstAJsonFramedEdge)
	t.Run("call_real_run_against_an_sse_framed_edge", testMCPCallRealRunAgainstAnSseFramedEdge)
	t.Run("call_real_run_json_output_carries_the_structured_result", testMCPCallRealRunJsonOutputCarriesTheStructuredResult)
	t.Run("call_real_run_surfaces_a_tool_error", testMCPCallRealRunSurfacesAToolError)
	t.Run("call_real_run_surfaces_a_jsonrpc_error", testMCPCallRealRunSurfacesAJsonrpcError)
	t.Run("call_real_run_prints_the_structured_result_when_a_tool_returns_no_text", testMCPCallRealRunPrintsTheStructuredResultWhenAToolReturnsNoText)
	t.Run("call_real_run_unauthorized_edge_points_at_a_redeploy", testMCPCallRealRunUnauthorizedEdgePointsAtARedeploy)
	t.Run("call_real_run_unreachable_edge_points_at_open", testMCPCallRealRunUnreachableEdgePointsAtOpen)
	t.Run("call_real_run_stale_forward_fails_fast_instead_of_hanging", testMCPCallRealRunStaleForwardFailsFastInsteadOfHanging)
	t.Run("call_real_run_unready_target_is_not_sent_to_a_reopen", testMCPCallRealRunUnreadyTargetIsNotSentToAReopen)
	t.Run("proxy_dry_run_traces_the_resolved_edge", testMCPProxyDryRunTracesTheResolvedEdge)
	t.Run("proxy_real_run_relays_a_json_framed_session", testMCPProxyRealRunRelaysAJsonFramedSession)
	t.Run("proxy_real_run_relays_an_sse_framed_reply", testMCPProxyRealRunRelaysAnSseFramedReply)
	t.Run("proxy_real_run_writes_nothing_for_a_notification", testMCPProxyRealRunWritesNothingForANotification)
	t.Run("proxy_real_run_unreachable_edge_answers_every_request_and_keeps_serving", testMCPProxyRealRunUnreachableEdgeAnswersEveryRequestAndKeepsServing)
	t.Run("proxy_real_run_unauthorized_edge_answers_with_the_redeploy_recovery", testMCPProxyRealRunUnauthorizedEdgeAnswersWithTheRedeployRecovery)
	t.Run("proxy_real_run_recovers_when_the_edge_forgets_its_session", testMCPProxyRealRunRecoversWhenTheEdgeForgetsItsSession)
	t.Run("call_real_run_recovers_when_the_local_forward_drops_mid_request", testMCPCallRealRunRecoversWhenTheLocalForwardDropsMidRequest)
	t.Run("proxy_real_run_surfaces_a_session_the_edge_never_accepts", testMCPProxyRealRunSurfacesASessionTheEdgeNeverAccepts)
	t.Run("call_real_run_a_live_edge_that_refuses_the_session_points_at_the_cli", testMCPCallRealRunALiveEdgeThatRefusesTheSessionPointsAtTheCli)
	t.Run("proxy_real_run_answers_a_request_the_edge_left_unanswered", testMCPProxyRealRunAnswersARequestTheEdgeLeftUnanswered)
	t.Run("proxy_real_run_serves_inputs_upload_locally", testMCPProxyRealRunServesInputsUploadLocally)
	t.Run("proxy_real_run_previews_inputs_upload_without_sending", testMCPProxyRealRunPreviewsInputsUploadWithoutSending)
	t.Run("tools_real_run_lists_tools_with_their_arguments", testMCPToolsRealRunListsToolsWithTheirArguments)
	t.Run("tools_structured_output_carries_the_descriptor_the_edge_sent", testMCPToolsStructuredOutputCarriesTheDescriptorTheEdgeSent)
}

func testMCPHelp(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	result := erun.Run(t, []string{"mcp", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/help", normalize.Apply(result.Combined))
}

func testMCPDryRunTracesEmcpLaunch(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	args := []string{
		"-v", "mcp", "team", "dev", "--dry-run",
		"--host", "0.0.0.0",
		"--port", "17001",
		"--path", "custom",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/dry_run_traces_emcp_launch", normalize.Apply(result.Combined))
}

func testMCPDryRunUsesEnvironmentLocalPortByDefault(t *testing.T) {
	t.Parallel()
	// Seeding alpha first pushes "team" to index 1, so the default port
	// resolves to 17100 (17000 + 100), not the index-0 17000 — the seed
	// order proves the port is environment-scoped.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "alpha", "dev")
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"-v", "mcp", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/dry_run_uses_environment_local_port_by_default", normalize.Apply(result.Combined))
}

func testMCPRealRunLaunchesEmcpStub(t *testing.T) {
	t.Parallel()
	// Real-run: the launcher body only executes past the dry-run gate,
	// so a stub is the only way to reach the bare-name emcp resolution
	// and lock the argv it launches.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "emcp", `printf 'emcp stub argv: %s\n' "$*"
exit 0`)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "emcp")...)
	result := erun.Run(t, []string{"-vv", "mcp", "team", "dev", "--port", "17001"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/real_run_launches_emcp_stub", normalize.Apply(result.Combined))
}

func testMCPRealRunErrorsWhenEmcpMissing(t *testing.T) {
	t.Parallel()
	// A missing emcp must surface the friendly "build or install it
	// first" message, not a raw exec error. The scenario's scrubbed PATH is
	// what makes emcp absent, on every host.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"mcp", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when emcp is missing, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "mcp/real_run_errors_when_emcp_missing", normalize.Apply(result.Combined))
}

func testMCPRealRunPropagatesEmcpExitFailure(t *testing.T) {
	t.Parallel()
	// A launched emcp that exits non-zero must propagate its raw exit
	// error and the tool's stderr (not the friendly missing-binary
	// message).
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "emcp", `printf 'emcp stub failing\n' >&2
exit 3`)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "emcp")...)
	result := erun.Run(t, []string{"mcp", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when emcp fails, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "mcp/real_run_propagates_emcp_exit_failure", normalize.Apply(result.Combined))
}

func testMCPCallHelp(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	result := erun.Run(t, []string{"mcp", "call", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/call_help", normalize.Apply(result.Combined))
}

func testMCPToolsHelp(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	result := erun.Run(t, []string{"mcp", "tools", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/tools_help", normalize.Apply(result.Combined))
}

func testMCPProxyHelp(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	result := erun.Run(t, []string{"mcp", "proxy", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/proxy_help", normalize.Apply(result.Combined))
}

func testMCPTokenHelp(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	result := erun.Run(t, []string{"mcp", "token", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/token_help", normalize.Apply(result.Combined))
}

func testMCPTokenDryRunTracesTheAudience(t *testing.T) {
	t.Parallel()
	// The audience is the whole point of the token, so the plan names it; no
	// identity is read in dry-run.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"mcp", "token", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/token_dry_run_traces_the_audience", normalize.Apply(result.Combined))
}

func testMCPTokenWithoutADesktopIdentitySaysWhereItComesFrom(t *testing.T) {
	t.Parallel()
	// No identity on this machine: the error must point at the desktop app
	// rather than mint a key no deployed environment would trust.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"mcp", "token"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit without a desktop identity, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "mcp/token_without_a_desktop_identity_says_where_it_comes_from", normalize.Apply(result.Combined))
}

func testMCPTokenRealRunMintsAVerifiableBearer(t *testing.T) {
	t.Parallel()
	// Real-run: the mint only happens past the dry-run gate. The token is a
	// fresh signature over a live timestamp, so the golden locks the result
	// shape (with the token normalized away) and the claims are asserted from
	// the parsed payload — the one thing the snapshot cannot check.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	identity := fixture.SeedDesktopIdentity(t, setup)
	result := erun.Run(t, []string{"mcp", "token", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/token_real_run_mints_a_verifiable_bearer", normalize.Apply(result.Combined))

	var minted struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &minted); err != nil {
		t.Fatalf("decode token result: %v\n%s", err, result.Stdout)
	}
	claims := verifyMCPToken(t, minted.Token, identity.PublicKey)
	if claims.Issuer != "file:///etc/erun/mcp-auth/desktopid.pub" {
		t.Fatalf("token issuer = %q", claims.Issuer)
	}
	if claims.Audience != "erun-mcp:team/dev" {
		t.Fatalf("token audience = %q", claims.Audience)
	}
	if claims.ExpiresAt-claims.IssuedAt != 300 {
		t.Fatalf("token lifetime = %ds, want 300", claims.ExpiresAt-claims.IssuedAt)
	}
}
