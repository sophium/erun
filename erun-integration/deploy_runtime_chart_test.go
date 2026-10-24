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
	// Real-run deploy of a remote env with no local checkout: the
	// runtime spec resolves to the published erun-devops OCI chart and
	// helm upgrade references it directly (no local chart copy, no
	// Chart.yaml stamping — the chart is pinned with --version). The
	// helm/kubectl stubs exit 0 so the rollout completes; real-run is
	// what proves DeployHelmChart skips prepareHelmChartForDeploy for
	// an OCI reference. The ERUN_PUBLISHED_CHART_PROBE_OVERRIDE seam marks
	// erun-devops published so the runtime chart ladder confirms it instead
	// of refusing (deploy never installs an unconfirmed coordinate).
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
	// Real-run deploy of a tenant's own published umbrella (team-backend-api,
	// which wraps erun-backend-api as a subchart). Because top-level --sets do
	// not reach a wrapped subchart, deploy re-scopes them under erun-backend-api
	// AND pulls the published chart to -f its bundled values.<env>.yaml. Only a
	// real run exercises runPublishedValuesPull (the helm pull + temp-dir
	// prep); the helm/kubectl stubs exit 0 so the pull and rollout complete. The
	// ERUN_PUBLISHED_CHART_PROBE_OVERRIDE seam marks the chart published so the
	// coherence check passes without a live registry.
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
	// Deploy is a consume operation. Even when the chart references a
	// runtime image that HAS a local build context (docker/team-devops/
	// Dockerfile makes team-devops genuinely buildable), an explicit
	// --version installs that already-published version by reference: the
	// trace shows the decision line and a `docker manifest inspect` of the
	// pinned image, then the helm upgrade — and crucially NO docker build,
	// no `docker image inspect` fingerprint probe, and no promote/rebuild
	// line. Without the fix this chart resolves a build for team-devops and
	// rebuilds it on a fingerprint-cache miss, relabelling the working tree
	// as the pinned version (and pushing over the published tag).
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
	// Deploy installs an existing version and does not build it, so a
	// version whose image is absent both locally and in the registry must
	// fail fast rather than silently rebuild from the working tree. The
	// existence check runs only in real mode (dry-run traces and skips it),
	// so this is a real-run scenario: the docker stub exits non-zero for
	// `manifest inspect` and `image inspect`, so both lookups report
	// "absent" and resolution errors before helm/kubectl are touched.
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
	// Regression #1193: a remote env (Remote=true) has its repopath on the
	// remote host's filesystem (e.g. proxmox1: /home/erun/git/erun) and has
	// no local checkout at all, and the probe seam confirms no candidate --
	// neither the tenant's own umbrella nor the shared erun-devops chart, in
	// either registry the ladder searches. Deploy must refuse rather than
	// substitute the shared chart at the tenant's version: erun-devops is
	// versioned on erun's own release line, so that coordinate can never
	// exist. Before the fix this installed exactly that impossible
	// coordinate (decision trace + a helm upgrade pinned to it); now it
	// stops before any helm command is built, naming every coordinate it
	// asked and that none was confirmed.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	// Note: no SeedDevopsRepo — there is no local checkout anywhere. The
	// harness default confirms the shared erun-devops chart at any version
	// (the realistic baseline); this scenario is precisely the case where
	// that is NOT true, so it resets the seam to empty (nothing published
	// anywhere).
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
	// Regression #1193, the exact production shape: the tenant's own
	// umbrella (team-devops) is never published, and the shared erun-devops
	// chart genuinely exists -- just on erun's own release line (1.0.201),
	// not the tenant's requested version (1.0.72). A ladder that only checks
	// "does a chart named erun-devops exist anywhere" would find it and
	// install it tagged 1.0.72, a chart:version pair that was never
	// published and never can be. Deploy must refuse instead of assembling
	// that impossible coordinate.
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
	// Self-contained tenant: when the tenant publishes its own runtime
	// chart (charts/team-devops) at the deploy version, published runtime
	// resolution prefers it over the shared charts/erun-devops — the
	// published analogue of the local <tenant>-devops-first order. The
	// probe's answer is supplied deterministically via the
	// ERUN_PUBLISHED_CHART_PROBE_OVERRIDE decision-input seam (real deploys
	// read the registry); the sibling
	// dry_run_remote_env_refuses_when_no_runtime_chart_is_confirmed locks the
	// refusal when the tenant chart is absent and nothing else is confirmed.
	// The seam also publishes the platform chart in erun's own registry, which
	// the search would otherwise reach: the tenant's umbrella wins over it, so
	// widening the search never diverts a self-contained tenant onto the
	// vanilla runtime.
	//
	// With no runtimeimage set, preferring the umbrella also defaults the
	// runtime image to the umbrella's own image: erun push publishes the
	// team-devops image and chart together on the tenant version line, so
	// imageOverrides.erun-devops resolves to <registry>/team-devops:<version>.
	// Building the custom image is then sufficient — the deploy no longer
	// silently falls back to the stock erun-devops:<tenant-version>, a tag the
	// tenant line never publishes (the ImagePullBackOff this fixes). The trace
	// names the default and the helm command carries the re-scoped --set-string.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=registry.example/test/team-devops:1.0.0,ghcr.io/sophium/erun-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	golden.Equal(t, "deploy/dry_run_remote_env_prefers_tenant_published_runtime_chart", normalize.Apply(result.Combined))
}

