package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func initRegistryScenarios(t *testing.T) {
	t.Run("cluster_registry_dry_run", testInitClusterRegistryDryRun)
	t.Run("cluster_registry_conflicts_with_container_registry", testInitClusterRegistryConflictsWithContainerRegistry)
	t.Run("erun_registry_dry_run", testInitErunRegistryDryRun)
	t.Run("erun_registry_conflicts_with_container_registry", testInitErunRegistryConflictsWithContainerRegistry)
	t.Run("erun_registry_conflicts_with_cluster_registry", testInitErunRegistryConflictsWithClusterRegistry)
	t.Run("dry_run_with_mcp_auth_public_key", testInitDryRunWithMcpAuthPublicKey)
}

func testInitClusterRegistryDryRun(t *testing.T) {
	setup := env.New(t)
	args := []string{
		"init", "team", "dev",
		"--type", "remote-agent",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--cluster-registry",
		"--no-git",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	// The probe override confirms erun-devops is published so the bootstrap deploy proceeds.
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/cluster_registry_dry_run", normalize.Apply(result.Combined))
}

func testInitClusterRegistryConflictsWithContainerRegistry(t *testing.T) {
	setup := env.New(t)
	args := []string{
		"init", "team", "dev",
		"--type", "remote-agent",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--cluster-registry",
		"--container-registry", "registry.example/test",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for conflicting flags, got 0: %s", result.Combined)
	}
	golden.Equal(t, "init/cluster_registry_conflicts_with_container_registry", normalize.Apply(result.Combined))
}

func testInitErunRegistryDryRun(t *testing.T) {
	setup := env.New(t)
	args := []string{
		"init", "team", "dev",
		"--type", "remote-agent",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--erun-registry",
		"--no-git",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	// The probe override confirms erun-devops is published so the bootstrap deploy proceeds.
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/erun_registry_dry_run", normalize.Apply(result.Combined))
}

func testInitErunRegistryConflictsWithContainerRegistry(t *testing.T) {
	setup := env.New(t)
	args := []string{
		"init", "team", "dev",
		"--type", "remote-agent",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--erun-registry",
		"--container-registry", "registry.example/test",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for conflicting flags, got 0: %s", result.Combined)
	}
	golden.Equal(t, "init/erun_registry_conflicts_with_container_registry", normalize.Apply(result.Combined))
}

func testInitErunRegistryConflictsWithClusterRegistry(t *testing.T) {
	setup := env.New(t)
	args := []string{
		"init", "team", "dev",
		"--type", "remote-agent",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--erun-registry",
		"--cluster-registry",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for conflicting flags, got 0: %s", result.Combined)
	}
	golden.Equal(t, "init/erun_registry_conflicts_with_cluster_registry", normalize.Apply(result.Combined))
}

func testInitDryRunWithMcpAuthPublicKey(t *testing.T) {
	// The MCP-auth key folds into init's single runtime deploy, so no redeploy is needed.
	setup := env.New(t)
	keyPath := filepath.Join(t.TempDir(), "desktopid.pub")
	if err := os.WriteFile(keyPath, []byte("-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAtestkeytestkeytestkeytestkeytestkeytestke=\n-----END PUBLIC KEY-----\n"), 0o600); err != nil {
		t.Fatalf("write public key fixture: %v", err)
	}
	args := []string{
		"init", "team", "dev",
		"--type", "remote-agent",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--mcp-auth-public-key", keyPath,
		"--no-git",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	// The probe override confirms erun-devops is published so the bootstrap deploy proceeds.
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/dry_run_with_mcp_auth_public_key", normalize.Apply(result.Combined))
}
