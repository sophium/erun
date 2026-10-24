package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func proxyStdin(messages ...string) string {
	return strings.Join(messages, "\n") + "\n"
}

// requireJSONRPCLines checks stdout alone: one stray byte desynchronizes the
// client, and Combined cannot tell stdout from stderr.
func requireJSONRPCLines(t *testing.T, stdout string, want int) []map[string]any {
	t.Helper()
	if stdout == "" && want == 0 {
		return nil
	}
	if !strings.HasSuffix(stdout, "\n") {
		t.Fatalf("proxy stdout does not end in a newline: %q", stdout)
	}
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	messages := make([]map[string]any, 0, len(lines))
	for index, line := range lines {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			t.Fatalf("proxy stdout line %d is not JSON-RPC (%v): %q", index+1, err, line)
		}
		messages = append(messages, decoded)
	}
	if len(messages) != want {
		t.Fatalf("proxy wrote %d stdout lines, want %d:\n%s", len(messages), want, stdout)
	}
	return messages
}

func jsonRPCErrorMessage(t *testing.T, message map[string]any) string {
	t.Helper()
	failure, ok := message["error"].(map[string]any)
	if !ok {
		t.Fatalf("reply carries no JSON-RPC error: %v", message)
	}
	text, _ := failure["message"].(string)
	return text
}

func testMCPProxyDryRunTracesTheResolvedEdge(t *testing.T) {
	t.Parallel()
	// The plan must neither read stdin nor touch the network.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"mcp", "proxy", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/proxy_dry_run_traces_the_resolved_edge", normalize.Apply(result.Combined))
}

func testMCPProxyRealRunRelaysAJsonFramedSession(t *testing.T) {
	// The notification produces no stdout line, and tools/list carries the
	// handshake's session.
	skipIfPortsBusy(t, mcpEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	identity := fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{Results: map[string]string{
		"initialize": `{"protocolVersion":"2025-06-18","capabilities":{"tools":{}}}`,
		"tools/list": `{"tools":[{"name":"version","description":"Report the runtime's erun version"}]}`,
	}}
	edge.start(t, mcpEdgeLocalPort)

	stdin := proxyStdin(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	)
	result := erun.Run(t, []string{"mcp", "proxy", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: stdin})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/proxy_real_run_relays_a_json_framed_session", normalize.Apply(result.Combined))

	replies := requireJSONRPCLines(t, result.Stdout, 2)
	if replies[0]["id"] != float64(1) || replies[1]["id"] != float64(2) {
		t.Fatalf("replies came back on the wrong ids: %v", replies)
	}
	list := edge.requestFor(t, "tools/list")
	if list.SessionID != "test-session" {
		t.Fatalf("tools/list session id = %q, want test-session", list.SessionID)
	}
	// Each request carries a freshly minted, verifiable bearer so a session
	// outlives token lifetime. Same-second mints are byte-equal.
	for _, request := range []fakeMCPRequest{edge.requestFor(t, "initialize"), list} {
		claims := verifyMCPToken(t, request.Authbearer, identity.PublicKey)
		if claims.Audience != "erun-mcp:team/dev" {
			t.Fatalf("%s bearer audience = %q", request.Method, claims.Audience)
		}
	}
}

func testMCPProxyRealRunRelaysAnSseFramedReply(t *testing.T) {
	skipIfPortsBusy(t, mcpEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{SSE: true, Results: map[string]string{
		"tools/call": `{"content":[{"type":"text","text":"1.2.3"}]}`,
	}}
	edge.start(t, mcpEdgeLocalPort)

	stdin := proxyStdin(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"version","arguments":{}}}`)
	result := erun.Run(t, []string{"mcp", "proxy", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: stdin})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/proxy_real_run_relays_an_sse_framed_reply", normalize.Apply(result.Combined))
	requireJSONRPCLines(t, result.Stdout, 1)
}

func testMCPProxyRealRunWritesNothingForANotification(t *testing.T) {
	// A notification has no id; any reply would desync the client.
	skipIfPortsBusy(t, mcpEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{}
	edge.start(t, mcpEdgeLocalPort)

	stdin := proxyStdin(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":3}}`)
	result := erun.Run(t, []string{"mcp", "proxy", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: stdin})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/proxy_real_run_writes_nothing_for_a_notification", normalize.Apply(result.Combined))
	requireJSONRPCLines(t, result.Stdout, 0)
	edge.requestFor(t, "notifications/cancelled")
}

