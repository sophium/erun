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

// TestBuildToolBuildsComponentWithMultiPlatformSpec: a component build on an environment whose build script is not disabled must
// still resolve a real, buildable docker spec (matching what the no-component
// path resolves via newDockerBuildSpec) rather than a spec with no platforms,
// which builds and pushes nothing while still reporting success.
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

// stubKubectlAlwaysSucceeds points the ERUN_KUBECTL_BIN seam (core.Command)
// at a script that exits 0 unconditionally, so deploy.RunHelmDeploy's own
// namespace-ensure — which runs before the pre-rollout secret applies and is not
// reachable through RuntimeConfig.EnsureKubernetesNamespace (see
// deploy.WrapHelmChartDeployerWithNamespaceEnsure and
// TestRunHelmDeployEnsuresTheNamespaceBeforeApplyingSecrets) — never shells out to
// a real cluster. Without it, these tests depend on the host's kubeconfig
// happening to have a context named "cluster-remote"/"cluster-dev", which no real
// machine does.
//
// `get secret ... -o json` is answered as NotFound rather than the same bare
// success every other invocation gets: the image-pull-secret refresh
// (registry.ExistingImagePullSecretAuths) reads a named Secret back before
// merging into it, and a bare `exit 0` with no stdout is not valid JSON. NotFound is exactly what a first deploy
// into a fresh namespace sees, and it is the shape every other image-pull-secret
// test already uses (see primitive/deploy's imagePullSecretKubectlStub).
func stubKubectlAlwaysSucceeds(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubectl")
	script := "#!/bin/sh\ncase \"$*\" in\n  *\"get secret\"*\"-o json\"*)\n    echo 'Error from server (NotFound): secrets not found' >&2\n    exit 1\n    ;;\n  *)\n    exit 0\n    ;;\nesac\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("ERUN_KUBECTL_BIN", path)
}

// stubGHCRDockerCredential points DOCKER_CONFIG at a fixture carrying a fake
// ghcr.io credential, so registry.ResolveOCIRegistryBasicAuth resolves one
// deterministically for these tests regardless of what the host machine
// actually has logged in -- without this, a remote init's runtime deploy
// (which defaults the tenant's own image onto ghcr.io) would take its
// pull-secret decision from whatever real ~/.docker/config.json this test
// happens to run next to, and fall back to a live, network-dependent
// anonymous-pullability probe on any host with no cached credential.
func stubGHCRDockerCredential(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"auths":{"ghcr.io":{"auth":"dGVzdDp0b2tlbg=="}}}`), 0o600); err != nil {
		t.Fatalf("write docker config fixture: %v", err)
	}
	t.Setenv("DOCKER_CONFIG", dir)
}

// fakeRemoteRepositoryStateRunner fakes RunRemoteCommand for a remote-init
// flow that reaches the repository-state script, answering the
// registry-credential-check script (identified by its own content, since
// every remote-exec call shares this one seam) with "credential configured"
// so these tests keep exercising repository interaction rather than tripping
// that check.
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
	// The remote init flow deploys the runtime chart, which now refuses rather
	// than guess when no candidate is confirmed published; this test cares
	// about the returned repository interaction, not registry resolution, so
	// the seam confirms erun-devops published at every version instead of
	// reaching a live registry.
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
	// The remote init flow deploys the runtime chart, which now refuses rather
	// than guess when no candidate is confirmed published; this test cares
	// about the deployed version, not registry resolution, so the seam
	// confirms erun-devops published at every version instead of reaching a
	// live registry.
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
