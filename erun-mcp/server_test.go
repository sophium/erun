package erunmcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/adrg/xdg"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type initInteractionStore struct{}

func (initInteractionStore) LoadERunConfig() (eruncommon.ERunConfig, string, error) {
	return eruncommon.ERunConfig{}, "", eruncommon.ErrNotInitialized
}

func (initInteractionStore) SaveERunConfig(eruncommon.ERunConfig) error {
	return nil
}

func (initInteractionStore) ListTenantConfigs() ([]eruncommon.TenantConfig, error) {
	return nil, nil
}

func (initInteractionStore) LoadTenantConfig(string) (eruncommon.TenantConfig, string, error) {
	return eruncommon.TenantConfig{}, "", eruncommon.ErrNotInitialized
}

func (initInteractionStore) SaveTenantConfig(eruncommon.TenantConfig) error {
	return nil
}

func (initInteractionStore) LoadEnvConfig(string, string) (eruncommon.EnvConfig, string, error) {
	return eruncommon.EnvConfig{}, "", eruncommon.ErrNotInitialized
}

func (initInteractionStore) ListEnvConfigs(string) ([]eruncommon.EnvConfig, error) {
	return nil, nil
}

func (initInteractionStore) SaveEnvConfig(string, eruncommon.EnvConfig) error {
	return nil
}

type listToolStore struct {
	initInteractionStore
	toolConfig    eruncommon.ERunConfig
	tenantConfigs map[string]eruncommon.TenantConfig
	envConfigs    map[string]eruncommon.EnvConfig
	envsByTenant  map[string][]eruncommon.EnvConfig
}

func (s listToolStore) LoadERunConfig() (eruncommon.ERunConfig, string, error) {
	return s.toolConfig, "", nil
}

func (s listToolStore) LoadTenantConfig(tenant string) (eruncommon.TenantConfig, string, error) {
	config, ok := s.tenantConfigs[tenant]
	if !ok {
		return eruncommon.TenantConfig{}, "", eruncommon.ErrNotInitialized
	}
	return config, "", nil
}

func (s listToolStore) ListTenantConfigs() ([]eruncommon.TenantConfig, error) {
	tenants := make([]eruncommon.TenantConfig, 0, len(s.tenantConfigs))
	for _, tenant := range s.tenantConfigs {
		tenants = append(tenants, tenant)
	}
	return tenants, nil
}

func (s listToolStore) LoadEnvConfig(tenant, environment string) (eruncommon.EnvConfig, string, error) {
	config, ok := s.envConfigs[tenant+"/"+environment]
	if !ok {
		return eruncommon.EnvConfig{}, "", eruncommon.ErrNotInitialized
	}
	return config, "", nil
}

func (s listToolStore) SaveEnvConfig(tenant string, config eruncommon.EnvConfig) error {
	if s.envConfigs == nil {
		s.envConfigs = make(map[string]eruncommon.EnvConfig)
	}
	s.envConfigs[tenant+"/"+config.Name] = config
	return nil
}

func (s listToolStore) ListEnvConfigs(tenant string) ([]eruncommon.EnvConfig, error) {
	return s.envsByTenant[tenant], nil
}

func TestBuildVersionOutputDefaultsVersion(t *testing.T) {
	got := buildVersionOutput(eruncommon.BuildInfo{})
	if got.Version != "dev" || got.Commit != "" || got.Date != "" {
		t.Fatalf("unexpected defaults: %+v", got)
	}
}

