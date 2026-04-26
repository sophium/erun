package erunmcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if len(tools.Tools) != 11 {
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

func TestRawToolRunsCommandFromRuntimeRepoRoot(t *testing.T) {
	projectRoot := t.TempDir()
	handler := rawTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{RepoPath: projectRoot},
	}))

	_, output, err := handler(context.Background(), nil, RawInput{
		Command:   []string{"/bin/sh", "-c", "printf '%s:%s' \"$PWD\" \"$(cat)\""},
		Stdin:     "input",
		Verbosity: 1,
	})
	if err != nil {
		t.Fatalf("rawTool failed: %v", err)
	}
	if !output.Executed || output.WorkingDirectory != projectRoot {
		t.Fatalf("unexpected output metadata: %+v", output)
	}
	if output.Stdout != projectRoot+":input" {
		t.Fatalf("unexpected stdout: %q", output.Stdout)
	}
	if len(output.Trace) != 1 || !strings.Contains(output.Trace[0], "cd "+projectRoot+" && /bin/sh -c") {
		t.Fatalf("unexpected trace: %+v", output.Trace)
	}
}

func TestRawToolPreviewRedactsTraceWithoutExecuting(t *testing.T) {
	projectRoot := t.TempDir()
	handler := rawTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{RepoPath: projectRoot},
	}))

	_, output, err := handler(context.Background(), nil, RawInput{
		Command:   []string{"/bin/sh", "-c", "exit 1", "--token", "secret-value"},
		Preview:   true,
		Verbosity: 1,
	})
	if err != nil {
		t.Fatalf("rawTool preview failed: %v", err)
	}
	if output.Executed {
		t.Fatalf("expected preview output, got %+v", output)
	}
	joined := strings.Join(output.Trace, "\n")
	if !strings.Contains(joined, "--token '<redacted>'") || strings.Contains(joined, "secret-value") {
		t.Fatalf("unexpected trace: %+v", output.Trace)
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
	if output.CurrentDirectory.Effective.LocalPorts.RangeStart != 17000 || output.CurrentDirectory.Effective.LocalPorts.SSH != 17022 {
		t.Fatalf("unexpected effective local ports: %+v", output.CurrentDirectory.Effective.LocalPorts)
	}
	if len(output.Tenants) != 1 || output.Tenants[0].Name != "tenant-a" {
		t.Fatalf("unexpected tenants: %+v", output.Tenants)
	}
	if output.Tenants[0].Environments[0].LocalPorts.RangeEnd != 17099 {
		t.Fatalf("unexpected environment local ports: %+v", output.Tenants[0].Environments[0].LocalPorts)
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
	want := "docker build -t erunpaas/erun-devops:1.1.0 --build-arg ERUN_VERSION=1.1.0 -f " + filepath.Join(componentDir, "Dockerfile") + " ."
	if output.Trace[0] != "cd "+projectRoot+" && "+want {
		t.Fatalf("unexpected trace output: %+v", output.Trace)
	}
}

func TestBuildToolPreviewReleaseIncludesReleaseAndBuildTrace(t *testing.T) {
	projectRoot := createReleaseRuntimeRepo(t, "develop")
	if err := eruncommon.SaveProjectConfig(projectRoot, eruncommon.ProjectConfig{}); err != nil {
		t.Fatalf("SaveProjectConfig failed: %v", err)
	}

	handler := buildTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{
			Environment: eruncommon.DefaultEnvironment,
			RepoPath:    projectRoot,
		},
	}))

	_, output, err := handler(context.Background(), nil, BuildInput{
		Component: "api",
		Release:   true,
		Preview:   true,
		Verbosity: 1,
	})
	if err != nil {
		t.Fatalf("buildTool failed: %v", err)
	}
	if len(output.Trace) < 2 {
		t.Fatalf("expected release and build trace, got %+v", output.Trace)
	}
	if !strings.Contains(output.Trace[0], "release: branch=develop mode=candidate version=1.4.2-rc.") {
		t.Fatalf("unexpected release trace: %+v", output.Trace)
	}
	foundBuildTrace := false
	foundVersionReport := false
	for _, trace := range output.Trace {
		if strings.Contains(trace, "docker buildx build --builder erun-multiarch --platform 'linux/amd64,linux/arm64'") &&
			strings.Contains(trace, "-t erunpaas/api:1.4.2-rc.") &&
			strings.Contains(trace, "--push") {
			foundBuildTrace = true
		}
		if strings.Contains(trace, "release version: 1.4.2-rc.") {
			foundVersionReport = true
		}
	}
	if !foundBuildTrace {
		t.Fatalf("unexpected build trace: %+v", output.Trace)
	}
	if !foundVersionReport {
		t.Fatalf("expected final release version output, got %+v", output.Trace)
	}
}