func testMCPProxyRealRunUnreachableEdgeAnswersEveryRequestAndKeepsServing(t *testing.T) {
	// Each request gets an error naming `erun open`, and later requests are still
	// served in case the forward returns.
	skipIfPortsBusy(t, mcpEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)

	stdin := proxyStdin(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	)
	result := erun.Run(t, []string{"mcp", "proxy", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: stdin})
	if result.ExitCode != 0 {
		t.Fatalf("expected the relay to keep serving and exit 0, got %d:\n%s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/proxy_real_run_unreachable_edge_answers_every_request_and_keeps_serving", normalize.Apply(result.Combined))

	replies := requireJSONRPCLines(t, result.Stdout, 2)
	for index, reply := range replies {
		if message := jsonRPCErrorMessage(t, reply); !strings.Contains(message, "erun open team dev") {
			t.Fatalf("reply %d does not name the port-forward recovery: %q", index+1, message)
		}
	}
}

func testMCPProxyRealRunUnauthorizedEdgeAnswersWithTheRedeployRecovery(t *testing.T) {
	skipIfPortsBusy(t, mcpEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{Status: http.StatusUnauthorized}
	edge.start(t, mcpEdgeLocalPort)

	// Unterminated on purpose: the last message without a newline must still be relayed.
	stdin := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	result := erun.Run(t, []string{"mcp", "proxy", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: stdin})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/proxy_real_run_unauthorized_edge_answers_with_the_redeploy_recovery", normalize.Apply(result.Combined))

	replies := requireJSONRPCLines(t, result.Stdout, 1)
	if message := jsonRPCErrorMessage(t, replies[0]); !strings.Contains(message, "redeploy it from the ERun desktop app") {
		t.Fatalf("reply does not name the trust recovery: %q", message)
	}
}

func testMCPProxyRealRunRecoversWhenTheEdgeForgetsItsSession(t *testing.T) {
	// A forgotten session costs one transparent re-handshake.
	skipIfPortsBusy(t, mcpEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{ForgetSessionOnce: true, Results: map[string]string{
		"initialize": `{"protocolVersion":"2025-06-18","capabilities":{"tools":{}}}`,
		"tools/list": `{"tools":[{"name":"version","description":"Report the runtime's erun version"}]}`,
	}}
	edge.start(t, mcpEdgeLocalPort)

	stdin := proxyStdin(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	)
	result := erun.Run(t, []string{"mcp", "proxy", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: stdin})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/proxy_real_run_recovers_when_the_edge_forgets_its_session", normalize.Apply(result.Combined))

	replies := requireJSONRPCLines(t, result.Stdout, 2)
	if _, failed := replies[1]["error"]; failed {
		t.Fatalf("tools/list came back as an error after the recovery: %v", replies[1])
	}
	if replies[1]["id"] != float64(2) {
		t.Fatalf("tools/list reply came back on the wrong id: %v", replies[1])
	}
	if handshakes := edge.requestsFor("initialize"); len(handshakes) != 2 {
		t.Fatalf("edge saw %d initialize requests, want exactly one re-handshake: %+v", len(handshakes), edge.recorded())
	}
	lists := edge.requestsFor("tools/list")
	if len(lists) != 2 {
		t.Fatalf("edge saw %d tools/list requests, want the original plus one replay: %+v", len(lists), edge.recorded())
	}
	if lists[0].SessionID != "test-session" || lists[1].SessionID != "test-session-2" {
		t.Fatalf("tools/list sessions = %q then %q, want the dead session then the re-established one", lists[0].SessionID, lists[1].SessionID)
	}
}

func testMCPProxyRealRunSurfacesASessionTheEdgeNeverAccepts(t *testing.T) {
	// Recovery is bounded: no endless re-handshake loop.
	skipIfPortsBusy(t, mcpEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{ForgetSessionAlways: true, Results: map[string]string{
		"initialize": `{"protocolVersion":"2025-06-18","capabilities":{"tools":{}}}`,
	}}
	edge.start(t, mcpEdgeLocalPort)

	stdin := proxyStdin(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	)
	result := erun.Run(t, []string{"mcp", "proxy", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: stdin})
	if result.ExitCode != 0 {
		t.Fatalf("expected the relay to keep serving and exit 0, got %d:\n%s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/proxy_real_run_surfaces_a_session_the_edge_never_accepts", normalize.Apply(result.Combined))

	replies := requireJSONRPCLines(t, result.Stdout, 2)
	if message := jsonRPCErrorMessage(t, replies[1]); !strings.Contains(message, "re-establish the MCP session") {
		t.Fatalf("reply does not say the session could not be re-established: %q", message)
	}
	if handshakes := edge.requestsFor("initialize"); len(handshakes) != 2 {
		t.Fatalf("edge saw %d initialize requests, want the client's plus exactly one retry: %+v", len(handshakes), edge.recorded())
	}
}

func testMCPProxyRealRunAnswersARequestTheEdgeLeftUnanswered(t *testing.T) {
	// A bodyless accept must be answered with an error, not hang the client.
	skipIfPortsBusy(t, mcpEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{AcceptEverything: true}
	edge.start(t, mcpEdgeLocalPort)

	stdin := proxyStdin(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	result := erun.Run(t, []string{"mcp", "proxy", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: stdin})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "mcp/proxy_real_run_answers_a_request_the_edge_left_unanswered", normalize.Apply(result.Combined))

	replies := requireJSONRPCLines(t, result.Stdout, 1)
	if message := jsonRPCErrorMessage(t, replies[0]); !strings.Contains(message, "returned no reply") {
		t.Fatalf("reply does not say the edge answered without one: %q", message)
	}
}

func testMCPProxyRealRunServesInputsUploadLocally(t *testing.T) {
	t.Parallel()
	// No fake edge: inputs_upload is host-served and must not touch the network.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	local := filepath.Join(setup.Cwd, "evidence.bin")
	if err := os.WriteFile(local, []byte("hello"), 0o644); err != nil {
		t.Fatalf("seed local file: %v", err)
	}
	stubs := setup.Cwd + "/stubs"
	stubKubectlUploadAccepts(t, stubs, 5, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)

	localJSON, err := json.Marshal(local)
	if err != nil {
		t.Fatalf("marshal local path: %v", err)
	}
	stdin := proxyStdin(fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"inputs_upload","arguments":{"localPath":%s,"remotePath":"/home/erun/.erun/outputs/evidence.bin"}}}`,
		string(localJSON)))
	result := erun.Run(t, []string{"mcp", "proxy", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars, Stdin: stdin})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}

	replies := requireJSONRPCLines(t, result.Stdout, 1)
	resultField, ok := replies[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("reply carries no result: %v", replies[0])
	}
	content, ok := resultField["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("reply result carries no content: %v", resultField)
	}
	first, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("reply content[0] is not an object: %v", content[0])
	}
	text, _ := first["text"].(string)
	if !strings.Contains(text, "sha256 2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824") {
		t.Fatalf("reply text does not report the verified checksum: %q", text)
	}
	if !strings.Contains(text, "/home/erun/.erun/outputs/evidence.bin") {
		t.Fatalf("reply text does not name the remote destination: %q", text)
	}
}

func testMCPProxyRealRunPreviewsInputsUploadWithoutSending(t *testing.T) {
	t.Parallel()
	// No kubectl stub, so a real call would fail.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	local := filepath.Join(setup.Cwd, "evidence.bin")
	if err := os.WriteFile(local, []byte("hello"), 0o644); err != nil {
		t.Fatalf("seed local file: %v", err)
	}

	localJSON, err := json.Marshal(local)
	if err != nil {
		t.Fatalf("marshal local path: %v", err)
	}
	stdin := proxyStdin(fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"inputs_upload","arguments":{"localPath":%s,"remotePath":"/home/erun/.erun/outputs/evidence.bin","preview":true}}}`,
		string(localJSON)))
	result := erun.Run(t, []string{"mcp", "proxy", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: stdin})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}

	replies := requireJSONRPCLines(t, result.Stdout, 1)
	resultField, ok := replies[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("reply carries no result: %v", replies[0])
	}
	content, _ := resultField["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("reply result carries no content: %v", resultField)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	if !strings.Contains(text, "inputs: uploading") {
		t.Fatalf("preview text does not describe the resolved transfer: %q", text)
	}
	if !strings.Contains(text, "kubectl") {
		t.Fatalf("preview text does not trace the kubectl command that would run: %q", text)
	}
}
