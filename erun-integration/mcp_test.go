package integration

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// The MCP edge scenarios pin the env's local port range to 26100 (same rule as
// the real-run open scenarios) so the fake edge never collides with a
// developer's live erun session on the default 17000 range.
const mcpEdgeLocalPort = 26100

// fakeMCPEdge stands in for the per-env erun-mcp server: it speaks the
// streamable-HTTP handshake (initialize, then a 202 for the initialized
// notification, then the call) and records what each request carried, so a
// scenario can assert the client minted a bearer and propagated the session id.
type fakeMCPEdge struct {
	// SSE frames replies as an event stream instead of a plain JSON body; the
	// edge does one or the other depending on how it was configured, so both
	// framings must round-trip.
	SSE bool
	// Status, when set, answers every POST with that status instead of a reply.
	Status int
	// AcceptEverything answers even a request carrying an id with a bodyless 202,
	// the protocol anomaly that leaves a client waiting on a reply that is never
	// coming.
	AcceptEverything bool
	// Results maps a JSON-RPC method to the raw JSON result it answers with.
	Results map[string]string
	// RPCErrors maps a JSON-RPC method to a raw JSON-RPC error object, the
	// protocol-level failure a tool-level isError does not cover.
	RPCErrors map[string]string
	// ForgetSessionOnce answers 404 for the first request carrying a session id,
	// the way an edge that restarted inside a still-running pod does; the next
	// handshake hands out a live session again.
	ForgetSessionOnce bool
	// ForgetSessionAlways never accepts a session id, so a client that keeps
	// re-handshaking would loop forever.
	ForgetSessionAlways bool

	mu         sync.Mutex
	requests   []fakeMCPRequest
	handshakes int
	forgot     bool
}

type fakeMCPRequest struct {
	Method     string
	Authbearer string
	SessionID  string
}

func (e *fakeMCPEdge) start(t *testing.T, port int) {
	t.Helper()
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listen on 127.0.0.1:%d: %v", port, err)
	}
	server := httptest.NewUnstartedServer(e)
	_ = server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
}

func (e *fakeMCPEdge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	_ = json.Unmarshal(body, &request)

	e.mu.Lock()
	e.requests = append(e.requests, fakeMCPRequest{
		Method:     request.Method,
		Authbearer: strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "),
		SessionID:  r.Header.Get("Mcp-Session-Id"),
	})
	e.mu.Unlock()

	if e.Status != 0 {
		http.Error(w, "edge refused the request", e.Status)
		return
	}
	if r.Header.Get("Mcp-Session-Id") != "" && e.forgetSession() {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if request.Method == "initialize" {
		e.newSession()
	}
	w.Header().Set("Mcp-Session-Id", e.sessionID())
	if len(request.ID) == 0 || e.AcceptEverything {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	reply := ""
	if rpcError, ok := e.RPCErrors[request.Method]; ok {
		reply = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":%s}`, request.ID, rpcError)
	} else {
		result, ok := e.Results[request.Method]
		if !ok {
			result = "{}"
		}
		reply = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, request.ID, result)
	}
	if e.SSE {
		w.Header().Set("Content-Type", "text/event-stream")
		// A progress notification ahead of the reply proves the client skips
		// non-reply events instead of failing on the first data line.
		_, _ = fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{}}\n\n")
		_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", reply)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintln(w, reply)
}

func (e *fakeMCPEdge) forgetSession() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ForgetSessionAlways {
		return true
	}
	if e.ForgetSessionOnce && !e.forgot {
		e.forgot = true
		return true
	}
	return false
}

func (e *fakeMCPEdge) newSession() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handshakes++
}

// The first handshake keeps the historical id so the scenarios that only ever
// shake hands once stay readable; a later one hands out a distinct id, which is
// how a replay can be told apart from a request on the dead session.
func (e *fakeMCPEdge) sessionID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.handshakes > 1 {
		return fmt.Sprintf("test-session-%d", e.handshakes)
	}
	return "test-session"
}

// requestsFor returns every recorded request for a method in arrival order, so a
// scenario can prove a recovery cost exactly one extra handshake and that the
// replay went out on the new session.
func (e *fakeMCPEdge) requestsFor(method string) []fakeMCPRequest {
	matched := make([]fakeMCPRequest, 0, 2)
	for _, request := range e.recorded() {
		if request.Method == method {
			matched = append(matched, request)
		}
	}
	return matched
}

func (e *fakeMCPEdge) recorded() []fakeMCPRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]fakeMCPRequest(nil), e.requests...)
}

