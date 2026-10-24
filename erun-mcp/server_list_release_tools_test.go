package erunmcp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-mcp/tools/build"
	"github.com/sophium/erun/erun-mcp/tools/cloud"
	"github.com/sophium/erun/erun-mcp/tools/list"
	sharedpkg "github.com/sophium/erun/erun-mcp/tools/shared"
)

func TestCloudSetToolSetsEnvironmentAlias(t *testing.T) {
	projectRoot := t.TempDir()
	store := &listToolStore{
		tenantConfigs: map[string]model.TenantConfig{
			"frs": {Name: "frs", DefaultEnvironment: "dev"},
		},
		envConfigs: map[string]configpkg.EnvConfig{
			"frs/dev": {
				Name:               "dev",
				LocalRepoPath:      projectRoot,
				KubernetesContext:  "cluster-dev",
				CloudProviderAlias: "old-cloud",
			},
		},
	}
	handler := cloud.CloudSetTool(normalizeRuntimeConfig(sharedpkg.RuntimeConfig{
		Context: sharedpkg.RuntimeContext{Tenant: "frs", Environment: "dev"},
		Store:   store,
	}))

	_, result, err := handler(context.Background(), nil, cloud.CloudSetInput{Alias: "team-cloud"})
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

func TestListToolReturnsConfiguredTenantsAndEffectiveTarget(t *testing.T) {
	projectRoot := t.TempDir()
	handler := list.ListTool(normalizeRuntimeConfig(sharedpkg.RuntimeConfig{
		Context: sharedpkg.RuntimeContext{
			Tenant:      "tenant-a",
			Environment: "dev",
			RepoPath:    projectRoot,
		},
		Store: listToolStore{
			toolConfig: configpkg.ERunConfig{DefaultTenant: "tenant-a"},
			tenantConfigs: map[string]model.TenantConfig{
				"tenant-a": {
					Name:               "tenant-a",
					DefaultEnvironment: "dev",
				},
			},
			envConfigs: map[string]configpkg.EnvConfig{
				"tenant-a/dev": {
					Name:              "dev",
					LocalRepoPath:     projectRoot,
					KubernetesContext: "cluster-dev",
				},
			},
			envsByTenant: map[string][]configpkg.EnvConfig{
				"tenant-a": {{
					Name:              "dev",
					LocalRepoPath:     projectRoot,
					KubernetesContext: "cluster-dev",
				}},
			},
		},
	}))

	_, output, err := handler(context.Background(), nil, list.ListInput{})
	if err != nil {
		t.Fatalf("listTool failed: %v", err)
	}

	assertListToolOutput(t, output)
}

func assertListToolOutput(t *testing.T, output list.ListToolResult) {
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

func assertListEffectiveTarget(t *testing.T, target lifecycle.ListEffectiveTargetResult) {
	t.Helper()

	if target.Tenant != "tenant-a" || target.Environment != "dev" {
		t.Fatalf("unexpected effective target: %+v", target)
	}
	if target.LocalPorts.RangeStart != 17000 || target.LocalPorts.SSH != 17022 {
		t.Fatalf("unexpected effective local ports: %+v", target.LocalPorts)
	}
}

func TestReleaseToolPreview(t *testing.T) {
	// A failing docker reads as "no cached image tag", even in preview.
	t.Setenv("ERUN_DOCKER_BIN", "false")
	projectRoot := createReleaseRuntimeRepo(t, "develop")
	if err := configpkg.SaveProjectConfig(projectRoot, configpkg.ProjectConfig{}); err != nil {
		t.Fatalf("SaveProjectConfig failed: %v", err)
	}

	handler := build.ReleaseTool(normalizeRuntimeConfig(sharedpkg.RuntimeConfig{
		Context: sharedpkg.RuntimeContext{
			RepoPath: projectRoot,
		},
	}))

	_, output, err := handler(context.Background(), nil, build.ReleaseInput{Preview: true, Verbosity: 1})
	if err != nil {
		t.Fatalf("releaseTool failed: %v", err)
	}

	if output.Executed {
		t.Fatalf("expected preview output, got %+v", output)
	}
	if output.Spec.Mode != model.ReleaseModeCandidate || output.Spec.Version == "" || len(output.Spec.Stages) == 0 {
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

	handler := build.PushTool(normalizeRuntimeConfig(sharedpkg.RuntimeConfig{
		Context: sharedpkg.RuntimeContext{
			Environment: configpkg.DefaultEnvironment,
			RepoPath:    projectRoot,
		},
	}))

	// A version gets past the version gate to the rejection under test.
	_, _, err := handler(context.Background(), nil, build.PushInput{Version: "1.0.0"})
	if err == nil {
		t.Fatal("expected missing Dockerfile error")
	}
	if err.Error() != "dockerfile not found in current directory" {
		t.Fatalf("unexpected error: %v", err)
	}
}

// push publishes a built version and never mints one (erun-mcp/AGENTS.md).
func TestPushToolRequiresVersion(t *testing.T) {
	handler := build.PushTool(normalizeRuntimeConfig(sharedpkg.RuntimeConfig{
		Context: sharedpkg.RuntimeContext{
			Environment: configpkg.DefaultEnvironment,
			RepoPath:    t.TempDir(),
		},
	}))

	if _, _, err := handler(context.Background(), nil, build.PushInput{}); !errors.Is(err, build.ErrMissingPushVersion) {
		t.Fatalf("expected errMissingPushVersion, got %v", err)
	}
}
