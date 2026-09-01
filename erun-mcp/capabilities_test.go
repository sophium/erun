package erunmcp

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// connectWithCapabilities serves the tool surface a caller with these
// capabilities would get, and returns a connected session. It bypasses the auth
// middleware deliberately: what is under test is the surface built for an
// already-resolved caller, not the token parsing that resolves one.
func connectWithCapabilities(t *testing.T, capabilities ...string) *mcp.ClientSession {
	t.Helper()
	for _, key := range []string{envMCPTrustedIssuers, envMCPTrustedIssuer, envMCPAudience, envTenant} {
		t.Setenv(key, "")
	}
	identity := authIdentity{
		Tenant:       "acme",
		User:         "operator@acme",
		Capabilities: eruncommon.NewMCPCapabilitySet(capabilities),
	}
	info := eruncommon.BuildInfo{Version: "1.2.3"}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return newServer(info, RuntimeConfig{}, identity, nil)
	}, &mcp.StreamableHTTPOptions{JSONResponse: true})

	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "capability-test", Version: "v0.0.1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             httpServer.URL,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func listToolNames(t *testing.T, session *mcp.ClientSession) []string {
	t.Helper()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

// tools/list must be the same answer as what the caller may do. A menu that
// lists tools the caller cannot call teaches it about a surface it has no
// business knowing, and invites an error where there should be none.
func TestReadCapabilitySeesOnlyTheReadTools(t *testing.T) {
	got := listToolNames(t, connectWithCapabilities(t, string(eruncommon.MCPCapabilityRead)))

	// diff/exec_diff and the job_*/exec_job_* pairs both appear during their
	// rename's one-release alias window (#1186, #1246); the retired name
	// authorizes as its replacement, so a read-only caller keeps it.
	want := []string{
		"activity_lease_list", "ai_sessions", "cloud_list", "context_list", "diff", "environment", "exec_diff",
		"exec_job_await", "exec_job_output", "exec_job_status",
		"idle", "idle_stop_history",
		"job_await", "job_output", "job_status", "list", "observe",
		"outputs_download", "outputs_list", "review_list", "review_queue_list", "review_show", "usage", "version",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("read capability exposed %v, want %v", got, want)
	}
}

// The tools that matter most: remote execution and everything that mutates.
func TestReadCapabilityCannotSeeExecutionOrMutation(t *testing.T) {
	got := listToolNames(t, connectWithCapabilities(t, string(eruncommon.MCPCapabilityRead)))

	for _, forbidden := range []string{"raw", "write", "commit", "deploy", "delete", "build", "push", "release", "context_init", "exec_agent", "exec_job_cancel"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("a read-only caller must not be offered %q: %v", forbidden, got)
		}
	}
}

func TestAdminCapabilitySeesEveryTool(t *testing.T) {
	admin := listToolNames(t, connectWithCapabilities(t, string(eruncommon.MCPCapabilityAdmin)))
	read := listToolNames(t, connectWithCapabilities(t, string(eruncommon.MCPCapabilityRead)))

	if len(admin) <= len(read) {
		t.Fatalf("admin must expose more than read: admin=%d read=%d", len(admin), len(read))
	}
	for _, required := range []string{"raw", "deploy", "delete", "version", "list"} {
		if !slices.Contains(admin, required) {
			t.Fatalf("admin is missing %q: %v", required, admin)
		}
	}
	// Read is a subset, not a different set.
	for _, tool := range read {
		if !slices.Contains(admin, tool) {
			t.Fatalf("admin should include every read tool, missing %q", tool)
		}
	}
}

// Calling a tool outside the caller's capabilities fails. It is unknown rather
// than forbidden, because it was never registered for this caller.
func TestCallingAToolOutsideTheCapabilityFails(t *testing.T) {
	session := connectWithCapabilities(t, string(eruncommon.MCPCapabilityRead))

	_, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "raw"})
	if err == nil {
		t.Fatal("a read-only caller must not be able to call raw")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "raw") {
		t.Fatalf("the error should name the tool that was refused, got %v", err)
	}
}

// A read-only caller must still be able to do what it is for.
func TestReadCapabilityCanStillCallAReadTool(t *testing.T) {
	session := connectWithCapabilities(t, string(eruncommon.MCPCapabilityRead))

	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "version"}); err != nil {
		t.Fatalf("a read capability must permit version: %v", err)
	}
}

// environment and ai_sessions are the authenticated-edge read model erun#1105
// needs a scoped mobile caller to reach; both must actually be reachable, not
// just listed. Calling either with no tenant/environment context resolves to
// a business error naming the missing target, not an authorization refusal --
// proof the call reached the handler rather than being turned away at
// registration.
func TestReadCapabilityCanReachTheEnvironmentReadModelTools(t *testing.T) {
	session := connectWithCapabilities(t, string(eruncommon.MCPCapabilityRead))

	for _, tool := range []string{"environment", "ai_sessions"} {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tool})
		if err != nil {
			t.Fatalf("a read capability must be allowed to call %s, got a transport error: %v", tool, err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("%s with no tenant/environment context should report a business error, got %+v", tool, result)
		}
	}
}

