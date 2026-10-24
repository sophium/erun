package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testDeployDryRunRemoteEnvClusterRegistryChartFromRuntimeImage(t *testing.T) {
	// With --cluster-registry, the runtime chart and RUNTIME_REGISTRY resolve from
	// the runtime image's registry: the in-cluster registry never holds the platform chart.
	setup := env.New(t)
	fixture.SeedClusterRegistryRemoteTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=ghcr.io/sophium/erun-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_remote_env_cluster_registry_chart_from_runtime_image", normalize.Apply(result.Combined))
}

func testDeployDryRunRuntimeChartResolvesFromThePlatformRegistry(t *testing.T) {
	// A deploy registry without charts falls back to the runtime image's registry;
	// the trace names each skipped rung.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=ghcr.io/sophium/erun-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_runtime_chart_resolves_from_the_platform_registry", normalize.Apply(result.Combined))
}

func testDeployDryRunRuntimeChartPrefersTheDeployRegistryOverThePlatformRegistry(t *testing.T) {
	// The runtime image's registry is a last resort: the deploy registry wins when
	// it publishes the chart.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=registry.example/test/erun-devops:1.0.0,ghcr.io/sophium/erun-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_runtime_chart_prefers_the_deploy_registry_over_the_platform_registry", normalize.Apply(result.Combined))
}

func testDeployDryRunOperatorSetRuntimeRegistryStandsOverADifferentResolution(t *testing.T) {
	// A deploy resolving outside the runtimeregistry memo keeps the memo and traces
	// the divergence.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	appendEnvConfig(t, setup, "team", "dev", "runtimeregistry: ghcr.io/petios\n")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=ghcr.io/sophium/erun-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_operator_set_runtime_registry_stands_over_a_different_resolution", normalize.Apply(result.Combined))
}

func testDeployRealRunRecordsTheRegistryTheRuntimeChartResolvedFrom(t *testing.T) {
	// The memo records the registry the chart resolved at, not where the search started.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envVars := deployStubEnv(t, setup, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=ghcr.io/sophium/erun-devops:1.0.99")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.99"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	assertEnvConfigContains(t, setup, "team", "dev", "runtimeregistry: ghcr.io/sophium")
}

func testDeployRealRunRecordsTheDeployRegistryWhenTheChartResolvesThere(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envVars := deployStubEnv(t, setup, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=registry.example/test/erun-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	assertEnvConfigContains(t, setup, "team", "dev", "runtimeregistry: registry.example/test")
}

func testDeployRealRunKeepsAnOperatorSetRuntimeRegistry(t *testing.T) {
	// A deploy never overwrites an operator-set runtimeregistry.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	appendEnvConfig(t, setup, "team", "dev", "runtimeregistry: ghcr.io/petios\n")
	envVars := deployStubEnv(t, setup, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=ghcr.io/sophium/erun-devops:1.0.99")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.99"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	assertEnvConfigContains(t, setup, "team", "dev", "runtimeregistry: ghcr.io/petios")
	assertEnvConfigContains(t, setup, "team", "dev", "runtimeversion: 1.0.99")
}

func testDeployDryRunRemoteEnvTenantComponentVerifiesAgainstTheClusterRegistry(t *testing.T) {
	// The component chart is verified against the cluster registry erun push
	// publishes to, not runtimeregistry.
	setup := env.New(t)
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, "team")
	envDir := filepath.Join(tenantDir, "dev")
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	mustWriteFile(t, filepath.Join(root, "config.yaml"), "defaulttenant: team\n")
	mustWriteFile(t, filepath.Join(tenantDir, "config.yaml"), "projectroot: "+setup.Cwd+"\nname: team\ndefaultenvironment: dev\n")
	mustWriteFile(t, filepath.Join(envDir, "config.yaml"),
		"name: dev\n"+
			"repopath: /nonexistent-remote/team\n"+
			"kubernetescontext: test-context\n"+
			"runtimeimage: ghcr.io/sophium/erun-devops\n"+
			"runtimeregistry: ghcr.io/sophium\n"+
			"runtimeversion: 1.0.0\n"+
			"type: remote-agent\n"+
			"containerregistries:\n"+
			"    - cluster: {insecure: true}\n"+
			"      roles:\n"+
			"        - build\n"+
			"        - deploy\n",
	)
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=<cluster-ip>:5000/team-backend-api:1.0.0")
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "1.0.0",
		"--components", "team-backend-api",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "verifying charts published at 1.0.0 in <cluster-ip>:5000: team-backend-api") {
		t.Fatalf("expected verification against the concretized cluster registry, not the platform runtimeregistry:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_remote_env_tenant_component_verifies_against_the_cluster_registry", normalize.Apply(result.Combined))
}

func testDeployRealRunPersistsRuntimeVersionAndRegistryToEnvConfig(t *testing.T) {
	// A real-run deploy persists the deployed RuntimeVersion and RuntimeRegistry.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedProjectK8sConfig(t, setup, "containerregistry: registry.example/published\n")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.99"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	envCfgPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	raw, err := os.ReadFile(envCfgPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, "runtimeversion: 1.0.99") {
		t.Fatalf("expected env config to be rewritten with runtimeversion: 1.0.99, got:\n%s", body)
	}
	if !strings.Contains(body, "runtimeregistry: registry.example/published") {
		t.Fatalf("expected env config to record runtimeregistry: registry.example/published, got:\n%s", body)
	}
}

