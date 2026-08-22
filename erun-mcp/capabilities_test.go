package erunmcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
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
		return newServer(info, RuntimeConfig{}, identity)
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

	want := []string{
		"activity_lease_list", "cloud_list", "context_list", "diff", "idle",
		"idle_stop_history",
		"job_await", "job_output", "job_status", "list", "observe",
		"outputs_download", "outputs_list", "version",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("read capability exposed %v, want %v", got, want)
	}
}

// The tools that matter most: remote execution and everything that mutates.
func TestReadCapabilityCannotSeeExecutionOrMutation(t *testing.T) {
	got := listToolNames(t, connectWithCapabilities(t, string(eruncommon.MCPCapabilityRead)))

	for _, forbidden := range []string{"raw", "write", "commit", "deploy", "delete", "build", "push", "release", "context_init", "job_start"} {
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

	guarded := guardTool(identity, "raw", handler)
	if _, _, err := guarded(context.Background(), nil, struct{}{}); err == nil {
		t.Fatal("the guard must refuse a tool outside the caller's capabilities")
	}
	if called {
		t.Fatal("a refused tool must not run its handler")
	}
}