// An edge with no trust anchor is the loopback-only deployment that predates
// authentication. Turning authentication off must not quietly turn
// authorization on and strand that deployment with an empty tool set.
func TestAnUnauthenticatedEdgeKeepsTheCoarseAdminSurface(t *testing.T) {
	identity := authIdentityFrom(context.Background())
	if !identity.Capabilities.AllowsTool("raw") {
		t.Fatalf("an unauthenticated edge keeps its existing surface, got %+v", identity.Capabilities)
	}
}

// The guard is defence in depth: registration already filtered, so this only
// matters if a handler is reached another way — exactly when a check that
// relied on registration would have been useless.
func TestGuardRefusesEvenWhenAHandlerIsReachedDirectly(t *testing.T) {
	identity := authIdentity{
		Tenant:       "acme",
		Capabilities: eruncommon.NewMCPCapabilitySet([]string{string(eruncommon.MCPCapabilityRead)}),
	}
	called := false
	handler := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
		called = true
		return nil, struct{}{}, nil
	}

	guarded := guardTool(identity, "raw", nil, handler)
	if _, _, err := guarded(context.Background(), nil, struct{}{}); err == nil {
		t.Fatal("the guard must refuse a tool outside the caller's capabilities")
	}
	if called {
		t.Fatal("a refused tool must not run its handler")
	}
}

// captureAuditLog redirects the standard logger, which auditToolDecision
// writes to, into a buffer for the duration of the test. log.SetOutput is
// process-global, so the previous writer is restored on cleanup (see
// erun-ui's restoreLogOutputAfter for the same pattern); the standard
// logger serializes concurrent writers internally, so concurrent callers may
// safely log through it while the test holds no lock of its own.
func captureAuditLog(t *testing.T) *strings.Builder {
	t.Helper()
	var out strings.Builder
	prev := log.Writer()
	log.SetOutput(&out)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &out
}

// The closure guardTool returns is built once per tool registration and
// reused for every call to that tool, so the captured identity must not
// become shared mutable state across concurrent callers. Each of many
// concurrent callers, released at the same instant, must see its own audit
// line -- not another caller's, and not a mix.
func TestGuardToolAttributesEachConcurrentCallToItsOwnCaller(t *testing.T) {
	out := captureAuditLog(t)

	capabilities := eruncommon.NewMCPCapabilitySet([]string{string(eruncommon.MCPCapabilityRead)})
	handler := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
		return nil, struct{}{}, nil
	}
	guarded := guardTool(authIdentity{Tenant: "acme", User: "registration-time", Capabilities: capabilities}, "list", nil, handler)

	const callers = 200
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			defer wg.Done()
			ctx := withAuthIdentity(context.Background(), authIdentity{
				Tenant:       "acme",
				User:         fmt.Sprintf("user-%d", i),
				Capabilities: capabilities,
			})
			<-start
			if _, _, err := guarded(ctx, nil, struct{}{}); err != nil {
				t.Errorf("call %d: unexpected refusal: %v", i, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	seen := make(map[string]int, callers)
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		idx := strings.Index(line, "user=")
		if idx < 0 {
			t.Fatalf("audit line has no user field: %q", line)
		}
		field := line[idx+len("user="):]
		if sp := strings.IndexByte(field, ' '); sp >= 0 {
			field = field[:sp]
		}
		seen[field]++
	}
	for i := 0; i < callers; i++ {
		want := fmt.Sprintf("user-%d", i)
		if seen[want] != 1 {
			t.Fatalf("caller %q was audited %d times, want exactly 1 (audit log: %s)", want, seen[want], out.String())
		}
	}
}

// A call with no live auth context in it (the loopback-only edge, or a
// misrouted request) must not be audited as whoever called previously. The
// captured parameter starts at "registration-time" and is never touched by a
// call that carries no identity, so it must still read that way after a
// live caller has been through the same closure.
func TestGuardToolWithNoAuthContextDoesNotInheritThePreviousCaller(t *testing.T) {
	out := captureAuditLog(t)

	capabilities := eruncommon.NewMCPCapabilitySet([]string{string(eruncommon.MCPCapabilityRead)})
	handler := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
		return nil, struct{}{}, nil
	}
	guarded := guardTool(authIdentity{Tenant: "acme", User: "registration-time", Capabilities: capabilities}, "list", nil, handler)

	liveCtx := withAuthIdentity(context.Background(), authIdentity{Tenant: "acme", User: "live-caller", Capabilities: capabilities})
	if _, _, err := guarded(liveCtx, nil, struct{}{}); err != nil {
		t.Fatalf("live caller: unexpected refusal: %v", err)
	}

	if _, _, err := guarded(context.Background(), nil, struct{}{}); err != nil {
		t.Fatalf("no-auth-context caller: unexpected refusal: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 audit lines, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[1], "user=registration-time") {
		t.Fatalf("a call with no auth context must be audited as the registered identity, not the previous live caller: %q", lines[1])
	}
}