func testDeployDryRunPersistedVersionReopenUsesRuntimeRegistryProvenance(t *testing.T) {
	// A reopen without --version renders against the persisted RuntimeRegistry,
	// not the current containerregistry.
	setup := env.New(t)
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, "team")
	envDir := filepath.Join(tenantDir, "dev")
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("defaulttenant: team\n"), 0o644); err != nil {
		t.Fatalf("root cfg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tenantDir, "config.yaml"),
		[]byte("projectroot: "+setup.Cwd+"\nname: team\ndefaultenvironment: dev\n"), 0o644); err != nil {
		t.Fatalf("tenant cfg: %v", err)
	}
	envBody := "name: dev\nrepopath: " + setup.Cwd + "\nkubernetescontext: test-context\nruntimeversion: 1.0.0\nruntimeregistry: registry.example/legacy\n"
	if err := os.WriteFile(filepath.Join(envDir, "config.yaml"), []byte(envBody), 0o644); err != nil {
		t.Fatalf("env cfg: %v", err)
	}
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedProjectK8sConfig(t, setup, "containerregistry: registry.example/current\n")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--current", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_persisted_version_reopen_uses_runtime_registry_provenance", normalize.Apply(result.Combined))
}

func testDeployDryRunExplicitVersionUsesProjectRegistryNotProvenance(t *testing.T) {
	// An explicit --version renders against the current containerregistry.
	setup := env.New(t)
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, "team")
	envDir := filepath.Join(tenantDir, "dev")
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("defaulttenant: team\n"), 0o644); err != nil {
		t.Fatalf("root cfg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tenantDir, "config.yaml"),
		[]byte("projectroot: "+setup.Cwd+"\nname: team\ndefaultenvironment: dev\n"), 0o644); err != nil {
		t.Fatalf("tenant cfg: %v", err)
	}
	envBody := "name: dev\nrepopath: " + setup.Cwd + "\nkubernetescontext: test-context\nruntimeversion: 1.0.0\nruntimeregistry: registry.example/legacy\n"
	if err := os.WriteFile(filepath.Join(envDir, "config.yaml"), []byte(envBody), 0o644); err != nil {
		t.Fatalf("env cfg: %v", err)
	}
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedProjectK8sConfig(t, setup, "containerregistry: registry.example/current\n")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.5", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_explicit_version_uses_project_registry_not_provenance", normalize.Apply(result.Combined))
}

func testDeployDryRunPersistedVersionWithoutProvenanceUsesProjectRegistry(t *testing.T) {
	// With no persisted runtimeregistry, reopen falls back to the current containerregistry.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedProjectK8sConfig(t, setup, "containerregistry: registry.example/current\n")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--current", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_persisted_version_without_provenance_uses_project_registry", normalize.Apply(result.Combined))
}
