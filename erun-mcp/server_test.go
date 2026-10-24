package erunmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-mcp/tools/shared/edgeauth"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	versionpkg "github.com/sophium/erun/erun-mcp/tools/version"
)

type initInteractionStore struct{}

func (initInteractionStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return configpkg.ERunConfig{}, "", configpkg.ErrNotInitialized
}

func (initInteractionStore) SaveERunConfig(configpkg.ERunConfig) error {
	return nil
}

func (initInteractionStore) ListTenantConfigs() ([]model.TenantConfig, error) {
	return nil, nil
}

func (initInteractionStore) LoadTenantConfig(string) (model.TenantConfig, string, error) {
	return model.TenantConfig{}, "", configpkg.ErrNotInitialized
}

func (initInteractionStore) SaveTenantConfig(model.TenantConfig) error {
	return nil
}

func (initInteractionStore) LoadEnvConfig(string, string) (configpkg.EnvConfig, string, error) {
	return configpkg.EnvConfig{}, "", configpkg.ErrNotInitialized
}

func (initInteractionStore) ListEnvConfigs(string) ([]configpkg.EnvConfig, error) {
	return nil, nil
}

func (initInteractionStore) SaveEnvConfig(string, configpkg.EnvConfig) error {
	return nil
}

type listToolStore struct {
	initInteractionStore
	toolConfig    configpkg.ERunConfig
	tenantConfigs map[string]model.TenantConfig
	envConfigs    map[string]configpkg.EnvConfig
	envsByTenant  map[string][]configpkg.EnvConfig
}

func (s listToolStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return s.toolConfig, "", nil
}

func (s listToolStore) LoadTenantConfig(tenant string) (model.TenantConfig, string, error) {
	config, ok := s.tenantConfigs[tenant]
	if !ok {
		return model.TenantConfig{}, "", configpkg.ErrNotInitialized
	}
	return config, "", nil
}

func (s listToolStore) ListTenantConfigs() ([]model.TenantConfig, error) {
	tenants := make([]model.TenantConfig, 0, len(s.tenantConfigs))
	for _, tenant := range s.tenantConfigs {
		tenants = append(tenants, tenant)
	}
	return tenants, nil
}

func (s listToolStore) LoadEnvConfig(tenant, environment string) (configpkg.EnvConfig, string, error) {
	config, ok := s.envConfigs[tenant+"/"+environment]
	if !ok {
		return configpkg.EnvConfig{}, "", configpkg.ErrNotInitialized
	}
	return config, "", nil
}

func (s listToolStore) SaveEnvConfig(tenant string, config configpkg.EnvConfig) error {
	if s.envConfigs == nil {
		s.envConfigs = make(map[string]configpkg.EnvConfig)
	}
	s.envConfigs[tenant+"/"+config.Name] = config
	return nil
}

func (s listToolStore) ListEnvConfigs(tenant string) ([]configpkg.EnvConfig, error) {
	return s.envsByTenant[tenant], nil
}

func TestBuildVersionOutputDefaultsVersion(t *testing.T) {
	got := versionpkg.BuildVersionOutput(model.BuildInfo{})
	if got.Version != "dev" || got.Commit != "" || got.Date != "" {
		t.Fatalf("unexpected defaults: %+v", got)
	}
}

func TestBuildVersionOutput(t *testing.T) {
	got := versionpkg.BuildVersionOutput(model.BuildInfo{
		Version: "1.2.3",
		Commit:  "abcdef",
		Date:    "2024-01-01",
	})
	if got.Version != "1.2.3" || got.Commit != "abcdef" || got.Date != "2024-01-01" {
		t.Fatalf("unexpected version output: %+v", got)
	}
}

func TestNormalizeHTTPConfigDefaults(t *testing.T) {
	got, err := normalizeHTTPConfig(HTTPConfig{})
	if err != nil {
		t.Fatalf("normalizeHTTPConfig failed: %v", err)
	}
	if got.Host != DefaultHost || got.Port != DefaultPort || got.Path != DefaultPath {
		t.Fatalf("unexpected defaults: %+v", got)
	}
}

func TestNormalizeHTTPConfigRejectsInvalidPort(t *testing.T) {
	if _, err := normalizeHTTPConfig(HTTPConfig{Port: 70000}); err == nil {
		t.Fatal("expected invalid port error")
	}
}

func TestEndpointURL(t *testing.T) {
	got := endpointURL(HTTPConfig{})
	if got != "http://127.0.0.1:17000/mcp" {
		t.Fatalf("unexpected endpoint URL: %q", got)
	}
}

// wantRegisteredTools is asserted by name so a failure says which tool changed.
var wantRegisteredTools = []string{
	// Sorted. Deprecated aliases and the job_start stub are part of the surface.
	"activity_lease_list", "activity_lease_release", "activity_lease_take",
	"ai_sessions",
	"build", "build_profile", "cloud_clear_aws_credentials", "cloud_init_aws",
	"cloud_init_cloudflare", "cloud_init_erun", "cloud_inject_aws_credentials",
	"cloud_list", "cloud_login", "cloud_oidc", "cloud_set", "commit",
	"context_init", "context_list", "context_start", "context_stop",
	"contribute_clone", "delete", "deploy", "diff", "doctor", "e2e", "environment", "exec_agent", "exec_close-pr", "exec_commit",
	"exec_diff", "exec_gate-merge", "exec_job_attach", "exec_job_await", "exec_job_cancel", "exec_job_output", "exec_job_status",
	"exec_merge", "exec_plan-ruleset-bypass", "exec_push", "exec_raw", "exec_reconcile-bypass", "exec_report-commit-status", "exec_route-check", "exec_write", "expose", "idle", "idle_stop_cancel",
	"idle_stop_history", "idle_stop_record", "init", "job_attach", "job_await",
	"job_cancel", "job_output", "job_start", "job_status", "jobs_finish", "jobs_list", "jobs_show", "jobs_start", "list", "observe",
	"outputs_download", "outputs_list", "pin", "platform_context_create",
	"platform_context_get", "platform_context_list", "platform_env_delete",
	"platform_env_deploy", "platform_env_get", "platform_env_list",
	"platform_env_register", "platform_env_stop", "platform_identity_org_create", "platform_provision",
	"platform_tenant_create", "platform_tenant_list", "platform_tenant_repair-org-mapping", "platform_user_enroll",
	"platform_user_grant-role", "platform_user_list", "platform_version", "platform_whoami", "publish", "push", "raw",
	"release", "resize", "review_close", "review_comment", "review_create", "review_list",
	"review_queue_advance", "review_queue_list", "review_queue_override-advance", "review_record-build",
	"review_report-merged", "review_requeue", "review_resolve",
	"review_reviewers_add", "review_reviewers_list", "review_reviewers_remove", "review_show",
	"review_unresolve", "services", "terraform", "unexpose", "upgrade", "usage", "version", "whip", "write",
}