func TestBuildVersionOutput(t *testing.T) {
	got := buildVersionOutput(eruncommon.BuildInfo{
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

// wantRegisteredTools is this module's registered tool surface, asserted by
// name in TestHTTPHandlerExposesVersionTool: a bare count cannot say which
// tool appeared or vanished, and its failure message prints unreadable struct
// pointers. Kept at package scope so the test function stays within funlen.
var wantRegisteredTools = []string{
	// Sorted. The four exec_* names joined the surface with #1186's rename;
	// diff/raw/write/commit remain as deprecated aliases for one release.
	// #1246 moved job's five query verbs to exec_job_* (job_* remain as
	// deprecated aliases for one release) and added exec_agent; job_start is
	// gone outright, split between exec_raw's wait:false mode and exec_agent.
	"activity_lease_list", "activity_lease_release", "activity_lease_take",
	"build", "cloud_clear_aws_credentials", "cloud_init_aws",
	"cloud_init_cloudflare", "cloud_init_erun", "cloud_inject_aws_credentials",
	"cloud_list", "cloud_login", "cloud_oidc", "cloud_set", "commit",
	"context_init", "context_list", "context_start", "context_stop",
	"contribute_clone", "delete", "deploy", "diff", "doctor", "exec_agent", "exec_commit",
	"exec_diff", "exec_job_attach", "exec_job_await", "exec_job_cancel", "exec_job_output", "exec_job_status",
	"exec_push", "exec_raw", "exec_write", "expose", "idle", "idle_stop_cancel",
	"idle_stop_history", "idle_stop_record", "init", "job_attach", "job_await",
	"job_cancel", "job_output", "job_status", "list", "observe",
	"outputs_download", "outputs_list", "pin", "platform_context_create",
	"platform_context_get", "platform_context_list", "platform_env_delete",
	"platform_env_deploy", "platform_env_get", "platform_env_list",
	"platform_env_register", "platform_env_stop", "platform_provision",
	"platform_tenant_create", "platform_tenant_list", "platform_user_enroll",
	"platform_user_list", "platform_whoami", "publish", "push", "raw",
	"release", "resize", "review_close", "review_comment", "review_create", "review_list",
	"review_queue_advance", "review_queue_list", "review_queue_override-advance", "review_resolve", "review_show",
	"review_unresolve", "terraform", "unexpose", "upgrade", "usage", "version", "whip", "write",
}

func TestHTTPHandlerExposesVersionTool(t *testing.T) {
	// newHTTPHandler resolves its auth trust anchor from the ambient environment,
	// so running this test inside an erun runtime pod (which sets
	// ERUN_MCP_TRUSTED_ISSUER) would enable auth and reject the unauthenticated
	// client. Clear the anchors so the tool surface is what is under test.
	for _, key := range []string{envMCPTrustedIssuers, envMCPTrustedIssuer, envMCPAudience, envTenant} {
		t.Setenv(key, "")
	}
	cfg := HTTPConfig{Path: "/mcp"}
	info := eruncommon.BuildInfo{
		Version: "1.2.3",
		Commit:  "abcdef",
		Date:    "2024-01-01",
	}

	httpServer := httptest.NewServer(newHTTPHandler(info, cfg, RuntimeConfig{}, nil))
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             httpServer.URL + cfg.Path,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer func() {
		_ = session.Close()
	}()

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	// The registered tool set is this module's public surface, so it is asserted
	// by name against wantRegisteredTools: a bare count cannot say which tool
	// appeared or vanished, and its failure message prints unreadable struct
	// pointers.
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

func TestActivityHTTPMiddlewareSkipsRecordingForIdleProbe(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	xdg.Reload()

	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}
	handler := activityHTTPMiddleware(runtime, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server := httptest.NewServer(handler)
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	req.Header.Set(eruncommon.MCPIdleProbeHeader, "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	_ = resp.Body.Close()

	dir, err := eruncommon.EnvironmentActivityDir("tenant-a", "dev")
	if err != nil {
		t.Fatalf("EnvironmentActivityDir failed: %v", err)
	}
	for _, name := range []string{"mcp.json", "codex.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be absent under probe header, stat err=%v", name, err)
		}
	}
}

func TestActivityHTTPMiddlewareRecordsWithoutIdleProbe(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	xdg.Reload()

	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}
	handler := activityHTTPMiddleware(runtime, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	_ = resp.Body.Close()

	dir, err := eruncommon.EnvironmentActivityDir("tenant-a", "dev")
	if err != nil {
		t.Fatalf("EnvironmentActivityDir failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mcp.json")); err != nil {
		t.Fatalf("expected mcp.json to exist without probe header: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "codex.json")); !os.IsNotExist(err) {
		t.Fatalf("expected codex.json to be absent for non-codex user agent, stat err=%v", err)
	}
}

func TestActivityHTTPMiddlewareRecordsCodexWhenUserAgentMatches(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	xdg.Reload()

	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}
	handler := activityHTTPMiddleware(runtime, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server := httptest.NewServer(handler)
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	req.Header.Set("User-Agent", "codex-cli/0.1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	_ = resp.Body.Close()

	dir, err := eruncommon.EnvironmentActivityDir("tenant-a", "dev")
	if err != nil {
		t.Fatalf("EnvironmentActivityDir failed: %v", err)
	}
	for _, name := range []string{"mcp.json", "codex.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("expected %s to exist for codex client: %v", name, err)
		}
	}
}

func TestCloudSetToolSetsEnvironmentAlias(t *testing.T) {
	projectRoot := t.TempDir()
	store := &listToolStore{
		tenantConfigs: map[string]eruncommon.TenantConfig{
			"frs": {Name: "frs", DefaultEnvironment: "dev"},
		},
		envConfigs: map[string]eruncommon.EnvConfig{
			"frs/dev": {
				Name:               "dev",
				LocalRepoPath:      projectRoot,
				KubernetesContext:  "cluster-dev",
				CloudProviderAlias: "old-cloud",
			},
		},
	}
	handler := cloudSetTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{Tenant: "frs", Environment: "dev"},
		Store:   store,
	}))

	_, result, err := handler(context.Background(), nil, CloudSetInput{Alias: "team-cloud"})
	if err != nil {
		t.Fatalf("cloudSetTool failed: %v", err)
	}
	if result.Tenant != "frs" || result.Environment != "dev" || result.EnvConfig.CloudProviderAlias != "team-cloud" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if store.envConfigs["frs/dev"].CloudProviderAlias != "team-cloud" {
		t.Fatalf("unexpected stored env: %+v", store.envConfigs["frs/dev"])
	}
}

func TestDiffToolReturnsStructuredGitDiff(t *testing.T) {
	projectRoot := t.TempDir()
	runGitTestCommand(t, projectRoot, "init", "-b", "main")
	runGitTestCommand(t, projectRoot, "config", "user.email", "codex@example.com")
	runGitTestCommand(t, projectRoot, "config", "user.name", "Codex")
	if err := os.WriteFile(filepath.Join(projectRoot, "app.txt"), []byte("old\nsame\n"), 0o644); err != nil {
		t.Fatalf("write app.txt: %v", err)
	}
	runGitTestCommand(t, projectRoot, "add", ".")
	runGitTestCommand(t, projectRoot, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(projectRoot, "app.txt"), []byte("new\nsame\nadded\n"), 0o644); err != nil {
		t.Fatalf("write app.txt: %v", err)
	}

	handler := diffTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{RepoPath: projectRoot},
	}))
	_, output, err := handler(context.Background(), nil, DiffInput{})
	if err != nil {
		t.Fatalf("diffTool failed: %v", err)
	}

	assertStructuredDiffOutput(t, output, projectRoot)
}

