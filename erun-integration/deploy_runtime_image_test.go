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

func testDeployDryRunRemoteEnvTenantUmbrellaExplicitRuntimeImageWins(t *testing.T) {
	// An explicit runtimeimage overrides the umbrella's own-image default.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"runtimeimage: registry.example/acme/hotfix-devops:9.9.9\n")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=team-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_remote_env_tenant_umbrella_explicit_runtime_image_wins", normalize.Apply(result.Combined))
}

func testDeployDryRunRemoteEnvUmbrellaIgnoresStaleStockRuntimeimage(t *testing.T) {
	// A stale runtimeimage naming the stock erun-devops image is ignored on an
	// umbrella deploy: erun-devops:<tenant-version> is never published.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"runtimeimage: ghcr.io/sophium/erun-devops\n")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=team-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_remote_env_umbrella_ignores_stale_stock_runtimeimage", normalize.Apply(result.Combined))
}

func testDeployDryRunRemoteEnvUmbrellaHonoursTenantsOwnRuntimeimageTag(t *testing.T) {
	// A pin on the tenant's own image is versioned on the tenant's release line,
	// so a tag that differs from the deploy version is honoured verbatim.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"runtimeimage: registry.example/test/team-devops:1.0.353-snapshot-20260824165146\n")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=team-devops:1.0.51")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.51", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_remote_env_umbrella_honours_tenants_own_runtimeimage_tag", normalize.Apply(result.Combined))
}

func testDeployDryRunRemoteEnvStatedChartIgnoresStaleStockRuntimeimage(t *testing.T) {
	// A stated runtime chart ignores a stale stock pin; the tenant image resolves
	// against the cluster's registry, not the chart's.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+
		"runtimeregistry: ghcr.io/sophium\n"+
		"runtimeimage: ghcr.io/sophium/erun-devops\n"+
		"runtimechart: oci://ghcr.io/sophium/charts/erun-devops:1.0.0\n"+
		"containerregistries:\n    - registry: registry.example/tenant\n      roles:\n        - build\n        - deploy\n")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "2.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_remote_env_stated_chart_ignores_stale_stock_runtimeimage", normalize.Apply(result.Combined))
}

func testDeployDryRunRuntimeChartFlagRescuesAStockRuntimeImageOverride(t *testing.T) {
	// Image inference reads the --runtime-chart override, not the env's stale
	// recorded chart version.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+
		"runtimechart: oci://ghcr.io/sophium/charts/erun-devops:1.0.178\n"+
		"runtimeimage: ghcr.io/sophium/erun-devops\n")
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "1.0.201",
		"--runtime-image", "ghcr.io/sophium/erun-devops:1.0.201",
		"--runtime-chart", "oci://ghcr.io/sophium/charts/erun-devops:1.0.201",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Home, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "imageOverrides.erun-devops=ghcr.io/sophium/erun-devops:1.0.201") {
		t.Fatalf("operator's explicit --runtime-image was discarded: %s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_runtime_chart_flag_rescues_a_stock_runtime_image_override", normalize.Apply(result.Combined))
}

func testDeployDryRunRuntimeChartFlagAloneHealsAPersistedStockRuntimeimageMemo(t *testing.T) {
	// A persisted runtimeimage is also judged against --runtime-chart, not the
	// recorded chart version.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+
		"runtimechart: oci://ghcr.io/sophium/charts/erun-devops:1.0.178\n"+
		"runtimeimage: ghcr.io/sophium/erun-devops\n")
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "1.0.201",
		"--runtime-chart", "oci://ghcr.io/sophium/charts/erun-devops:1.0.201",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Home, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "imageOverrides.erun-devops=ghcr.io/sophium/erun-devops:1.0.201") {
		t.Fatalf("persisted runtimeimage memo was wrongly evicted: %s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_runtime_chart_flag_alone_heals_a_persisted_stock_runtimeimage_memo", normalize.Apply(result.Combined))
}

func testDeployDryRunRemoteEnvExplicitRuntimeImageWinsOnTheTenantsOwnVersionLine(t *testing.T) {
	// An explicit --runtime-image differing from the inferred default only by tag
	// is not a stale pin.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+
		"runtimeregistry: ghcr.io/sophium\n"+
		"containerregistries:\n    - registry: registry.example/tenant\n      roles:\n        - build\n        - deploy\n")
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "1.0.0",
		"--runtime-image", "registry.example/tenant/team-devops:9.9.9-snapshot-20260101010101",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Home, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "imageOverrides.erun-devops=registry.example/tenant/team-devops:9.9.9-snapshot-20260101010101") {
		t.Fatalf("operator's explicit --runtime-image was discarded: %s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_remote_env_explicit_runtime_image_wins_on_the_tenants_own_version_line", normalize.Apply(result.Combined))
}

func testDeployDryRunRemoteEnvCustomRuntimeImage(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"runtimeimage: registry.example/acme/my-devops:2.0.0\n")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_remote_env_custom_runtime_image", normalize.Apply(result.Combined))
}

func testDeployDryRunRemoteEnvRuntimeImageWithoutTag(t *testing.T) {
	// A tagless override is pinned to the runtime version; :latest is never published.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"runtimeimage: registry.example/acme/my-devops\n")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_remote_env_runtime_image_without_tag", normalize.Apply(result.Combined))
}