func TestBuildToolPreviewAtRepoRootIncludesBuildTrace(t *testing.T) {
	projectRoot := t.TempDir()
	moduleRoot := filepath.Join(projectRoot, "tenant-a-devops")
	componentDirs := []string{
		filepath.Join(moduleRoot, "docker", "tenant-a-devops"),
		filepath.Join(moduleRoot, "docker", "erun-dind"),
	}
	for _, dir := range componentDirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir component dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
			t.Fatalf("write Dockerfile: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(componentDirs[0], "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDirs[1], "VERSION"), []byte("28.1.1\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := eruncommon.SaveProjectConfig(projectRoot, eruncommon.ProjectConfig{
		Environments: map[string]eruncommon.ProjectEnvironmentConfig{
			eruncommon.DefaultEnvironment: {ContainerRegistry: "erunpaas"},
		},
	}); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	handler := buildTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{
			Tenant:      "tenant-a",
			Environment: eruncommon.DefaultEnvironment,
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
	if len(output.Trace) != 2 {
		t.Fatalf("unexpected trace output: %+v", output.Trace)
	}
}

func TestDoctorToolPreviewIncludesDindCleanupTrace(t *testing.T) {
	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	runtime := normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{
			Tenant:      "tenant-a",
			Environment: "local",
			RepoPath:    projectRoot,
		},
		Store: listToolStore{
			toolConfig: eruncommon.ERunConfig{DefaultTenant: "tenant-a"},
			tenantConfigs: map[string]eruncommon.TenantConfig{
				"tenant-a": {
					Name:               "tenant-a",
					ProjectRoot:        projectRoot,
					DefaultEnvironment: "local",
				},
			},
			envConfigs: map[string]eruncommon.EnvConfig{
				"tenant-a/local": {
					Name:              "local",
					RepoPath:          projectRoot,
					KubernetesContext: "cluster-local",
				},
			},
			envsByTenant: map[string][]eruncommon.EnvConfig{
				"tenant-a": {{
					Name:              "local",
					RepoPath:          projectRoot,
					KubernetesContext: "cluster-local",
				}},
			},
		},
	})

	handler := doctorTool(runtime)
	_, output, err := handler(context.Background(), nil, DoctorInput{
		Preview:     true,
		Verbosity:   1,
		PruneImages: true,
	})
	if err != nil {
		t.Fatalf("doctorTool failed: %v", err)
	}
	if len(output.Trace) == 0 {
		t.Fatalf("expected trace output, got %+v", output)
	}
	joined := strings.Join(output.Trace, "\n")
	for _, want := range []string{
		"kubectl --context cluster-local --namespace tenant-a-local wait --for=condition=Available --timeout 2m0s deployment/tenant-a-devops",
		"kubectl --context cluster-local --namespace tenant-a-local exec -c erun-dind deployment/tenant-a-devops -- /bin/sh -lc '<remote-script>'",
		"docker system df",
		"docker image prune -a -f",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected trace to contain %q, got %+v", want, output.Trace)
		}
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

	_, _, err := handler(context.Background(), nil, PushInput{})
	if err == nil {
		t.Fatal("expected missing Dockerfile error")
	}
	if err.Error() != "dockerfile not found in current directory" {
		t.Fatalf("unexpected error: %v", err)
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

func TestBuildToolPreviewVerboseIncludesBuildScriptTrace(t *testing.T) {
	projectRoot := t.TempDir()
	scriptDir := filepath.Join(projectRoot, "scripts", "build")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("mkdir script dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
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
	if output.Trace[0] != "cd "+scriptDir+" && ./build.sh" {
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

func TestInitToolReturnsRepositoryInteractionForRemoteInit(t *testing.T) {
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
		RunRemoteCommand: func(eruncommon.ShellLaunchParams, string) (eruncommon.RemoteCommandResult, error) {
			return eruncommon.RemoteCommandResult{
				Stdout: "repo_missing\n__ERUN_REMOTE_PUBLIC_KEY__\nssh-ed25519 AAAATEST remote\n",
			}, nil
		},
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
		RunRemoteCommand: func(eruncommon.ShellLaunchParams, string) (eruncommon.RemoteCommandResult, error) {
			return eruncommon.RemoteCommandResult{
				Stdout: "repo_exists\n__ERUN_REMOTE_PUBLIC_KEY__\nssh-ed25519 AAAATEST remote\n",
			}, nil
		},
	}))

	_, output, err := handler(context.Background(), nil, InitInput{
		Tenant:             "tenant-a",
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