func assertStructuredDiffOutput(t *testing.T, output eruncommon.DiffResult, projectRoot string) {
	t.Helper()

	if output.WorkingDirectory != projectRoot || output.RawDiff == "" {
		t.Fatalf("unexpected output: %+v", output)
	}
	if output.Summary.FileCount != 1 || output.Summary.Additions != 2 || output.Summary.Deletions != 1 {
		t.Fatalf("unexpected summary: %+v", output.Summary)
	}
	if len(output.Files) != 1 || output.Files[0].Path != "app.txt" {
		t.Fatalf("unexpected files: %+v", output.Files)
	}
	if len(output.Tree) != 1 || output.Tree[0].Name != "app.txt" {
		t.Fatalf("unexpected tree: %+v", output.Tree)
	}
}

// dangerousExecContent carries the constructs a shell would reinterpret —
// backticks, command substitution, embedded quotes, a trailing newline — so a
// round trip through write/commit demonstrates the property that justifies
// bypassing raw for these two operations: nothing here is ever shell-parsed.
const dangerousExecContent = "line one\n`echo pwned` $(echo pwned) \"quoted\" 'quoted'\ntrailing\n\n"

func TestWriteToolWritesContentByteIdenticallyAndRefusesOutsideRoot(t *testing.T) {
	projectRoot := t.TempDir()

	handler := writeTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{RepoPath: projectRoot},
	}))
	_, output, err := handler(context.Background(), nil, WriteInput{Path: "config/values.yaml", Content: dangerousExecContent})
	if err != nil {
		t.Fatalf("writeTool failed: %v", err)
	}
	if output.Write == nil {
		t.Fatalf("expected Write result, got %+v", output)
	}
	wantPath := filepath.Join(projectRoot, "config", "values.yaml")
	if output.Write.Path != wantPath {
		t.Fatalf("Path = %q, want %q", output.Write.Path, wantPath)
	}
	if output.Write.Bytes != int64(len(dangerousExecContent)) {
		t.Fatalf("Bytes = %d, want %d", output.Write.Bytes, len(dangerousExecContent))
	}
	got, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(got) != dangerousExecContent {
		t.Fatalf("written content = %q, want byte-identical %q", got, dangerousExecContent)
	}

	_, _, err = handler(context.Background(), nil, WriteInput{Path: "../escape.txt", Content: "x"})
	if err == nil {
		t.Fatalf("expected refusal for a path outside the repo root")
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(projectRoot), "escape.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no file to land outside the repo root")
	}
}

// TestWriteToolRefusesWriteThroughInTreeSymlink proves the write tool cannot
// be pointed outside the repo root through a symlink planted inside it — a
// lexical containment check alone does not follow symlinks, so this asserts
// on the actual filesystem outcome, not just the handler's error, since the
// whole defect is that a lexical check reports success incorrectly.
func TestWriteToolRefusesWriteThroughInTreeSymlink(t *testing.T) {
	projectRoot := t.TempDir()
	outside := t.TempDir()
	escapeTarget := filepath.Join(outside, "pwned.txt")
	if err := os.Symlink(outside, filepath.Join(projectRoot, "escape")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	handler := writeTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{RepoPath: projectRoot},
	}))
	_, _, err := handler(context.Background(), nil, WriteInput{Path: "escape/pwned.txt", Content: "pwned"})
	if err == nil {
		t.Fatalf("expected refusal writing through a symlinked directory component")
	}
	if _, statErr := os.Stat(escapeTarget); !os.IsNotExist(statErr) {
		t.Fatalf("expected no file to land outside the repo root through the symlink")
	}
}

