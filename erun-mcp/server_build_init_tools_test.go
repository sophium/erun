package erunmcp

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/setup"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/shared"
	"github.com/sophium/erun/erun-mcp/tools/build"
	"github.com/sophium/erun/erun-mcp/tools/initenv"
	sharedpkg "github.com/sophium/erun/erun-mcp/tools/shared"
)

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
	handler := build.BuildTool(normalizeRuntimeConfig(sharedpkg.RuntimeConfig{
		Context: sharedpkg.RuntimeContext{
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
		BuildDockerImage: func(shared.DockerBuildSpec, io.Writer, io.Writer) error {
			t.Fatal("unexpected docker build")
			return nil
		},
	}))

	_, output, err := handler(context.Background(), nil, build.BuildInput{})
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

// newComponentBuildFixture includes a root build.sh so the build script is not disabled.
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

// A component build must still resolve a spec with platforms; one without
// builds nothing yet reports success.
func TestBuildToolBuildsComponentWithMultiPlatformSpec(t *testing.T) {
	projectRoot := newComponentBuildFixture(t)

	var captured *shared.DockerBuildSpec
	handler := build.BuildTool(normalizeRuntimeConfig(sharedpkg.RuntimeConfig{
		Context: sharedpkg.RuntimeContext{
			Environment: "dev",
			RepoPath:    projectRoot,
		},
		BuildScriptRunner: func(dir, path string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
			t.Fatal("unexpected build script call: --component must resolve the docker context directly")
			return nil
		},
		BuildDockerImage: func(spec shared.DockerBuildSpec, stdout, stderr io.Writer) error {
			captured = &spec
			return nil
		},
	}))

	_, output, err := handler(context.Background(), nil, build.BuildInput{Component: "web", NoIncremental: true})
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

// The erun-cli integration suite never enters this MCP wrapper, so error
// propagation is covered here.
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

	handler := build.BuildTool(normalizeRuntimeConfig(sharedpkg.RuntimeConfig{
		Context: sharedpkg.RuntimeContext{
			Environment: "dev",
			RepoPath:    projectRoot,
		},
		BuildDockerImage: func(shared.DockerBuildSpec, io.Writer, io.Writer) error {
			t.Fatal("unexpected docker build: resolution must fail before execution")
			return nil
		},
	}))

	_, _, err := handler(context.Background(), nil, build.BuildInput{Component: "web", Preview: true})
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

	handler := initenv.InitTool(normalizeRuntimeConfig(sharedpkg.RuntimeConfig{
		Context: sharedpkg.RuntimeContext{
			Tenant:   "tenant-a",
			RepoPath: projectRoot,
		},
		Store: initInteractionStore{},
	}))

	_, output, err := handler(context.Background(), nil, initenv.InitInput{})
	if err != nil {
		t.Fatalf("initTool failed: %v", err)
	}
	if output.Interaction == nil && output.Executed {
		t.Fatalf("expected interaction output, got %+v", output)
	}
	if output.Interaction == nil {
		t.Fatalf("expected interaction output, got %+v", output)
	}
	if output.Interaction.Type != setup.BootstrapInitInteractionConfirmTenant {
		t.Fatalf("unexpected interaction: %+v", output.Interaction)
	}
	if output.Executed {
		t.Fatalf("expected non-executed interaction output, got %+v", output)
	}
}

// stubKubectlAlwaysSucceeds keeps RunHelmDeploy off a real cluster. `get secret`
// answers NotFound, since an empty stdout is not valid JSON for the pull-secret read.
func stubKubectlAlwaysSucceeds(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubectl")
	script := "#!/bin/sh\ncase \"$*\" in\n  *\"get secret\"*\"-o json\"*)\n    echo 'Error from server (NotFound): secrets not found' >&2\n    exit 1\n    ;;\n  *)\n    exit 0\n    ;;\nesac\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("ERUN_KUBECTL_BIN", path)
}

// stubGHCRDockerCredential makes the ghcr.io pull-secret decision independent
// of the host's docker login and the network.
func stubGHCRDockerCredential(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"auths":{"ghcr.io":{"auth":"dGVzdDp0b2tlbg=="}}}`), 0o600); err != nil {
		t.Fatalf("write docker config fixture: %v", err)
	}
	t.Setenv("DOCKER_CONFIG", dir)
}

// fakeRemoteRepositoryStateRunner answers the registry-credential check
// (recognized by its script content) as configured.
func fakeRemoteRepositoryStateRunner(repositoryStateStdout string) func(configpkg.ShellLaunchParams, string) (kube.RemoteCommandResult, error) {
	return func(_ configpkg.ShellLaunchParams, script string) (kube.RemoteCommandResult, error) {
		if strings.Contains(script, ".docker/config.json") {
			return kube.RemoteCommandResult{Stdout: "1\n"}, nil
		}
		return kube.RemoteCommandResult{Stdout: repositoryStateStdout}, nil
	}
}

func TestInitToolReturnsRepositoryInteractionForRemoteInit(t *testing.T) {
	stubKubectlAlwaysSucceeds(t)
	stubGHCRDockerCredential(t)
	// Confirm the runtime chart as published without reaching a registry.
	t.Setenv("ERUN_PUBLISHED_CHART_PROBE_OVERRIDE", "erun-devops:*")
	handler := initenv.InitTool(normalizeRuntimeConfig(sharedpkg.RuntimeConfig{
		Context: sharedpkg.RuntimeContext{},
		Store:   initInteractionStore{},
		EnsureKubernetesNamespace: func(string, string) error {
			return nil
		},
		DeployHelmChart: func(deploy.HelmDeployParams) error {
			return nil
		},
		WaitForRemoteRuntime: func(configpkg.ShellLaunchParams) error {
			return nil
		},
		RunRemoteCommand: fakeRemoteRepositoryStateRunner("repo_missing\n__ERUN_REMOTE_PUBLIC_KEY__\nssh-ed25519 AAAATEST remote\n"),
	}))

	_, output, err := handler(context.Background(), nil, initenv.InitInput{
		Tenant:             "frs",
		Environment:        "dev",
		Remote:             true,
		KubernetesContext:  "cluster-remote",
		ContainerRegistry:  configpkg.DefaultContainerRegistry,
		ConfirmTenant:      boolPtr(true),
		ConfirmEnvironment: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("initTool failed: %v", err)
	}
	if output.Interaction == nil {
		t.Fatalf("expected interaction output, got %+v", output)
	}
	if output.Interaction.Type != setup.BootstrapInitInteractionRemoteRepository {
		t.Fatalf("unexpected interaction: %+v", output.Interaction)
	}
}

func TestInitToolUsesExplicitRuntimeVersionOverride(t *testing.T) {
	stubKubectlAlwaysSucceeds(t)
	stubGHCRDockerCredential(t)
	// Confirm the runtime chart as published without reaching a registry.
	t.Setenv("ERUN_PUBLISHED_CHART_PROBE_OVERRIDE", "erun-devops:*")
	var deployedVersion string
	handler := initenv.InitTool(normalizeRuntimeConfig(sharedpkg.RuntimeConfig{
		Context: sharedpkg.RuntimeContext{},
		Store:   initInteractionStore{},
		EnsureKubernetesNamespace: func(string, string) error {
			return nil
		},
		DeployHelmChart: func(params deploy.HelmDeployParams) error {
			deployedVersion = params.Version
			return nil
		},
		WaitForRemoteRuntime: func(configpkg.ShellLaunchParams) error {
			return nil
		},
		RunRemoteCommand: fakeRemoteRepositoryStateRunner("repo_exists\n__ERUN_REMOTE_PUBLIC_KEY__\nssh-ed25519 AAAATEST remote\n"),
	}))

	_, output, err := handler(context.Background(), nil, initenv.InitInput{
		Tenant:             "tenanta",
		Environment:        "dev",
		Version:            "1.0.19-snapshot-20260418141901",
		Remote:             true,
		KubernetesContext:  "cluster-dev",
		ContainerRegistry:  configpkg.DefaultContainerRegistry,
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
