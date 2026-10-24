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

func testDeployRealRunRemoteEnvPublishedChartViaStubs(t *testing.T) {
	// Real run proves an OCI reference skips local chart preparation.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	envVars = append(envVars, fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/real_run_remote_env_published_chart_via_stubs", normalize.Apply(result.Combined))
}

func testDeployRealRunRemoteEnvTenantUmbrellaPullsBundledValues(t *testing.T) {
	// Top-level --sets don't reach a wrapped subchart, so they are re-scoped and the
	// bundled values.<env>.yaml is pulled; only a real run exercises the pull.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "helm", "")
	envVars := append(setup.Env(),
		"ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=team-backend-api:1.0.0")
	envVars = append(envVars, fixture.StubEnv(stubs, "kubectl", "helm")...)
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "1.0.0",
		"--components", "team-backend-api",
	}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/real_run_remote_env_tenant_umbrella_pulls_bundled_values", normalize.Apply(result.Combined))
}

func testDeployDryRunPinnedVersionInstallsBuildableChartWithoutBuilding(t *testing.T) {
	// --version installs by reference even when the image is buildable locally:
	// no docker build, fingerprint probe, or promote line.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsRuntimeDockerfile(t, setup, "team")
	seedDevopsChartRuntimeImage(t, setup, "team", "ghcr.io/sophium/team-devops:{{ .Chart.AppVersion }}")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_pinned_version_installs_buildable_chart_without_building", normalize.Apply(result.Combined))
}

func testDeployRealRunPinnedVersionMissingImageErrors(t *testing.T) {
	// The existence check runs only in real mode; the docker stub reports both lookups absent.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	seedDevopsChartRuntimeImage(t, setup, "team", "ghcr.io/sophium/team-devops:{{ .Chart.AppVersion }}")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "docker", fixture.StubBinarySpec{ExitCode: 1})
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "kubectl", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "helm", "kubectl")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when the pinned version's image is absent, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/real_run_pinned_version_missing_image_errors", normalize.Apply(result.Combined))
}

func testDeployDryRunRemoteEnvRefusesWhenNoRuntimeChartIsConfirmed(t *testing.T) {
	// erun-devops rides erun's release line, so substituting it at the tenant's
	// version would be a coordinate that never exists.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	// The harness default confirms erun-devops at any version; reset it to nothing.
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected deploy to refuse when no runtime chart candidate is confirmed, got exit 0:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "helm upgrade") {
		t.Fatalf("deploy must never attempt to install a chart coordinate it has not confirmed, got:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_remote_env_refuses_when_no_runtime_chart_is_confirmed", normalize.Apply(result.Combined))
}