func TestCommitToolCommitsAndRefusesBranchMismatch(t *testing.T) {
	projectRoot := t.TempDir()
	runGitTestCommand(t, projectRoot, "init", "-b", "main")
	runGitTestCommand(t, projectRoot, "config", "user.email", "codex@example.com")
	runGitTestCommand(t, projectRoot, "config", "user.name", "Codex")
	runGitTestCommand(t, projectRoot, "commit", "--allow-empty", "-m", "initial")
	if err := os.WriteFile(filepath.Join(projectRoot, "app.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("write app.txt: %v", err)
	}

	handler := commitTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{RepoPath: projectRoot},
	}))

	_, _, err := handler(context.Background(), nil, CommitInput{Branch: "not-main", Message: dangerousExecContent})
	if err == nil {
		t.Fatalf("expected refusal when the declared branch does not match the current branch")
	}

	_, output, err := handler(context.Background(), nil, CommitInput{Branch: "main", Message: dangerousExecContent})
	if err != nil {
		t.Fatalf("commitTool failed: %v", err)
	}
	assertCommitToolOutput(t, output, projectRoot)
}

func assertCommitToolOutput(t *testing.T, output JobEnvelopeOutput, projectRoot string) {
	t.Helper()

	if output.Commit == nil {
		t.Fatalf("expected Commit result, got %+v", output)
	}
	if output.Commit.Branch != "main" || output.Commit.Commit == "" {
		t.Fatalf("unexpected commit result: %+v", output.Commit)
	}
	if len(output.Commit.Files) != 1 || output.Commit.Files[0] != "app.txt" {
		t.Fatalf("unexpected committed files: %+v", output.Commit.Files)
	}

	messageCmd := exec.Command("git", "log", "-1", "--format=%B")
	messageCmd.Dir = projectRoot
	messageOut, err := messageCmd.Output()
	if err != nil {
		t.Fatalf("read commit message: %v", err)
	}
	if !strings.Contains(string(messageOut), "`echo pwned` $(echo pwned) \"quoted\" 'quoted'") {
		t.Fatalf("commit message lost dangerous content verbatim: %q", messageOut)
	}
}

func gitStatusPorcelain(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	return string(out)
}

func mustWriteTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// seedCommitToolScopedPathsRepo prepares the motivating scenario: the caller
// wrote values.yaml and wants to commit only that, but the tree also carries
// unrelated.txt from some other writer. Before path scoping existed, the tool
// had no way to express "only these paths" at all, so `git add -A` would
// have swept both in.
func seedCommitToolScopedPathsRepo(t *testing.T) string {
	t.Helper()
	projectRoot := t.TempDir()
	runGitTestCommand(t, projectRoot, "init", "-b", "main")
	runGitTestCommand(t, projectRoot, "config", "user.email", "codex@example.com")
	runGitTestCommand(t, projectRoot, "config", "user.name", "Codex")
	runGitTestCommand(t, projectRoot, "commit", "--allow-empty", "-m", "initial")
	mustWriteTestFile(t, filepath.Join(projectRoot, "values.yaml"), "typo: fixed\n")
	mustWriteTestFile(t, filepath.Join(projectRoot, "unrelated.txt"), "someone else's in-flight work\n")
	return projectRoot
}

func TestCommitToolScopedPathsRefusesUnrelatedDirtyFile(t *testing.T) {
	projectRoot := seedCommitToolScopedPathsRepo(t)
	handler := commitTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{RepoPath: projectRoot},
	}))

	_, _, err := handler(context.Background(), nil, CommitInput{Branch: "main", Message: "fix the values typo", Paths: []string{"values.yaml"}})
	if err == nil {
		t.Fatalf("expected refusal when the tree has changes outside the declared paths")
	}
	if !strings.Contains(err.Error(), "unrelated.txt") {
		t.Fatalf("expected the refusal to name the unrelated file, got: %v", err)
	}
	if strings.TrimSpace(gitStatusPorcelain(t, projectRoot)) == "" {
		t.Fatalf("expected both files to remain uncommitted after refusal")
	}
}

func TestCommitToolScopedPathsCommitsOnlyTheDeclaredFile(t *testing.T) {
	projectRoot := seedCommitToolScopedPathsRepo(t)
	if err := os.Remove(filepath.Join(projectRoot, "unrelated.txt")); err != nil {
		t.Fatalf("remove unrelated.txt: %v", err)
	}
	handler := commitTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{RepoPath: projectRoot},
	}))

	_, output, err := handler(context.Background(), nil, CommitInput{Branch: "main", Message: "fix the values typo", Paths: []string{"values.yaml"}})
	if err != nil {
		t.Fatalf("commitTool failed: %v", err)
	}
	if output.Commit == nil {
		t.Fatalf("expected Commit result, got %+v", output)
	}
	if got := output.Commit.Files; len(got) != 1 || got[0] != "values.yaml" {
		t.Fatalf("unexpected committed files: %+v", got)
	}
}