func testDeployDryRunRuntimeImageOverrideUsesPublishedChart(t *testing.T) {
	// --runtime-image reroutes to the published erun-devops chart even when a
	// repo-local runtime chart exists.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--runtime-image", "ghcr.io/sophium/erun-devops", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_runtime_image_override_uses_published_chart", normalize.Apply(result.Combined))
}

func testDeployDryRunRuntimeImageOverrideNoK8sTreeUsesPublishedChart(t *testing.T) {
	// --runtime-image with no k8s chart tree in the devops module bootstraps on
	// the published chart instead of failing.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsRuntimeDockerfile(t, setup, "team")
	if err := os.RemoveAll(filepath.Join(setup.Cwd, "team-devops", "k8s")); err != nil {
		t.Fatalf("remove k8s tree: %v", err)
	}
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--runtime-image", "ghcr.io/sophium/erun-devops", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_runtime_image_override_no_k8s_tree_uses_published_chart", normalize.Apply(result.Combined))
}

func testDeployRealRunHealsStaleRuntimeimageWhenMovingOntoTheTenantsOwnLine(t *testing.T) {
	// A persisted stock runtimeimage heals to the bare tenant name in the same
	// write as runtimeversion.
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnvNoRepoPath(t, setup, "frs", "build")
	appendEnvConfig(t, setup, "frs", "build", "runtimeimage: ghcr.io/sophium/erun-devops\n")
	envVars := deployStubEnv(t, setup,
		"ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=frs-devops:1.0.86",
		"ERUN_ANONYMOUS_PULLABLE_OVERRIDE=sophium/frs-devops:1.0.86",
	)
	result := erun.Run(t, []string{"deploy", "frs", "build", "--version", "1.0.86"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	assertEnvConfigContains(t, setup, "frs", "build",
		"runtimeversion: 1.0.86",
		"runtimeimage: frs-devops",
	)
	assertEnvConfigLacks(t, setup, "frs", "build", "runtimeimage: ghcr.io/sophium/erun-devops")
	golden.Equal(t, "deploy/real_run_heals_stale_runtimeimage_when_moving_onto_the_tenants_own_line", normalize.Apply(result.Combined))
}

func testDeployRealRunRefusesCrossLineRuntimeImageSwitchBeforeAnyMutation(t *testing.T) {
	// Only the release line of runtimerunningimage catches this stale pin. No
	// kubectl/helm stub: a late refusal would surface in the golden.
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnvNoRepoPath(t, setup, "frs", "build")
	appendEnvConfig(t, setup, "frs", "build",
		"runtimeimage: ghcr.io/sophium/erun-devops\n"+
			"runtimerunningimage: ghcr.io/sophium/frs-devops:1.0.84\n",
	)
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:*")
	result := erun.Run(t, []string{"deploy", "frs", "build", "--version", "1.0.86"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a refusal for a deploy that would silently switch release lines, got exit 0:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "kubectl") || strings.Contains(result.Combined, "helm upgrade") {
		t.Fatalf("refusal must fire before any cluster interaction: %s", result.Combined)
	}
	golden.Equal(t, "deploy/real_run_refuses_cross_line_runtime_image_switch_before_any_mutation", normalize.Apply(result.Combined))
}

func testDeployDryRunConsistentStockRuntimeImageEnvDeploysUnchanged(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnvNoRepoPath(t, setup, "frs", "local")
	appendEnvConfig(t, setup, "frs", "local",
		"runtimeimage: erun-devops\n"+
			"runtimerunningimage: ghcr.io/sophium/erun-devops:1.0.203\n",
	)
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:*")
	result := erun.Run(t, []string{"deploy", "frs", "local", "--version", "1.0.204", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_consistent_stock_runtime_image_env_deploys_unchanged", normalize.Apply(result.Combined))
}

func testDeployDryRunUnclassifiablePriorRuntimeImageWarnsButProceeds(t *testing.T) {
	// An unclassifiable runtimerunningimage is traced and the deploy proceeds.
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnvNoRepoPath(t, setup, "frs", "build")
	appendEnvConfig(t, setup, "frs", "build", "runtimerunningimage: ghcr.io/sophium/\n")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:*")
	result := erun.Run(t, []string{"deploy", "frs", "build", "--version", "1.0.86", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("expected an unclassifiable prior image to proceed rather than refuse, got exit %d:\n%s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "could not be classified") {
		t.Fatalf("expected a trace explaining the prior image could not be classified, got:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_unclassifiable_prior_runtime_image_warns_but_proceeds", normalize.Apply(result.Combined))
}

func testDeployDryRunRefusesADeployVersionFromAnotherProductsReleaseLine(t *testing.T) {
	// A fallback to the shared erun-devops chart while the env runs the frs line
	// is refused before any cluster interaction. A chart the tenant states itself
	// is not checked.
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnvNoRepoPath(t, setup, "frs", "build")
	appendEnvConfig(t, setup, "frs", "build",
		"runtimeimage: frs-devops\n"+
			"runtimerunningimage: ghcr.io/sophium/frs-devops:1.0.138\n",
	)
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.304")
	result := erun.Run(t, []string{"deploy", "frs", "build", "--version", "1.0.304", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a refusal for a version from another product's release line, got exit 0:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "helm upgrade") {
		t.Fatalf("refusal must fire before any cluster interaction: %s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_refuses_a_deploy_version_from_another_products_release_line", normalize.Apply(result.Combined))
}