func testDeployDryRunRefusesSharedChartConfirmedOnlyAtADifferentVersion(t *testing.T) {
	// erun-devops exists only on erun's line (1.0.201), so 1.0.72 must be refused,
	// not assembled.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=ghcr.io/sophium/erun-devops:1.0.201")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.72", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected deploy to refuse rather than install erun-devops at the tenant's version, got exit 0:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "erun-devops:1.0.72") || strings.Contains(result.Combined, "erun-devops --version 1.0.72") {
		t.Fatalf("deploy must never carry the tenant's version onto the shared erun-devops chart, got:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "helm upgrade") {
		t.Fatalf("deploy must never attempt to install a chart coordinate it has not confirmed, got:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_refuses_shared_chart_confirmed_only_at_a_different_version", normalize.Apply(result.Combined))
}

func testDeployDryRunRemoteEnvPrefersTenantPublishedRuntimeChart(t *testing.T) {
	// A published tenant umbrella wins over erun-devops in either registry, and with
	// no runtimeimage the image defaults to <registry>/team-devops:<version>.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=registry.example/test/team-devops:1.0.0,ghcr.io/sophium/erun-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	golden.Equal(t, "deploy/dry_run_remote_env_prefers_tenant_published_runtime_chart", normalize.Apply(result.Combined))
}

func testDeployDryRunMovesTheTenantUmbrellaPinWithTheDeployVersion(t *testing.T) {
	// The umbrella is on the tenant's line, so it moves with --version along with the image.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "frs", "prod", "/nonexistent-remote/frs")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "frs", "prod", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+
		"runtimeregistry: ghcr.io/sophium\n"+
		"runtimechart: oci://ghcr.io/sophium/charts/frs-devops:1.0.142\n")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=frs-devops:1.0.143")
	result := erun.Run(t, []string{"deploy", "frs", "prod", "--version", "1.0.143", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	// The golden normalizes versions, so assert the umbrella and image versions verbatim.
	if !strings.Contains(result.Combined, "charts/frs-devops --version 1.0.143") {
		t.Errorf("the umbrella is not installed at the deploy version:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "imageOverrides.erun-devops=registry.example/test/frs-devops:1.0.143") {
		t.Errorf("the image override does not name the deploy version:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_moves_the_tenant_umbrella_pin_with_the_deploy_version", normalize.Apply(result.Combined))
}

func testDeployDryRunSaysWhenItHoldsTheTenantUmbrellaBack(t *testing.T) {
	// An unpublished umbrella version keeps the stated one, but deploy must say so.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "frs", "prod", "/nonexistent-remote/frs")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "frs", "prod", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+
		"runtimeregistry: ghcr.io/sophium\n"+
		"runtimechart: oci://ghcr.io/sophium/charts/frs-devops:1.0.142\n")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=frs-devops:1.0.142")
	result := erun.Run(t, []string{"deploy", "frs", "prod", "--version", "1.0.143", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "charts/frs-devops --version 1.0.142") {
		t.Errorf("an unconfirmed coordinate must not be substituted:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "imageOverrides.erun-devops=registry.example/test/frs-devops:1.0.143") {
		t.Errorf("the runtime image still moves to the deploy version:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "holding back the env's runtime umbrella") {
		t.Errorf("the deploy does not say it left the umbrella behind:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_says_when_it_holds_the_tenant_umbrella_back", normalize.Apply(result.Combined))
}

func testDeployDryRunRemoteEnvDeploysPublishedComponents(t *testing.T) {
	// Sourceless path: components install by OCI reference in rank order, and the
	// runtime stays on the tenant's own published chart.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envVars := append(setup.Env(),
		"ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=team-devops:1.0.0,erun-backend-api:1.0.0,erun-backend-postgres:1.0.0,erun-backend-db:1.0.0")
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "1.0.0",
		"--components", "erun-backend-api,erun-backend-postgres,erun-backend-db,team-devops",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	golden.Equal(t, "deploy/dry_run_remote_env_deploys_published_components", normalize.Apply(result.Combined))
}

func testDeployDryRunRemoteEnvTenantArtifactsRequirePublishedCharts(t *testing.T) {
	// team-powerdns is unpublished at 1.0.0, so the coherence check fails before any
	// spec is built.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envVars := append(setup.Env(),
		"ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=team-backend-api:1.0.0")
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "1.0.0",
		"--components", "team-backend-api,team-powerdns",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	golden.Equal(t, "deploy/dry_run_remote_env_tenant_artifacts_require_published_charts", normalize.Apply(result.Combined))
}

func testDeployDryRunStatedRuntimeChartIsExemptFromTheTenantChartMandate(t *testing.T) {
	// A stated runtime chart exempts the runtime from the tenant-line mandate;
	// components are still verified.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"runtimechart: oci://ghcr.io/sophium/charts/erun-devops:1.0.178\n")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=team-backend-api:1.0.0")
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "1.0.0",
		"--components", "team-backend-api,team-devops",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "oci://ghcr.io/sophium/charts/erun-devops --version 1.0.178") {
		t.Fatalf("stated runtime chart not installed: %s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_stated_runtime_chart_is_exempt_from_the_tenant_chart_mandate", normalize.Apply(result.Combined))
}

func testDeployDryRunEnvStatesItsRuntimeChartInConfig(t *testing.T) {
	// A configured chart skips the lookup, so version-only desktop deploys work.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+
		"runtimeimage: registry.example/acme/team-devops\n"+
		"runtimechart: oci://ghcr.io/sophium/charts/erun-devops:1.0.178\n")
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "9.9.9-snapshot-20260101010101",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Home, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "oci://ghcr.io/sophium/charts/erun-devops --version 1.0.178") {
		t.Fatalf("env's stated chart not installed at its own version: %s", result.Combined)
	}
	if !strings.Contains(result.Combined, "imageOverrides.erun-devops=registry.example/acme/team-devops:9.9.9-snapshot-20260101010101") {
		t.Fatalf("image not tagged on the deploy version: %s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_env_states_its_runtime_chart_in_config", normalize.Apply(result.Combined))
}

func testDeployDryRunRuntimeChartFlagBeatsTheEnvConfigChart(t *testing.T) {
	// The flag beats the config field and is not persisted.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"runtimechart: oci://ghcr.io/sophium/charts/erun-devops:1.0.178\n")
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "9.9.9-snapshot-20260101010101",
		"--runtime-chart", "oci://ghcr.io/sophium/charts/erun-devops:1.2.3",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Home, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "oci://ghcr.io/sophium/charts/erun-devops --version 1.2.3") {
		t.Fatalf("flag did not win over the config chart: %s", result.Combined)
	}
	after, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config after deploy: %v", err)
	}
	if !strings.Contains(string(after), "runtimechart: oci://ghcr.io/sophium/charts/erun-devops:1.0.178") {
		t.Fatalf("run-only flag changed the env's stated chart: %s", after)
	}
	golden.Equal(t, "deploy/dry_run_runtime_chart_flag_beats_the_env_config_chart", normalize.Apply(result.Combined))
}

func testDeployDryRunRemoteEnvRuntimeChartAndImageOnSeparateLines(t *testing.T) {
	// The chart and image are separate coordinates: the chart at its reference's
	// version, the image and release at --version.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"runtimeimage: registry.example/acme/team-devops\n")
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "9.9.9-snapshot-20260101010101",
		"--runtime-chart", "oci://ghcr.io/sophium/charts/erun-devops:1.0.0",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Home, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	// The goldens normalize versions, so assert both coordinates literally.
	if !strings.Contains(result.Combined, "oci://ghcr.io/sophium/charts/erun-devops --version 1.0.0") {
		t.Fatalf("chart not pulled at its own version: %s", result.Combined)
	}
	if !strings.Contains(result.Combined, "imageOverrides.erun-devops=registry.example/acme/team-devops:9.9.9-snapshot-20260101010101") {
		t.Fatalf("image not held on the tenant version line: %s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_remote_env_runtime_chart_and_image_on_separate_lines", normalize.Apply(result.Combined))
}

func testDeployDryRunRuntimeChartWithoutVersionKeepsTheDeployVersion(t *testing.T) {
	// A registry port in the reference must not be read as a version.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "1.0.0",
		"--runtime-chart", "registry.example:5000/charts/erun-devops",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Home, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "oci://registry.example:5000/charts/erun-devops --version 1.0.0") {
		t.Fatalf("port read as a version, or deploy version not kept: %s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_runtime_chart_without_version_keeps_the_deploy_version", normalize.Apply(result.Combined))
}

func testDeployDryRunRemoteEnvNoRepoPathDeploysPublishedChart(t *testing.T) {
	// A runtime env with no mounted source must not require a repo path.
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnvNoRepoPath(t, setup, "team", "dev")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--runtime-image", "ghcr.io/sophium/erun-devops", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_remote_env_no_repo_path_deploys_published_chart", normalize.Apply(result.Combined))
}

func testDeployDryRunRuntimeUmbrellaRescopesSetValues(t *testing.T) {
	// helm does not pass top-level values into a subchart, so every runtime value
	// is nested under erun-devops.*.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsUmbrellaChart(t, setup, "team", "dev", "team-devops", "erun-devops")
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "1.0.0",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "deploy/dry_run_runtime_umbrella_rescopes_set_values", normalize.Apply(result.Combined))
}

func testDeployComponentOnlyTreeBootstrapsPublishedRuntime(t *testing.T) {
	// With no runtime chart in the tree, the published erun-devops is installed and
	// the unselected component charts are not.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("expected exit 0 bootstrapping the published runtime, got %d:\n%s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/component_only_tree_bootstraps_published_runtime", normalize.Apply(result.Combined))
}

// seedDevopsChartRuntimeImage gives the chart an image to verify; the default seed has none.
func seedDevopsChartRuntimeImage(t *testing.T, setup env.Setup, tenant, imageRef string) {
	t.Helper()
	templates := filepath.Join(setup.Cwd, tenant+"-devops", "k8s", tenant+"-devops", "templates")
	if err := os.MkdirAll(templates, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", templates, err)
	}
	body := "apiVersion: apps/v1\nkind: Deployment\nspec:\n  template:\n    spec:\n      containers:\n        - name: runtime\n          image: " + imageRef + "\n"
	if err := os.WriteFile(filepath.Join(templates, "deployment.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write deployment.yaml: %v", err)
	}
}