// TestCommitToolRejectsBlankPathEntries proves a Paths list of only blank
// strings is refused rather than silently degrading to the unscoped
// "commit everything" behavior — the exact failure path scoping (#1155)
// exists to prevent, reintroduced through a caller passing blanks instead of
// omitting Paths entirely.
func TestCommitToolRejectsBlankPathEntries(t *testing.T) {
	projectRoot := seedCommitToolScopedPathsRepo(t)
	handler := commitTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{RepoPath: projectRoot},
	}))

	_, _, err := handler(context.Background(), nil, CommitInput{Branch: "main", Message: "fix the values typo", Paths: []string{"", ""}})
	if err == nil {
		t.Fatalf("expected refusal for a paths list of blank entries")
	}
	if strings.TrimSpace(gitStatusPorcelain(t, projectRoot)) == "" {
		t.Fatalf("expected both files to remain uncommitted after refusal")
	}
}

func TestListToolReturnsConfiguredTenantsAndEffectiveTarget(t *testing.T) {
	projectRoot := t.TempDir()
	handler := listTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{
			Tenant:      "tenant-a",
			Environment: "dev",
			RepoPath:    projectRoot,
		},
		Store: listToolStore{
			toolConfig: eruncommon.ERunConfig{DefaultTenant: "tenant-a"},
			tenantConfigs: map[string]eruncommon.TenantConfig{
				"tenant-a": {
					Name:               "tenant-a",
					DefaultEnvironment: "dev",
				},
			},
			envConfigs: map[string]eruncommon.EnvConfig{
				"tenant-a/dev": {
					Name:              "dev",
					LocalRepoPath:     projectRoot,
					KubernetesContext: "cluster-dev",
				},
			},
			envsByTenant: map[string][]eruncommon.EnvConfig{
				"tenant-a": {{
					Name:              "dev",
					LocalRepoPath:     projectRoot,
					KubernetesContext: "cluster-dev",
				}},
			},
		},
	}))

	_, output, err := handler(context.Background(), nil, ListInput{})
	if err != nil {
		t.Fatalf("listTool failed: %v", err)
	}

	assertListToolOutput(t, output)
}

func assertListToolOutput(t *testing.T, output eruncommon.ListResult) {
	t.Helper()

	if output.Defaults.Tenant != "tenant-a" || output.Defaults.Environment != "dev" {
		t.Fatalf("unexpected defaults: %+v", output.Defaults)
	}
	if output.CurrentDirectory.Effective == nil {
		t.Fatalf("expected effective target, got %+v", output.CurrentDirectory)
	}
	assertListEffectiveTarget(t, *output.CurrentDirectory.Effective)
	if len(output.Tenants) != 1 || output.Tenants[0].Name != "tenant-a" {
		t.Fatalf("unexpected tenants: %+v", output.Tenants)
	}
	if output.Tenants[0].Environments[0].LocalPorts.RangeEnd != 17099 {
		t.Fatalf("unexpected environment local ports: %+v", output.Tenants[0].Environments[0].LocalPorts)
	}
}

func assertListEffectiveTarget(t *testing.T, target eruncommon.ListEffectiveTargetResult) {
	t.Helper()

	if target.Tenant != "tenant-a" || target.Environment != "dev" {
		t.Fatalf("unexpected effective target: %+v", target)
	}
	if target.LocalPorts.RangeStart != 17000 || target.LocalPorts.SSH != 17022 {
		t.Fatalf("unexpected effective local ports: %+v", target.LocalPorts)
	}
}

func TestReleaseToolPreview(t *testing.T) {
	projectRoot := createReleaseRuntimeRepo(t, "develop")
	if err := eruncommon.SaveProjectConfig(projectRoot, eruncommon.ProjectConfig{}); err != nil {
		t.Fatalf("SaveProjectConfig failed: %v", err)
	}

	handler := releaseTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{
			RepoPath: projectRoot,
		},
	}))

	_, output, err := handler(context.Background(), nil, ReleaseInput{Preview: true, Verbosity: 1})
	if err != nil {
		t.Fatalf("releaseTool failed: %v", err)
	}

	if output.Executed {
		t.Fatalf("expected preview output, got %+v", output)
	}
	if output.Spec.Mode != eruncommon.ReleaseModeCandidate || output.Spec.Version == "" || len(output.Spec.Stages) == 0 {
		t.Fatalf("unexpected release output: %+v", output)
	}
	if len(output.Spec.DockerImages) != 1 || output.Spec.DockerImages[0].Tag == "" {
		t.Fatalf("unexpected docker images: %+v", output.Spec.DockerImages)
	}
}

