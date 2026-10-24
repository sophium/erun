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
	// The `--cluster-registry` branch of the chart-registry resolver. The env's
	// deploy registry is the in-cluster erun-registry (a cluster: entry resolved
	// from the kube-context), which holds the tenant's built app images but never
	// the erun platform chart. So the runtime chart and RUNTIME_REGISTRY must
	// resolve from the runtime image's own registry (ghcr.io/sophium), not the
	// in-cluster pull host — resolving them to the cluster registry would fail
	// every chart pull. This locks the branch that fix 49f7f92f introduced, the
	// counterpart to the plain-env branch every other remote deploy golden pins.
	// The cluster entry is concretized in dry-run via the kubectl svc ClusterIP
	// lookup, which returns a placeholder without touching a cluster. The
	// ERUN_PUBLISHED_CHART_PROBE_OVERRIDE seam confirms erun-devops published
	// specifically in ghcr.io/sophium, which is what proves resolution reaches
	// that registry rather than refusing.
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
	// The reported deadlock: a tenant whose deploy registry is its own ECR
	// holds that tenant's app images and no charts/* repository at all, so
	// neither its umbrella nor the shared platform chart is in it and the
	// deploy failed at every version. The ladder's last rung looks where erun
	// actually publishes the platform chart — the runtime image's registry,
	// ghcr.io/sophium here — and the deploy resolves there instead. The trace
	// names each rung it passed over, so the search itself is auditable.
	// The registry-qualified ERUN_PUBLISHED_CHART_PROBE_OVERRIDE entry is what
	// puts erun-devops in one registry and not the other.
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
	// No-regression guard on the widened search: when the deploy registry does
	// publish the shared platform chart, that is still what installs — the
	// runtime image's registry is a last resort, not a preference. Both
	// registries publish erun-devops here, and the deploy stops at the first.
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
	// The memo is how an operator redirects the runtime chart search
	// (`erun init --runtime-registry`), so a deploy that resolves somewhere
	// else keeps the operator's value and says so, naming the command that
	// would change it. Without the trace the divergence is invisible: the
	// deploy succeeds either way, and only reading the config would show it.
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
	// The regression: the deploy memoized where the chart search STARTED, so
	// an env whose deploy registry carries no platform chart came out of a
	// successful deploy recording that registry as the place erun's artifacts
	// live — the one thing the search had just disproved, twice. The memo must
	// name the rung the chart resolved at. Real-run because the persist is the
	// side effect under test, and the assertion reads the config the goldens
	// cannot see.
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
	// The no-regression twin: a chart found where the search started is still
	// memoized as before, so following the resolution changed nothing for the
	// environments whose chart is published beside their own images.
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
	// `erun init --runtime-registry` is the documented way out of an env whose
	// deploy registry has no platform chart; a deploy that overwrote it reset
	// the field by doing the very thing it was set to enable. The chart here
	// resolves at ghcr.io/sophium while the operator named ghcr.io/petios, and
	// the operator's value survives.
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
	// erun#1598: a `--cluster-registry` env with an explicit runtimeregistry
	// (a realistic pairing -- runtimeregistry names where the shared
	// platform chart lives, containerregistries names the in-cluster
	// registry the tenant's own images and component charts publish to)
	// could never confirm its own component chart, because verification
	// probed the platform-chart registry (runtimeregistry) instead of the
	// registry `erun push` actually publishes tenant charts to. The
	// registry-qualified ERUN_PUBLISHED_CHART_PROBE_OVERRIDE entry below is
	// published only at the concretized cluster pull address (the dry-run
	// placeholder <cluster-ip>:5000), never at the runtimeregistry, so this
	// only passes when resolution reaches the cluster registry -- exactly
	// the registry the cluster entry is marked insecure for, which
	// erun-common's registry-scheme unit tests cover directly (a live HTTPS
	// probe against a plain-HTTP registry cannot be observed from this
	// harness; see erun-integration/AGENTS.md's coverage-gaps section).
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
	// Regression: `erun deploy --version X` updates helm's release
	// appVersion but used to leave EnvConfig.RuntimeVersion at the
	// previously persisted value, so the desktop runtime dialog and
	// `erun list` kept showing the stale string. Real-run deploy now
	// writes the deployed version back to the env config.
	//
	// This extends to the source registry, persisted
	// alongside the version as RuntimeRegistry, so a subsequent
	// reopen can address the same image even if the user edits the
	// project's container registry afterwards.
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
	// When the env has a persisted (RuntimeVersion,
	// RuntimeRegistry) pair and the user reopens without --version,
	// helm renders the runtime chart against RuntimeRegistry — not
	// the project's currently-configured containerregistry. This
	// protects users who edit the project registry after a deploy:
	// the previously-deployed image stays reachable on reopen.
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
	// An explicit --version is a fresh deploy intent.
	// Even with RuntimeRegistry pinned in env config, helm renders
	// against the project's current containerregistry — and the
	// post-deploy persist step will rewrite RuntimeRegistry to that
	// value. The provenance pin only protects no-override reopens.
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
	// Legacy envs persisted by older binaries have
	// runtimeversion but no runtimeregistry. On reopen we must NOT
	// invent a provenance — fall back to the project's current
	// containerregistry, the same behaviour callers had before the
	// field existed. The next successful deploy backfills the pair.
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
