package erunmcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/mcptools"
	"github.com/sophium/erun/erun-mcp/tools/shared/callerid"
	"github.com/sophium/erun/erun-mcp/tools/shared/edgeauth"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
)

// connectWithCapabilities bypasses auth middleware: it tests the surface, not token parsing.
func connectWithCapabilities(t *testing.T, capabilities ...string) *mcp.ClientSession {
	t.Helper()
	return connectWithRuntime(t, runtimecfg.RuntimeConfig{}, capabilities...)
}

// connectWithRuntime is connectWithCapabilities with a real runtime for handlers that read it.
func connectWithRuntime(t *testing.T, runtime runtimecfg.RuntimeConfig, capabilities ...string) *mcp.ClientSession {
	t.Helper()
	for _, key := range []string{edgeauth.EnvMCPTrustedIssuers, edgeauth.EnvMCPTrustedIssuer, edgeauth.EnvMCPAudience, edgeauth.EnvTenant} {
		t.Setenv(key, "")
	}
	identity := callerid.AuthIdentity{
		Tenant:       "acme",
		User:         "operator@acme",
		Capabilities: mcptools.NewMCPCapabilitySet(capabilities),
	}
	info := model.BuildInfo{Version: "1.2.3"}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return newServer(info, runtime, identity, nil)
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

// tools/list must match exactly what the caller may call.
func TestReadCapabilitySeesOnlyTheReadTools(t *testing.T) {
	got := listToolNames(t, connectWithCapabilities(t, string(mcptools.MCPCapabilityRead)))

	// Retired alias names authorize as their replacements during the alias window.
	want := []string{
		"activity_lease_list", "ai_sessions", "cloud_list", "context_list", "diff", "environment", "exec_diff",
		"exec_job_await", "exec_job_output", "exec_job_status",
		"idle", "idle_stop_history",
		"job_await", "job_output", "job_status", "jobs_list", "jobs_show", "list", "observe",
		"outputs_download", "outputs_list", "review_list", "review_queue_list", "review_show", "services", "usage", "version",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("read capability exposed %v, want %v", got, want)
	}
}

func TestReadCapabilityCannotSeeExecutionOrMutation(t *testing.T) {
	got := listToolNames(t, connectWithCapabilities(t, string(mcptools.MCPCapabilityRead)))

	for _, forbidden := range []string{"raw", "write", "commit", "deploy", "delete", "build", "push", "release", "context_init", "exec_agent", "exec_job_cancel"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("a read-only caller must not be offered %q: %v", forbidden, got)
		}
	}
}

func TestAdminCapabilitySeesEveryTool(t *testing.T) {
	admin := listToolNames(t, connectWithCapabilities(t, string(mcptools.MCPCapabilityAdmin)))
	read := listToolNames(t, connectWithCapabilities(t, string(mcptools.MCPCapabilityRead)))

	if len(admin) <= len(read) {
		t.Fatalf("admin must expose more than read: admin=%d read=%d", len(admin), len(read))
	}
	for _, required := range []string{"raw", "deploy", "delete", "version", "list", "context_start", "context_stop", "resize"} {
		if !slices.Contains(admin, required) {
			t.Fatalf("admin is missing %q: %v", required, admin)
		}
	}
	for _, tool := range read {
		if !slices.Contains(admin, tool) {
			t.Fatalf("admin should include every read tool, missing %q", tool)
		}
	}
}

// Operate is lifecycle only: no observation, no environment creation, no arbitrary code.
func TestOperateCapabilitySeesOnlyLifecycleTools(t *testing.T) {
	got := listToolNames(t, connectWithCapabilities(t, string(mcptools.MCPCapabilityOperate)))

	want := []string{"context_start", "context_stop", "deploy", "resize"}
	if !slices.Equal(got, want) {
		t.Fatalf("operate capability exposed %v, want %v", got, want)
	}
}