func createReleaseRuntimeRepo(t *testing.T, branch string) string {
	t.Helper()

	projectRoot := t.TempDir()
	releaseRoot := filepath.Join(projectRoot, "erun-devops")
	for _, dir := range []string{
		filepath.Join(releaseRoot, "k8s", "api"),
		filepath.Join(releaseRoot, "docker", "api"),
		filepath.Join(releaseRoot, "docker", "base"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(releaseRoot, "VERSION"), []byte("1.4.2\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(releaseRoot, "k8s", "api", "Chart.yaml"), []byte("apiVersion: v2\nname: api\nversion: 0.1.0\nappVersion: 0.1.0\n"), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(releaseRoot, "docker", "api", "Dockerfile"), []byte("FROM alpine:3.22\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(releaseRoot, "docker", "base", "Dockerfile"), []byte("FROM alpine:3.22\n"), 0o644); err != nil {
		t.Fatalf("write other Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(releaseRoot, "docker", "base", "VERSION"), []byte("9.9.9\n"), 0o644); err != nil {
		t.Fatalf("write other VERSION: %v", err)
	}

	runGitTestCommand(t, projectRoot, "init", "-b", branch)
	runGitTestCommand(t, projectRoot, "config", "user.email", "codex@example.com")
	runGitTestCommand(t, projectRoot, "config", "user.name", "Codex")
	runGitTestCommand(t, projectRoot, "add", ".")
	runGitTestCommand(t, projectRoot, "commit", "-m", "initial")
	return projectRoot
}

func runGitTestCommand(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

func TestPushToolRejectsRepoRootWithoutComponent(t *testing.T) {
	projectRoot := t.TempDir()
	moduleRoot := filepath.Join(projectRoot, "tenant-a-devops")
	componentDir := filepath.Join(moduleRoot, "docker", "tenant-a-devops")
	if err := os.MkdirAll(componentDir, 0o755); err != nil {
		t.Fatalf("mkdir component dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}

	handler := pushTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{
			Environment: eruncommon.DefaultEnvironment,
			RepoPath:    projectRoot,
		},
	}))

	// Pass a version so the version-required gate passes and the test reaches
	// the repo-root-without-component rejection it actually covers.
	_, _, err := handler(context.Background(), nil, PushInput{Version: "1.0.0"})
	if err == nil {
		t.Fatal("expected missing Dockerfile error")
	}
	if err.Error() != "dockerfile not found in current directory" {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestPushToolRequiresVersion locks the version-required gate: push is a pure
// primitive that publishes a built version and never mints one, so the tool
// must fail before any work when no version is supplied (erun-mcp/AGENTS.md).
func TestPushToolRequiresVersion(t *testing.T) {
	handler := pushTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{
			Environment: eruncommon.DefaultEnvironment,
			RepoPath:    t.TempDir(),
		},
	}))

	if _, _, err := handler(context.Background(), nil, PushInput{}); err != errMissingPushVersion {
		t.Fatalf("expected errMissingPushVersion, got %v", err)
	}
}

func TestBuildToolRunsProjectBuildScriptWhenPresent(t *testing.T) {
	projectRoot := t.TempDir()
	scriptDir := filepath.Join(projectRoot, "scripts", "build")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("mkdir script dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write build.sh: %v", err)
	}

	var called bool
	handler := buildTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{
			Environment: "dev",
			RepoPath:    projectRoot,
		},
		BuildScriptRunner: func(dir, path string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
			called = true
			if dir != scriptDir || path != "./build.sh" {
				t.Fatalf("unexpected build script call: dir=%q path=%q", dir, path)
			}
			if len(env) != 0 {
				t.Fatalf("unexpected build script env: %+v", env)
			}
			return nil
		},
		BuildDockerImage: func(eruncommon.DockerBuildSpec, io.Writer, io.Writer) error {
			t.Fatal("unexpected docker build")
			return nil
		},
	}))

	_, output, err := handler(context.Background(), nil, BuildInput{})
	if err != nil {
		t.Fatalf("buildTool failed: %v", err)
	}
	if !output.Executed {
		t.Fatalf("expected execution output, got %+v", output)
	}
	if !called {
		t.Fatal("expected build script runner to be called")
	}
}