// connectTestMCPSession clears ambient auth anchors, which inside a runtime pod
// would reject the unauthenticated test client.
func connectTestMCPSession(t *testing.T, info model.BuildInfo, runtime runtimecfg.RuntimeConfig) *mcp.ClientSession {
	t.Helper()
	for _, key := range []string{edgeauth.EnvMCPTrustedIssuers, edgeauth.EnvMCPTrustedIssuer, edgeauth.EnvMCPAudience, edgeauth.EnvTenant} {
		t.Setenv(key, "")
	}
	cfg := HTTPConfig{Path: "/mcp"}
	httpServer := httptest.NewServer(newHTTPHandler(info, cfg, runtime, nil))
	t.Cleanup(httpServer.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             httpServer.URL + cfg.Path,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestHTTPHandlerExposesVersionTool(t *testing.T) {
	session := connectTestMCPSession(t, model.BuildInfo{
		Version: "1.2.3",
		Commit:  "abcdef",
		Date:    "2024-01-01",
	}, runtimecfg.RuntimeConfig{})

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	gotTools := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		gotTools = append(gotTools, tool.Name)
	}
	slices.Sort(gotTools)
	if !slices.Equal(gotTools, wantRegisteredTools) {
		t.Fatalf("exposed tools = %v, want %v", gotTools, wantRegisteredTools)
	}

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "version"})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	version := decodeStructuredVersion(t, result.StructuredContent)
	if got := version["version"]; got != "1.2.3" {
		t.Fatalf("unexpected structured content: %+v", version)
	}
}

// job_start is a registered stub, so the call must fail as a tool error naming
// its replacements, not as "unknown tool".
func TestCallingARemovedToolNamesItsReplacement(t *testing.T) {
	session := connectTestMCPSession(t, model.BuildInfo{}, runtimecfg.RuntimeConfig{})

	removed, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "job_start",
		Arguments: map[string]any{"command": []string{"echo", "hi"}},
	})
	if err != nil {
		t.Fatalf("CallTool(job_start) failed at the protocol level: %v", err)
	}
	if !removed.IsError {
		t.Fatalf("expected job_start to report a tool error, got: %+v", removed)
	}
	if got := removedToolText(t, removed); !strings.Contains(got, "exec_raw") || !strings.Contains(got, "exec_agent") {
		t.Fatalf("job_start error should name both replacements, got: %q", got)
	}
}

func removedToolText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	var sb strings.Builder
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			sb.WriteString(text.Text)
		}
	}
	return sb.String()
}

func decodeStructuredVersion(t *testing.T, content any) map[string]any {
	t.Helper()

	switch typed := content.(type) {
	case map[string]any:
		return typed
	case json.RawMessage:
		var decoded map[string]any
		if err := json.Unmarshal(typed, &decoded); err != nil {
			t.Fatalf("Unmarshal(structured content) failed: %v", err)
		}
		return decoded
	default:
		t.Fatalf("unexpected structured content type %T", content)
		return nil
	}
}

// lockedBuffer is written from the goroutine serving the request.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// The refusal is a JSON-RPC error on HTTP 200, so the assertion is on the
// SDK's log line, not the response.
func TestARefusedToolCallOnAnUnknownSessionIsLogged(t *testing.T) {
	for _, key := range []string{edgeauth.EnvMCPTrustedIssuers, edgeauth.EnvMCPTrustedIssuer, edgeauth.EnvMCPAudience, edgeauth.EnvTenant} {
		t.Setenv(key, "")
	}

	previous := mcpServerLogger
	logs := &lockedBuffer{}
	mcpServerLogger = slog.New(slog.NewTextHandler(logs, nil))
	t.Cleanup(func() { mcpServerLogger = previous })

	cfg := HTTPConfig{Path: "/mcp"}
	httpServer := httptest.NewServer(newHTTPHandler(model.BuildInfo{Version: "1.2.3"}, cfg, runtimecfg.RuntimeConfig{}, nil))
	t.Cleanup(httpServer.Close)

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"version","arguments":{}}}`
	req, err := http.NewRequest(http.MethodPost, httpServer.URL+cfg.Path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build tools/call request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST tools/call with no session: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	reply, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if !strings.Contains(string(reply), "invalid during session initialization") {
		t.Fatalf("expected the session-initialization guard to refuse the call, got HTTP %d: %s", resp.StatusCode, reply)
	}

	if got := logs.String(); !strings.Contains(got, "invalid during initialization") {
		t.Fatalf("the SDK refused a tool call without logging it; the edge reports refusals to nowhere. Captured log: %q", got)
	}
}