func testDeployDryRunMovesTheTenantUmbrellaPinWithTheDeployVersion(t *testing.T) {
	// The reported failure, at the surface it was reported from: a deploy of
	// frs/prod at 1.0.143 resolved the tenant's own frs-devops
	// umbrella at its stated 1.0.142 while the same command set the runtime
	// image to frs-devops:1.0.143 -- and reported success. The umbrella is
	// where the wrapped erun version lives, so that deploy would have run a
	// 1.0.143 image wrapped around an older erun. The umbrella is on the
	// tenant's own line, the one --version names, so it moves with it.
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
	// The golden normalizes every version to <VERSION>, so it cannot carry
	// the pair that was the reported failure -- the umbrella's --version and
	// the image override's tag. Assert them verbatim.
	if !strings.Contains(result.Combined, "charts/frs-devops --version 1.0.143") {
		t.Errorf("the umbrella is not installed at the deploy version:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "imageOverrides.erun-devops=registry.example/test/frs-devops:1.0.143") {
		t.Errorf("the image override does not name the deploy version:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_moves_the_tenant_umbrella_pin_with_the_deploy_version", normalize.Apply(result.Combined))
}

func testDeployDryRunSaysWhenItHoldsTheTenantUmbrellaBack(t *testing.T) {
	// The other half of the same report: when the deploy version is not published for
	// the umbrella, the stated version stays -- an unconfirmed coordinate is
	// never substituted -- but the deploy must not stay silent about it. The
	// reported run printed a version, waited for a rollout and succeeded, so
	// nothing distinguished a moved umbrella from a stranded one.
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
	// The umbrella line stops at the stated version.
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=frs-devops:1.0.142")
	result := erun.Run(t, []string{"deploy", "frs", "prod", "--version", "1.0.143", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	// Same normalization caveat as the scenario above: the goldens pin the
	// prose, these pin the versions it is about.
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
	// A remote/runtime env has no local checkout, yet --components selects
	// platform components: each resolves to its PUBLISHED erun-<component>
	// chart by reference (oci://<registry>/charts/erun-<component>) and
	// installs directly with top-level --set tenant/environment — no local
	// umbrella, no source. Components are emitted in default-rank order
	// (postgres → db → api), not the scrambled --components input order, and
	// the runtime (team-devops selected) resolves to the tenant's own published
	// team-devops chart — the mandate keeps it on the tenant version line rather
	// than the erun-devops fallback. This is the sourceless deploy path.
	//
	// Selecting tenant components binds the deploy to the tenant version line, so
	// the coherence check verifies the runtime and every component chart is
	// published at the version. The ERUN_PUBLISHED_CHART_PROBE_OVERRIDE seam
	// stands in for the registry read that check performs (real deploys read the
	// registry); here every selected chart is published at 1.0.0.
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
	// The tenant-chart mandate: deploying the tenant's own component charts binds
	// the deploy to the tenant's version line, so the tenant runtime chart and
	// every selected component chart must be published at the version. Here
	// team-powerdns is not published at 1.0.0 (the probe override lists only
	// team-backend-api), so the coherence check fails fast — before any spec is
	// built — instead of the runtime silently falling back to erun-devops or a
	// half-applied rollout dying on a mid-deploy chart pull. The override seam
	// stands in for the registry read (real deploys read the registry).
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
	// The mandate binds a tenant-component deploy to the tenant's version line,
	// and the tenant runtime chart is normally part of that: it must exist at the
	// version. An env that states its runtime chart is not riding a tenant chart
	// at all, so requiring one would fail a deploy that is entirely coherent --
	// the component runs on the tenant's line, the runtime on the line the env
	// named. The probe override publishes only team-backend-api at 1.0.0 (no
	// team-devops), which without the exemption is exactly the fast failure the
	// sibling scenario above pins. The components are still verified.
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
	// The durable half of the coordinate. An env whose image rides the project's
	// own release line states its chart once, in config, so every later deploy --
	// including one driven from the desktop, which passes only a version --
	// installs the chart that exists instead of probing for one at a version the
	// chart's line never published. The lookup is skipped entirely: the env was
	// taken at its word, and the trace says so.
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
	// Precedence, the same way --runtime-image beats runtimeimage: the config
	// field is the env's standing statement, the flag is this run's. An operator
	// trying a different chart must not have to edit config to do it, and must
	// not silently leave the env changed afterwards -- the flag is not persisted.
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
	// The chart and the runtime image are separate artifacts in separate
	// registries. A tenant that versions its image on its own release line has
	// no chart at that version, so --runtime-chart states the chart as its own
	// coordinate: the trace names the override, the helm command pulls the chart
	// at the version the reference carries, and --version still stamps the
	// release and resolves the image.
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
	// The goldens normalize every version to <VERSION>, which is exactly what
	// this scenario is about, so the two coordinates are asserted literally:
	// the chart at its own version, the image at the deploy version.
	if !strings.Contains(result.Combined, "oci://ghcr.io/sophium/charts/erun-devops --version 1.0.0") {
		t.Fatalf("chart not pulled at its own version: %s", result.Combined)
	}
	if !strings.Contains(result.Combined, "imageOverrides.erun-devops=registry.example/acme/team-devops:9.9.9-snapshot-20260101010101") {
		t.Fatalf("image not held on the tenant version line: %s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_remote_env_runtime_chart_and_image_on_separate_lines", normalize.Apply(result.Combined))
}

func testDeployDryRunRuntimeChartWithoutVersionKeepsTheDeployVersion(t *testing.T) {
	// A reference with no version names only the chart's repository, so the
	// chart still resolves at the deploy version; a registry port in the
	// reference must not be read as one.
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
	// A control-plane-provisioned tenant with no project has no repopath at
	// all (fixture.SeedRuntimeTenantEnvNoRepoPath mirrors the backend's
	// bootstrapEnvironmentScript seed exactly). `--runtime-image` must
	// still install the canonical published erun-devops chart by reference:
	// resolveOpenRepoPath must not require a repo path for a runtime env with
	// no mounted source worktree, or this fails with "repo path is not
	// configured" before ever reaching the override.
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
	// The repo-local runtime chart (team-devops) is an umbrella that wraps
	// the published erun-devops chart as a subchart. helm does not pass
	// top-level --set values into subchart scope, so deploy nests every
	// runtime value under the erun-devops.* subchart key (--set, --set-string,
	// and --set-json alike) and helm-dependency-builds the umbrella before the
	// upgrade. Any imageOverrides.erun-devops a custom build env contributes
	// rides the same --set-string path and is nested identically. A plain
	// (non-umbrella) runtime chart keeps top-level --sets; see
	// dry_run_from_devops_cwd.
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
	// A component-only tenant tree (component charts under <tenant>-devops/k8s
	// but no <tenant>-devops runtime chart — the frs platform shape). With no
	// selection, deploy defaults to the runtime and, finding no local runtime
	// chart, installs the published erun-devops chart by reference (once the
	// ladder confirms it published at the version — the
	// ERUN_PUBLISHED_CHART_PROBE_OVERRIDE seam stands in for that registry
	// read); the present component charts are NOT deployed (not selected).
	// This is the dual-lookup + published fallback now available to deploy
	// as it is to open.
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

// seedDevopsChartRuntimeImage gives the seeded <tenant>-devops chart a concrete
// chart-referenced image so the install-by-reference path has something to
// verify; the default SeedDevopsRepo chart has no templates and no such image.
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