// newComponentBuildFixture writes a project with a root build.sh (so the
// environment's build script is not disabled) plus one component docker
// context, for TestBuildToolBuildsComponentWithMultiPlatformSpec.
func newComponentBuildFixture(t *testing.T) string {
	t.Helper()
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write build.sh: %v", err)
	}
	componentDir := filepath.Join(projectRoot, "acme-devops", "docker", "web")
	if err := os.MkdirAll(componentDir, 0o755); err != nil {
		t.Fatalf("mkdir component dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	return projectRoot
}

// TestBuildToolBuildsComponentWithMultiPlatformSpec locks erun#1248: a
// component build on an environment whose build script is not disabled must
// still resolve a real, buildable docker spec (matching what the no-component
// path resolves via newDockerBuildSpec) rather than a spec with no platforms,
// which builds and pushes nothing while still reporting success.
func TestBuildToolBuildsComponentWithMultiPlatformSpec(t *testing.T) {
	projectRoot := newComponentBuildFixture(t)

	var captured *eruncommon.DockerBuildSpec
	handler := buildTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{
			Environment: "dev",
			RepoPath:    projectRoot,
		},
		BuildScriptRunner: func(dir, path string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
			t.Fatal("unexpected build script call: --component must resolve the docker context directly")
			return nil
		},
		BuildDockerImage: func(spec eruncommon.DockerBuildSpec, stdout, stderr io.Writer) error {
			captured = &spec
			return nil
		},
	}))

	_, output, err := handler(context.Background(), nil, BuildInput{Component: "web", NoIncremental: true})
	if err != nil {
		t.Fatalf("buildTool failed: %v", err)
	}
	if !output.Executed {
		t.Fatalf("expected execution output, got %+v", output)
	}
	if captured == nil {
		t.Fatal("expected the docker image builder to be invoked")
	}
	wantPlatforms := []string{"linux/amd64", "linux/arm64"}
	if !slices.Equal(captured.Platforms, wantPlatforms) {
		t.Fatalf("resolved component build spec has the wrong platforms, so it builds nothing real: got %v want %v", captured.Platforms, wantPlatforms)
	}
	if captured.DockerfilePath == "" {
		t.Fatalf("expected a resolved Dockerfile path, got %+v", captured)
	}
}

// TestBuildToolSurfacesInvalidDockerContext locks that a misconfigured
// paths.dockercontext fails the MCP build tool loudly for a component build,
// rather than being swallowed — the erun-common resolver's error must propagate
// through the MCP transport. The shared behaviour is gated by the erun-cli
// integration suite, which never enters this MCP wrapper, so the propagation is
// covered here in the owning module.
func TestBuildToolSurfacesInvalidDockerContext(t *testing.T) {
	projectRoot := t.TempDir()
	componentDir := filepath.Join(projectRoot, "acme-devops", "docker", "web")
	if err := os.MkdirAll(componentDir, 0o755); err != nil {
		t.Fatalf("mkdir component dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	erunDir := filepath.Join(projectRoot, ".erun")
	if err := os.MkdirAll(erunDir, 0o755); err != nil {
		t.Fatalf("mkdir .erun: %v", err)
	}
	if err := os.WriteFile(filepath.Join(erunDir, "config.yaml"), []byte("paths:\n  dockercontext: bogus\n"), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	handler := buildTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{
			Environment: "dev",
			RepoPath:    projectRoot,
		},
		BuildDockerImage: func(eruncommon.DockerBuildSpec, io.Writer, io.Writer) error {
			t.Fatal("unexpected docker build: resolution must fail before execution")
			return nil
		},
	}))

	_, _, err := handler(context.Background(), nil, BuildInput{Component: "web", Preview: true})
	if err == nil {
		t.Fatal("expected an error for an invalid paths.dockercontext value")
	}
	want := `invalid docker context "bogus" (.erun/config.yaml paths.dockercontext): expected "repo-root" or "component"`
	if err.Error() != want {
		t.Fatalf("unexpected error:\n got: %v\nwant: %s", err, want)
	}
}

func TestInitToolReturnsInteractionWhenSharedInitNeedsInput(t *testing.T) {
	projectRoot := t.TempDir()

	handler := initTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{
			Tenant:   "tenant-a",
			RepoPath: projectRoot,
		},
		Store: initInteractionStore{},
	}))

	_, output, err := handler(context.Background(), nil, InitInput{})
	if err != nil {
		t.Fatalf("initTool failed: %v", err)
	}
	if output.Interaction == nil && output.Executed {
		t.Fatalf("expected interaction output, got %+v", output)
	}
	if output.Interaction == nil {
		t.Fatalf("expected interaction output, got %+v", output)
	}
	if output.Interaction.Type != eruncommon.BootstrapInitInteractionConfirmTenant {
		t.Fatalf("unexpected interaction: %+v", output.Interaction)
	}
	if output.Executed {
		t.Fatalf("expected non-executed interaction output, got %+v", output)
	}
}

// stubKubectlAlwaysSucceeds points the ERUN_KUBECTL_BIN seam (eruncommon.Command)
// at a script that exits 0 unconditionally, so eruncommon.RunHelmDeploy's own
// namespace-ensure — which runs before the pre-rollout secret applies and is not
// reachable through RuntimeConfig.EnsureKubernetesNamespace (see
// eruncommon.WrapHelmChartDeployerWithNamespaceEnsure and
// TestRunHelmDeployEnsuresTheNamespaceBeforeApplyingSecrets) — never shells out to
// a real cluster. Without it, these tests depend on the host's kubeconfig
// happening to have a context named "cluster-remote"/"cluster-dev", which no real
// machine does.
func stubKubectlAlwaysSucceeds(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubectl")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("ERUN_KUBECTL_BIN", path)
}

