package initenv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimedefaults"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

func TestInitToolReturnsInteractionWhenSharedInitNeedsInput(t *testing.T) {
	projectRoot := t.TempDir()

	handler := InitTool(runtimedefaults.NormalizeRuntimeConfig(runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{
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
	if output.Interaction.Type != model.BootstrapInitInteractionConfirmTenant {
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
	handler := InitTool(runtimedefaults.NormalizeRuntimeConfig(runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{},
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

	_, output, err := handler(context.Background(), nil, InitInput{
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
	if output.Interaction.Type != model.BootstrapInitInteractionRemoteRepository {
		t.Fatalf("unexpected interaction: %+v", output.Interaction)
	}
}

func TestInitToolUsesExplicitRuntimeVersionOverride(t *testing.T) {
	stubKubectlAlwaysSucceeds(t)
	stubGHCRDockerCredential(t)
	// Confirm the runtime chart as published without reaching a registry.
	t.Setenv("ERUN_PUBLISHED_CHART_PROBE_OVERRIDE", "erun-devops:*")
	var deployedVersion string
	handler := InitTool(runtimedefaults.NormalizeRuntimeConfig(runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{},
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

	_, output, err := handler(context.Background(), nil, InitInput{
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