// requestFor returns the recorded request for a method, failing the scenario when
// the client never sent it.
func (e *fakeMCPEdge) requestFor(t *testing.T, method string) fakeMCPRequest {
	t.Helper()
	for _, request := range e.recorded() {
		if request.Method == method {
			return request
		}
	}
	t.Fatalf("edge never received %s; got %+v", method, e.recorded())
	return fakeMCPRequest{}
}

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

// proxyStdin frames JSON-RPC messages the way an MCP client writes them: one
// per line, newline-delimited, then EOF.
func proxyStdin(messages ...string) string {
	return strings.Join(messages, "\n") + "\n"
}

// requireJSONRPCLines is the proxy's stdout contract, and the one thing the
// Combined snapshot cannot assert: it concatenates both streams, so it cannot
// tell a diagnostic that leaked onto stdout from one that went to stderr where it
// belongs. A single stray byte on stdout desynchronizes the client's parser, so
// every line is parsed here and the decoded messages are returned for the
// scenario to assert against.
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

// jsonRPCErrorMessage returns a reply's error message, failing when the reply
// carries no error at all.
func jsonRPCErrorMessage(t *testing.T, message map[string]any) string {
	t.Helper()
	failure, ok := message["error"].(map[string]any)
	if !ok {
		t.Fatalf("reply carries no JSON-RPC error: %v", message)
	}
	text, _ := failure["message"].(string)
	return text
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
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"mcp", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "mcp/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_traces_emcp_launch", func(t *testing.T) {
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
	})

	t.Run("dry_run_uses_environment_local_port_by_default", func(t *testing.T) {
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
	})

	t.Run("real_run_launches_emcp_stub", func(t *testing.T) {
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
	})

	t.Run("real_run_errors_when_emcp_missing", func(t *testing.T) {
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
	})

	t.Run("real_run_propagates_emcp_exit_failure", func(t *testing.T) {
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
	})

	t.Run("call_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"mcp", "call", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "mcp/call_help", normalize.Apply(result.Combined))
	})

	t.Run("tools_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"mcp", "tools", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "mcp/tools_help", normalize.Apply(result.Combined))
	})

	t.Run("proxy_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"mcp", "proxy", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "mcp/proxy_help", normalize.Apply(result.Combined))
	})

	t.Run("token_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"mcp", "token", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "mcp/token_help", normalize.Apply(result.Combined))
	})

	t.Run("call_dry_run_traces_the_resolved_tool_call", func(t *testing.T) {
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
	})

	t.Run("call_without_a_tool_fails_informatively", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"mcp", "call", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without --tool, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "mcp/call_without_a_tool_fails_informatively", normalize.Apply(result.Combined))
	})

	t.Run("call_with_malformed_args_fails_before_resolving", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		args := []string{"mcp", "call", "--tool", "raw", "--args", "not-json", "--dry-run"}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for malformed --args, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "mcp/call_with_malformed_args_fails_before_resolving", normalize.Apply(result.Combined))
	})

	t.Run("token_dry_run_traces_the_audience", func(t *testing.T) {
		// The audience is the whole point of the token, so the plan names it; no
		// identity is read in dry-run.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"mcp", "token", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "mcp/token_dry_run_traces_the_audience", normalize.Apply(result.Combined))
	})

	t.Run("token_without_a_desktop_identity_says_where_it_comes_from", func(t *testing.T) {
		// No identity on this machine: the error must point at the desktop app
		// rather than mint a key no deployed environment would trust.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"mcp", "token"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without a desktop identity, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "mcp/token_without_a_desktop_identity_says_where_it_comes_from", normalize.Apply(result.Combined))
	})

	t.Run("token_real_run_mints_a_verifiable_bearer", func(t *testing.T) {
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
	})

	t.Run("call_real_run_against_a_json_framed_edge", func(t *testing.T) {
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
	})

	t.Run("call_real_run_against_an_sse_framed_edge", func(t *testing.T) {
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
	})

	t.Run("call_real_run_json_output_carries_the_structured_result", func(t *testing.T) {
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
	})

	t.Run("call_real_run_surfaces_a_tool_error", func(t *testing.T) {
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
	})

	t.Run("call_real_run_surfaces_a_jsonrpc_error", func(t *testing.T) {
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
	})

	t.Run("call_real_run_prints_the_structured_result_when_a_tool_returns_no_text", func(t *testing.T) {
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
	})

	t.Run("call_real_run_unauthorized_edge_points_at_a_redeploy", func(t *testing.T) {
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
	})

	t.Run("call_real_run_unreachable_edge_points_at_open", func(t *testing.T) {
		// Nothing listening on the env's local MCP port means the port-forward is
		// missing; the fix is `erun open`, and the error must say that instead of
		// leaking a raw dial error.
		skipIfPortsBusy(t, mcpEdgeLocalPort)
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
		fixture.SeedDesktopIdentity(t, setup)

		result := erun.Run(t, []string{"mcp", "call", "--tool", "version"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for an unreachable edge, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "mcp/call_real_run_unreachable_edge_points_at_open", normalize.Apply(result.Combined))
	})

	t.Run("proxy_dry_run_traces_the_resolved_edge", func(t *testing.T) {
		// The plan must name the edge the relay would reach, and must neither read
		// stdin nor touch the network.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"mcp", "proxy", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "mcp/proxy_dry_run_traces_the_resolved_edge", normalize.Apply(result.Combined))
	})

	t.Run("proxy_real_run_relays_a_json_framed_session", func(t *testing.T) {
		// The whole session an MCP client drives: initialize, the initialized
		// notification, then tools/list. The notification must produce no stdout
		// line at all, and the tools/list must carry the session the handshake
		// handed out — the client never sees either fact, so the relay owns both.
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
		// Every relayed request carries its own freshly minted bearer — the reason
		// a session outlives the 5-minute token lifetime. The headers are in
		// neither stream, so the snapshot cannot see them. (Two mints inside the
		// same second sign identical claims, so the bearers are byte-equal here;
		// what is assertable is that each request carried a verifiable one.)
		for _, request := range []fakeMCPRequest{edge.requestFor(t, "initialize"), list} {
			claims := verifyMCPToken(t, request.Authbearer, identity.PublicKey)
			if claims.Audience != "erun-mcp:team/dev" {
				t.Fatalf("%s bearer audience = %q", request.Method, claims.Audience)
			}
		}
	})

	t.Run("proxy_real_run_relays_an_sse_framed_reply", func(t *testing.T) {
		// The edge may frame a reply as an event stream; the client only speaks
		// newline-delimited JSON, so the relay must unwrap the data: framing and
		// drop the progress event ahead of it.
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
	})

	t.Run("proxy_real_run_writes_nothing_for_a_notification", func(t *testing.T) {
		// A notification has no id, so the protocol has nothing to answer it with.
		// Writing anything here would leave the client one reply ahead of itself.
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
	})

	t.Run("proxy_real_run_unreachable_edge_answers_every_request_and_keeps_serving", func(t *testing.T) {
		// Nothing listening means the port-forward is missing. A relay that went
		// quiet here would look to the client like a hung server, so each request
		// gets a JSON-RPC error naming `erun open`, and the next one is still
		// served — the port-forward can come back mid-session.
		skipIfPortsBusy(t, mcpEdgeLocalPort)
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
		fixture.SeedDesktopIdentity(t, setup)

		// The notification in the middle still has no id to answer against, so the
		// outage reaches stderr only and stdout stays at one line per request.
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
	})

	t.Run("proxy_real_run_unauthorized_edge_answers_with_the_redeploy_recovery", func(t *testing.T) {
		// An edge that rejects the bearer is a trust problem, not connectivity, and
		// the client must see that distinction rather than a generic failure.
		skipIfPortsBusy(t, mcpEdgeLocalPort)
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpEdgeLocalPort)
		fixture.SeedDesktopIdentity(t, setup)
		edge := &fakeMCPEdge{Status: http.StatusUnauthorized}
		edge.start(t, mcpEdgeLocalPort)

		// Deliberately unterminated: a client that closes the pipe right after its
		// last message leaves no trailing newline, and that message must still be
		// relayed rather than dropped.
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
	})

	t.Run("proxy_real_run_recovers_when_the_edge_forgets_its_session", func(t *testing.T) {
		// The relay pins one session id for its whole lifetime, so an edge that
		// restarts inside a still-running pod would otherwise 404 every later
		// request and leave the client with no tools at all. A forgotten session
		// must cost one transparent re-handshake and nothing else: the client still
		// sees one reply per request, on its own ids.
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
		// Session ids are request headers, so the snapshot cannot see them: the
		// retry must have gone out on the session the second handshake handed out.
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
	})

	t.Run("proxy_real_run_surfaces_a_session_the_edge_never_accepts", func(t *testing.T) {
		// The recovery is bounded: an edge that rejects every session must produce
		// one JSON-RPC error against the request the client is waiting on, not an
		// endless re-handshake loop.
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
	})

	t.Run("proxy_real_run_answers_a_request_the_edge_left_unanswered", func(t *testing.T) {
		// An edge that accepts a request and returns no body would leave the client
		// waiting forever on a reply. The relay closes that hole: the request is
		// answered with an error naming what happened, so the client fails visibly
		// instead of hanging.
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
	})

	t.Run("tools_real_run_lists_tools_with_their_arguments", func(t *testing.T) {
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
	})
}