func TestOperateCapabilityCannotSeeExecutionOrEnvironmentLifecycleDecisions(t *testing.T) {
	got := listToolNames(t, connectWithCapabilities(t, string(mcptools.MCPCapabilityOperate)))

	for _, forbidden := range []string{
		"raw", "exec_raw", "delete", "terraform", "init", "context_init",
		"build", "push", "doctor", "expose", "unexpose", "pin", "version", "list",
	} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("an operate-only caller must not be offered %q: %v", forbidden, got)
		}
	}
}

// Unknown rather than forbidden: it was never registered for this caller.
func TestCallingAToolOutsideTheCapabilityFails(t *testing.T) {
	session := connectWithCapabilities(t, string(mcptools.MCPCapabilityRead))

	_, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "raw"})
	if err == nil {
		t.Fatal("a read-only caller must not be able to call raw")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "raw") {
		t.Fatalf("the error should name the tool that was refused, got %v", err)
	}
}

func TestReadCapabilityCanStillCallAReadTool(t *testing.T) {
	session := connectWithCapabilities(t, string(mcptools.MCPCapabilityRead))

	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "version"}); err != nil {
		t.Fatalf("a read capability must permit version: %v", err)
	}
}

// A business error naming the missing target proves the handler was reached.
func TestReadCapabilityCanReachTheEnvironmentReadModelTools(t *testing.T) {
	session := connectWithCapabilities(t, string(mcptools.MCPCapabilityRead))

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

type bearerRoundTripper struct {
	token string
}

func (rt bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+rt.token)
	return http.DefaultTransport.RoundTrip(req)
}

// Drives a real signed erun:operate bearer through the production auth middleware.
// erun-mcp cannot import erun-backend-api, so the token is signed with the same
// mcpauth.SignMCPToken call.
func TestOperateScopedTokenOverTheRealEdgeReachesLifecycleToolsAndIsRefusedElsewhere(t *testing.T) {
	issuer, token := identityWithScopedToken(t, string(mcptools.MCPCapabilityOperate))
	t.Setenv(edgeauth.EnvMCPTrustedIssuer, issuer)
	t.Setenv(edgeauth.EnvMCPAudience, "erun-mcp")
	t.Setenv(edgeauth.EnvTenant, "acme")

	cfg := HTTPConfig{Path: "/mcp"}
	httpServer := httptest.NewServer(newHTTPHandler(model.BuildInfo{Version: "1.2.3"}, cfg, runtimecfg.RuntimeConfig{}, nil))
	t.Cleanup(httpServer.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "operate-edge-test", Version: "v0.0.1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             httpServer.URL + cfg.Path,
		DisableStandaloneSSE: true,
		HTTPClient:           &http.Client{Transport: bearerRoundTripper{token: token}},
	}, nil)
	if err != nil {
		t.Fatalf("connect with an operate-scoped bearer token over the real edge: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	got := listToolNames(t, session)
	want := []string{"context_start", "context_stop", "deploy", "resize"}
	if !slices.Equal(got, want) {
		t.Fatalf("operate-scoped edge exposed %v, want %v", got, want)
	}

	// deploy without a version fails for a non-authorization reason.
	deployed, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "deploy", Arguments: map[string]any{"version": ""}})
	if err != nil {
		t.Fatalf("an operate-scoped token must reach deploy's handler, got a transport error: %v", err)
	}
	if deployed == nil || !deployed.IsError {
		t.Fatalf("deploy with no version should report a business error, got %+v", deployed)
	}
	if text := removedToolText(t, deployed); !strings.Contains(text, "version") {
		t.Fatalf("deploy's refusal should name the missing version, got %q", text)
	}

	for _, forbidden := range []string{"exec_raw", "delete", "terraform", "init"} {
		if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: forbidden}); err == nil {
			t.Fatalf("an operate-scoped token must not be able to call %q", forbidden)
		}
	}
}
