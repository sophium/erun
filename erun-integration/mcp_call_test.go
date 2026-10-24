package integration

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testMCPCallDryRunTracesTheResolvedToolCall(t *testing.T) {
	t.Parallel()
	// The plan must name the endpoint the call would reach, the tool, and the
	// arguments, and must not touch the network.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	args := []string{"mcp", "call", "--tool", "raw", "--args", `{"command":["git","status"]}`, "--dry-run"}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/call_dry_run_traces_the_resolved_tool_call", normalize.Apply(result.Combined))
}

func testMCPCallWithoutAToolFailsInformatively(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"mcp", "call", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit without --tool, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "mcp/call_without_a_tool_fails_informatively", normalize.Apply(result.Combined))
}

func testMCPCallWithMalformedArgsFailsBeforeResolving(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	args := []string{"mcp", "call", "--tool", "raw", "--args", "not-json", "--dry-run"}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for malformed --args, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "mcp/call_with_malformed_args_fails_before_resolving", normalize.Apply(result.Combined))
}

func testMCPCallRealRunAgainstAJsonFramedEdge(t *testing.T) {
	// Real-run: the round-trip only happens past the dry-run gate. A fake edge
	// on the env's own local MCP port answers a plain JSON body, the framing
	// the deployed edge uses today.
	skipIfPortsBusy(t, mcpEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	identity := fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{Results: map[string]string{
		"tools/call": `{"content":[{"type":"text","text":"1.2.3"}],"structuredContent":{"version":"1.2.3"}}`,
	}}
	edge.start(t, mcpEdgeLocalPort)

	result := erun.Run(t, []string{"mcp", "call", "--tool", "version"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/call_real_run_against_a_json_framed_edge", normalize.Apply(result.Combined))

	// The bearer and the session id are request headers, not output, so the
	// snapshot cannot cover them: assert the edge saw a token it can verify and
	// that the call carried the session the handshake handed out.
	call := edge.requestFor(t, "tools/call")
	claims := verifyMCPToken(t, call.Authbearer, identity.PublicKey)
	if claims.Audience != "erun-mcp:team/dev" {
		t.Fatalf("call bearer audience = %q", claims.Audience)
	}
	if call.SessionID != "test-session" {
		t.Fatalf("call session id = %q, want test-session", call.SessionID)
	}
}

func testMCPCallRealRunAgainstAnSseFramedEdge(t *testing.T) {
	// Same call against an edge that frames its reply as an event stream, so
	// both framings the streamable-HTTP transport allows are locked.
	skipIfPortsBusy(t, mcpEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{SSE: true, Results: map[string]string{
		"tools/call": `{"content":[{"type":"text","text":"sse reply body"}]}`,
	}}
	edge.start(t, mcpEdgeLocalPort)

	result := erun.Run(t, []string{"mcp", "call", "--tool", "version"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/call_real_run_against_an_sse_framed_edge", normalize.Apply(result.Combined))
}

func testMCPCallRealRunJsonOutputCarriesTheStructuredResult(t *testing.T) {
	// --output json is the orchestrator contract: the tool's structured payload
	// must survive to stdout instead of being flattened into text.
	skipIfPortsBusy(t, mcpEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{Results: map[string]string{
		"tools/call": `{"content":[{"type":"text","text":"1.2.3"}],"structuredContent":{"version":"1.2.3"}}`,
	}}
	edge.start(t, mcpEdgeLocalPort)

	result := erun.Run(t, []string{"mcp", "call", "--tool", "version", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/call_real_run_json_output_carries_the_structured_result", normalize.Apply(result.Combined))
}

func testMCPCallRealRunSurfacesAToolError(t *testing.T) {
	// A tool that reports its own failure must exit non-zero with the tool's
	// message, distinct from a transport or auth failure.
	skipIfPortsBusy(t, mcpEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{Results: map[string]string{
		"tools/call": `{"content":[{"type":"text","text":"no such path"}],"isError":true}`,
	}}
	edge.start(t, mcpEdgeLocalPort)

	result := erun.Run(t, []string{"mcp", "call", "--tool", "raw"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a tool error, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "mcp/call_real_run_surfaces_a_tool_error", normalize.Apply(result.Combined))
}

func testMCPCallRealRunSurfacesAJsonrpcError(t *testing.T) {
	// An unknown tool fails at the protocol level, not inside a tool, so the
	// JSON-RPC error's code and message are what the operator must see.
	skipIfPortsBusy(t, mcpEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{RPCErrors: map[string]string{
		"tools/call": `{"code":-32602,"message":"unknown tool: nope"}`,
	}}
	edge.start(t, mcpEdgeLocalPort)

	result := erun.Run(t, []string{"mcp", "call", "--tool", "nope"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a JSON-RPC error, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "mcp/call_real_run_surfaces_a_jsonrpc_error", normalize.Apply(result.Combined))
}

func testMCPCallRealRunPrintsTheStructuredResultWhenAToolReturnsNoText(t *testing.T) {
	// Text mode must still show something useful for a tool whose result is
	// only structured content.
	skipIfPortsBusy(t, mcpEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{Results: map[string]string{
		"tools/call": `{"structuredContent":{"stopEligible":false}}`,
	}}
	edge.start(t, mcpEdgeLocalPort)

	result := erun.Run(t, []string{"mcp", "call", "--tool", "idle"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/call_real_run_prints_the_structured_result_when_a_tool_returns_no_text", normalize.Apply(result.Combined))
}

func testMCPCallRealRunUnauthorizedEdgePointsAtARedeploy(t *testing.T) {
	// An edge that rejects the bearer is a trust problem, not a connectivity
	// one: the error must say so and name the fix.
	skipIfPortsBusy(t, mcpEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{Status: http.StatusUnauthorized}
	edge.start(t, mcpEdgeLocalPort)

	result := erun.Run(t, []string{"mcp", "call", "--tool", "version"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an unauthorized edge, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "mcp/call_real_run_unauthorized_edge_points_at_a_redeploy", normalize.Apply(result.Combined))
}

func testMCPCallRealRunUnreachableEdgePointsAtOpen(t *testing.T) {
	// Nothing listening on the env's local MCP port means the port-forward is
	// missing; the fix is `erun open`, and the error must say that instead of
	// leaking a raw dial error. A one-shot reattach is attempted first (there
	// is no kubectl on this scrubbed PATH, so it fails fast rather than
	// hanging); the unreachable channel must still exit on the exit code
	// distinct from a plain error (mcpChannelUnreachableExitCode, 126 —
	// erun-cli/cmd/shared/environment_call.go), never a job/tool outcome's own code,
	// so a caller looping on this command cannot misread "channel down" as
	// "finished".
	skipIfPortsBusy(t, mcpEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)

	result := erun.Run(t, []string{"mcp", "call", "--tool", "version"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 126 {
		t.Fatalf("expected exit 126 (channel unreachable) for an unreachable edge, got %d:\n%s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/call_real_run_unreachable_edge_points_at_open", normalize.Apply(result.Combined))
}

func testMCPCallRealRunStaleForwardFailsFastInsteadOfHanging(t *testing.T) {
	// A stale kubectl port-forward keeps the local port bound and accepts
	// every connection, but never answers — the exact shape a dial-based
	// check cannot see, and the one that used to hang a call for its full
	// timeout (or forever, with none) instead of failing. The listener here
	// holds every connection open and never writes to it, so the only way
	// this scenario passes is if the client's own reachability probe — not
	// the caller's timeout — is what ends the wait.
	skipIfPortsBusy(t, mcpEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	startSilentPortForward(t, mcpEdgeLocalPort)

	result := erun.Run(t, []string{"mcp", "call", "--tool", "version"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 126 {
		t.Fatalf("expected exit 126 (channel unreachable) for a stale port-forward, got %d:\n%s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "held but the edge never answers") {
		t.Fatalf("expected the stale forward to be named as such, distinct from a missing one, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "erun open team dev") {
		t.Fatalf("expected the same recovery guidance a missing forward gets, got:\n%s", result.Combined)
	}
}

func testMCPCallRealRunUnreadyTargetIsNotSentToAReopen(t *testing.T) {
	// A forward whose target has not come up yet — an environment mid-roll
	// after a deploy — takes the local connection and closes it without
	// answering. The forward itself is working, so the recovery the stale
	// case prints ("run `erun open …`") would replace nothing and leave the
	// same wait ahead: the failure has to name the state it is actually in.
	skipIfPortsBusy(t, mcpEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	startDroppingPortForward(t, mcpEdgeLocalPort)

	result := erun.Run(t, []string{"mcp", "call", "--tool", "version"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 126 {
		t.Fatalf("expected exit 126 (channel unreachable) for a target that has not come up, got %d:\n%s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "still starting") {
		t.Fatalf("expected the unready target to be named as such, got:\n%s", result.Combined)
	}
	// Re-opening is what the stale shape prints, and it replaces nothing
	// while the forward is up and working.
	if strings.Contains(result.Combined, "so the local MCP port-forward is up") {
		t.Fatalf("an environment whose forward is up must not be sent to a re-open, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "if it stays unresponsive") {
		t.Fatalf("the unready target still needs a way out when it does not recover, got:\n%s", result.Combined)
	}
}

func testMCPCallRealRunRecoversWhenTheLocalForwardDropsMidRequest(t *testing.T) {
	// A flapping kubectl port-forward ("lost connection to pod", then a fresh
	// forward) drops a request that was already in flight. The remote MCP
	// server process itself never restarted — only the local tunnel blipped —
	// so the retry needs no re-handshake, just one resend once the tunnel
	// answers again, which is exactly what the still-live edge below proves.
	skipIfPortsBusy(t, mcpEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{DropFirstRequest: true, Results: map[string]string{
		"initialize": `{"protocolVersion":"2025-06-18","capabilities":{"tools":{}}}`,
	}}
	edge.start(t, mcpEdgeLocalPort)

	result := erun.Run(t, []string{"mcp", "call", "--tool", "version"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("expected the dropped first request to recover, got exit %d:\n%s", result.ExitCode, result.Combined)
	}
	// The dropped connection carries no readable request (it was hijacked and
	// closed before any body was parsed), so exactly one *readable* initialize
	// reaching the edge is what proves this succeeded via a plain retry, not
	// by the edge happening to answer the doomed connection after all.
	if handshakes := edge.requestsFor("initialize"); len(handshakes) != 1 {
		t.Fatalf("edge saw %d readable initialize requests, want exactly one (the retry): %+v", len(handshakes), edge.recorded())
	}
}

func testMCPCallRealRunALiveEdgeThatRefusesTheSessionPointsAtTheCli(t *testing.T) {
	// The reported failure: an environment whose edge is up and answering
	// refuses this client's session on every call. Read as an unreachable
	// endpoint it says the environment is down, and an orchestrator that
	// believes that stops dispatching to a live environment — the one reading
	// that costs capacity, since the CLI was reaching it throughout.
	skipIfPortsBusy(t, mcpEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{RPCErrors: map[string]string{
		"tools/call": `{"code":-32603,"message":"method \"tools/call\" is invalid during session initialization"}`,
	}}
	edge.start(t, mcpEdgeLocalPort)

	result := erun.Run(t, []string{"mcp", "call", "--tool", "version"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a session the edge never accepts, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "mcp/call_real_run_a_live_edge_that_refuses_the_session_points_at_the_cli", normalize.Apply(result.Combined))

	// Which side broke, named: the edge answered, so nothing about the
	// environment is in question.
	for _, want := range []string{"the MCP edge answered", "team/dev is up and dispatchable", "use the CLI"} {
		if !strings.Contains(result.Combined, want) {
			t.Fatalf("expected the message to contain %q, got:\n%s", want, result.Combined)
		}
	}
	// The path that keeps working, named. It is what makes the difference
	// between "wait for the environment" and "dispatch to it now".
	if !strings.Contains(result.Combined, "erun exec job start --tenant team --environment dev") {
		t.Fatalf("expected the CLI dispatch path to be spelled out, got:\n%s", result.Combined)
	}
	// The collapse the report described: an unreachable endpoint is a
	// port-forward problem, and this failure is not one.
	if strings.Contains(result.Combined, "so the local MCP port-forward is up") {
		t.Fatalf("a live edge must not be reported as an unreachable endpoint, got:\n%s", result.Combined)
	}
	// Bounded recovery: one re-handshake, then the truth.
	if handshakes := edge.requestsFor("initialize"); len(handshakes) != 2 {
		t.Fatalf("edge saw %d initialize requests, want the handshake plus exactly one retry: %+v", len(handshakes), edge.recorded())
	}
}

func testMCPToolsRealRunListsToolsWithTheirArguments(t *testing.T) {
	// Text mode must be usable for picking a tool: names, first description
	// line, and which arguments are required.
	skipIfPortsBusy(t, mcpEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{Results: map[string]string{
		"tools/list": `{"tools":[` +
			`{"name":"raw","description":"Run an argv in the runtime pod\nSecond line is dropped.","inputSchema":{"type":"object","properties":{"command":{"type":"array"},"verbosity":{"type":"integer"}},"required":["command"]}},` +
			`{"name":"version","description":"Report the runtime's erun version","inputSchema":{"type":"object","properties":{}}}` +
			`]}`,
	}}
	edge.start(t, mcpEdgeLocalPort)

	result := erun.Run(t, []string{"mcp", "tools"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/tools_real_run_lists_tools_with_their_arguments", normalize.Apply(result.Combined))
}

func testMCPToolsStructuredOutputCarriesTheDescriptorTheEdgeSent(t *testing.T) {
	// Structured output exists for callers that decide whether a tool is
	// safe to call before calling it, so it must carry the annotations and
	// protocol extensions the edge sent, not the subset the scannable text
	// rendering narrows to.
	skipIfPortsBusy(t, mcpEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{Results: map[string]string{
		"tools/list": `{"tools":[` +
			`{"_meta":{"family":"cloud","mcpOnly":true},` +
			`"annotations":{"destructiveHint":true,"idempotentHint":false,"openWorldHint":true,"readOnlyHint":false},` +
			`"description":"Clear the AWS credentials delivered to this environment.",` +
			`"inputSchema":{"type":"object","properties":{}},` +
			`"name":"cloud_clear_aws_credentials",` +
			`"outputSchema":{"type":"object","properties":{"cleared":{"type":"boolean"}}},` +
			`"title":"Clear AWS credentials"}` +
			`]}`,
	}}
	edge.start(t, mcpEdgeLocalPort)

	result := erun.Run(t, []string{"mcp", "tools", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	var listed, want struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &listed); err != nil {
		t.Fatalf("decode tools JSON output: %v\n%s", err, result.Stdout)
	}
	if err := json.Unmarshal([]byte(edge.Results["tools/list"]), &want); err != nil {
		t.Fatalf("decode the edge's own tools/list payload: %v", err)
	}
	if len(listed.Tools) != len(want.Tools) {
		t.Fatalf("structured output listed %d tools, want %d: %s", len(listed.Tools), len(want.Tools), result.Stdout)
	}
	// Whole descriptors, so a field the protocol gains later fails here
	// rather than disappearing from the structured surface unnoticed.
	if !reflect.DeepEqual(listed.Tools, want.Tools) {
		t.Errorf("structured output does not carry the descriptor the edge sent\n got: %v\nwant: %v", listed.Tools, want.Tools)
	}
}