// fakeRemoteRepositoryStateRunner fakes RunRemoteCommand for a remote-init
// flow that reaches the repository-state script, answering the #1201
// registry-credential-check script (identified by its own content, since
// every remote-exec call shares this one seam) with "credential configured"
// so these tests keep exercising repository interaction rather than tripping
// the new check.
func fakeRemoteRepositoryStateRunner(repositoryStateStdout string) func(eruncommon.ShellLaunchParams, string) (eruncommon.RemoteCommandResult, error) {
	return func(_ eruncommon.ShellLaunchParams, script string) (eruncommon.RemoteCommandResult, error) {
		if strings.Contains(script, ".docker/config.json") {
			return eruncommon.RemoteCommandResult{Stdout: "1\n"}, nil
		}
		return eruncommon.RemoteCommandResult{Stdout: repositoryStateStdout}, nil
	}
}

func TestInitToolReturnsRepositoryInteractionForRemoteInit(t *testing.T) {
	stubKubectlAlwaysSucceeds(t)
	// The remote init flow deploys the runtime chart, which now refuses rather
	// than guess when no candidate is confirmed published; this test cares
	// about the returned repository interaction, not registry resolution, so
	// the seam confirms erun-devops published at every version instead of
	// reaching a live registry.
	t.Setenv("ERUN_PUBLISHED_CHART_PROBE_OVERRIDE", "erun-devops:*")
	handler := initTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{},
		Store:   initInteractionStore{},
		EnsureKubernetesNamespace: func(string, string) error {
			return nil
		},
		DeployHelmChart: func(eruncommon.HelmDeployParams) error {
			return nil
		},
		WaitForRemoteRuntime: func(eruncommon.ShellLaunchParams) error {
			return nil
		},
		RunRemoteCommand: fakeRemoteRepositoryStateRunner("repo_missing\n__ERUN_REMOTE_PUBLIC_KEY__\nssh-ed25519 AAAATEST remote\n"),
	}))

	_, output, err := handler(context.Background(), nil, InitInput{
		Tenant:             "frs",
		Environment:        "dev",
		Remote:             true,
		KubernetesContext:  "cluster-remote",
		ContainerRegistry:  eruncommon.DefaultContainerRegistry,
		ConfirmTenant:      boolPtr(true),
		ConfirmEnvironment: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("initTool failed: %v", err)
	}
	if output.Interaction == nil {
		t.Fatalf("expected interaction output, got %+v", output)
	}
	if output.Interaction.Type != eruncommon.BootstrapInitInteractionRemoteRepository {
		t.Fatalf("unexpected interaction: %+v", output.Interaction)
	}
}

func TestInitToolUsesExplicitRuntimeVersionOverride(t *testing.T) {
	stubKubectlAlwaysSucceeds(t)
	// The remote init flow deploys the runtime chart, which now refuses rather
	// than guess when no candidate is confirmed published; this test cares
	// about the deployed version, not registry resolution, so the seam
	// confirms erun-devops published at every version instead of reaching a
	// live registry.
	t.Setenv("ERUN_PUBLISHED_CHART_PROBE_OVERRIDE", "erun-devops:*")
	var deployedVersion string
	handler := initTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{},
		Store:   initInteractionStore{},
		EnsureKubernetesNamespace: func(string, string) error {
			return nil
		},
		DeployHelmChart: func(params eruncommon.HelmDeployParams) error {
			deployedVersion = params.Version
			return nil
		},
		WaitForRemoteRuntime: func(eruncommon.ShellLaunchParams) error {
			return nil
		},
		RunRemoteCommand: fakeRemoteRepositoryStateRunner("repo_exists\n__ERUN_REMOTE_PUBLIC_KEY__\nssh-ed25519 AAAATEST remote\n"),
	}))

	_, output, err := handler(context.Background(), nil, InitInput{
		Tenant:             "tenanta",
		Environment:        "dev",
		Version:            "1.0.19-snapshot-20260418141901",
		Remote:             true,
		KubernetesContext:  "cluster-dev",
		ContainerRegistry:  eruncommon.DefaultContainerRegistry,
		ConfirmTenant:      boolPtr(true),
		ConfirmEnvironment: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("initTool failed: %v", err)
	}
	if output.Interaction != nil {
		t.Fatalf("unexpected interaction output: %+v", output)
	}
	if deployedVersion != "1.0.19-snapshot-20260418141901" {
		t.Fatalf("unexpected deployed version %q", deployedVersion)
	}
}

func boolPtr(value bool) *bool {
	return &value
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
