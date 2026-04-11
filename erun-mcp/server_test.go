package erunmcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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

func TestHTTPHandlerExposesVersionTool(t *testing.T) {
	cfg := HTTPConfig{Path: "/mcp"}
	info := eruncommon.BuildInfo{
		Version: "1.2.3",
		Commit:  "abcdef",
		Date:    "2024-01-01",
	}

	httpServer := httptest.NewServer(newHTTPHandler(info, cfg, RuntimeConfig{}))
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
	if len(tools.Tools) != 7 {
		t.Fatalf("unexpected tools: %+v", tools.Tools)
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
					ProjectRoot:        projectRoot,
					DefaultEnvironment: "dev",
				},
			},
			envConfigs: map[string]eruncommon.EnvConfig{
				"tenant-a/dev": {
					Name:              "dev",
					RepoPath:          projectRoot,
					KubernetesContext: "cluster-dev",
				},
			},
			envsByTenant: map[string][]eruncommon.EnvConfig{
				"tenant-a": {{
					Name:              "dev",
					RepoPath:          projectRoot,
					KubernetesContext: "cluster-dev",
				}},
			},
		},
	}))

	_, output, err := handler(context.Background(), nil, ListInput{})
	if err != nil {
		t.Fatalf("listTool failed: %v", err)
	}

	if output.Defaults.Tenant != "tenant-a" || output.Defaults.Environment != "dev" {
		t.Fatalf("unexpected defaults: %+v", output.Defaults)
	}
	if output.CurrentDirectory.Effective == nil {
		t.Fatalf("expected effective target, got %+v", output.CurrentDirectory)
	}
	if output.CurrentDirectory.Effective.Tenant != "tenant-a" || output.CurrentDirectory.Effective.Environment != "dev" {
		t.Fatalf("unexpected effective target: %+v", output.CurrentDirectory.Effective)
	}
	if len(output.Tenants) != 1 || output.Tenants[0].Name != "tenant-a" {
		t.Fatalf("unexpected tenants: %+v", output.Tenants)
	}
}

func TestBuildToolRunsErunBuildWithResolvedContext(t *testing.T) {
	projectRoot := t.TempDir()
	componentDir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(componentDir, 0o755); err != nil {
		t.Fatalf("mkdir component dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}

	handler := buildTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{
			Environment: "dev",
			RepoPath:    projectRoot,
		},
	}))

	_, output, err := handler(context.Background(), nil, BuildInput{
		Component: "erun-devops",
		Preview:   true,
	})
	if err != nil {
		t.Fatalf("buildTool failed: %v", err)
	}

	if output.WorkingDirectory != projectRoot {
		t.Fatalf("unexpected working directory: %+v", output)
	}
	if len(output.Trace) != 0 {
		t.Fatalf("did not expect trace output at default preview verbosity, got %+v", output.Trace)
	}
}

func TestBuildToolPreviewVerboseIncludesTrace(t *testing.T) {
	projectRoot := t.TempDir()
	componentDir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(componentDir, 0o755); err != nil {
		t.Fatalf("mkdir component dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}

	handler := buildTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{
			Environment: "dev",
			RepoPath:    projectRoot,
		},
	}))

	_, output, err := handler(context.Background(), nil, BuildInput{
		Component: "erun-devops",
		Preview:   true,
		Verbosity: 1,
	})
	if err != nil {
		t.Fatalf("buildTool failed: %v", err)
	}
	if len(output.Trace) == 0 {
		t.Fatalf("expected trace output at preview verbosity 1, got %+v", output)
	}
	want := "docker build -t erunpaas/erun-devops:1.1.0 -f " + filepath.Join(componentDir, "Dockerfile") + " ."
	if output.Trace[0] != "cd "+projectRoot+" && "+want {
		t.Fatalf("unexpected trace output: %+v", output.Trace)
	}
}

func TestBuildToolRunsProjectBuildScriptWhenPresent(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write build.sh: %v", err)
	}

	var called bool
	handler := buildTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{
			Environment: "dev",
			RepoPath:    projectRoot,
		},
		BuildScriptRunner: func(dir, path string, stdin io.Reader, stdout, stderr io.Writer) error {
			called = true
			if dir != projectRoot || path != "./build.sh" {
				t.Fatalf("unexpected build script call: dir=%q path=%q", dir, path)
			}
			return nil
		},
		BuildDockerImage: func(string, string, string, io.Writer, io.Writer) error {
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

func TestBuildToolPreviewVerboseIncludesBuildScriptTrace(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write build.sh: %v", err)
	}

	handler := buildTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{
			Environment: "dev",
			RepoPath:    projectRoot,
		},
	}))

	_, output, err := handler(context.Background(), nil, BuildInput{
		Preview:   true,
		Verbosity: 1,
	})
	if err != nil {
		t.Fatalf("buildTool failed: %v", err)
	}
	if len(output.Trace) == 0 {
		t.Fatalf("expected trace output, got %+v", output)
	}
	if output.Trace[0] != "cd "+projectRoot+" && ./build.sh" {
		t.Fatalf("unexpected trace output: %+v", output.Trace)
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
