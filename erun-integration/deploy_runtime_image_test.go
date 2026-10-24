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
	// Precedence: an explicit runtimeimage overrides the umbrella's own-image
	// default. A tenant that publishes team-devops but points the env at a
	// specific image (e.g. a hotfix build) must get that image as
	// imageOverrides.erun-devops, NOT the umbrella default — the operator's
	// choice always wins. The trace names the override (not the default) and
	// the helm command re-scopes it under the erun-devops subchart key.
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
	// Migration hardening: when a tenant rode the shared erun-devops chart and
	// later moved to its own team-devops umbrella, a leftover
	// runtimeimage pointing at the stock erun-devops image would (as an explicit
	// override) win over the umbrella default and pin erun-devops:<tenant-version>
	// — a tag the tenant line never publishes (ImagePullBackOff). resolveDeployRuntimeImage
	// detects the stock name on an umbrella deploy, traces that it is ignoring the
	// stale pin, and defaults to the umbrella's own image instead.
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
	// A pin naming the tenant's OWN team-devops image at a tag that disagrees
	// with the deploy version must not be replaced with a guessed
	// team-devops:<erun-version> tag, which would conflate two independent
	// version lines: the tenant's own
	// image is versioned on the tenant's own release line (exactly what
	// `erun pin` already documents and enforces — it never rewrites this
	// tag), so a tag that disagrees with the deploy version is the expected,
	// correct case, not staleness. The recorded runtimeimage must be
	// honoured verbatim, and the deploy must roll out that tag rather than
	// one nothing ever published.
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
	// The shared-chart half of the stale-pin migration. An env that states its
	// runtime chart at the chart's own version is deploying a version from another
	// line, so a leftover runtimeimage naming the stock erun-devops would pin
	// erun-devops:<tenant-version> — a tag erun's line never publishes
	// (ImagePullBackOff). The stated chart is the signal: the pin is ignored and the
	// version names the tenant's own image. That image resolves against the registry
	// the cluster pulls from, not the chart's — an env states its chart precisely to
	// keep the two apart.
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
	// An env recorded at an old runtimechart version, deployed with
	// --version/--runtime-image/--runtime-chart together moving it forward.
	// Image inference must read the --runtime-chart override, not the env's
	// stale recorded chart version, or it substitutes a tenant image tag that
	// was never built (ImagePullBackOff). The operator's own image
	// and chart must both install exactly as stated.
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
	// The ordering fix in isolation, with no --runtime-image flag this run: a
	// persisted runtimeimage memo (not this invocation's own choice) still goes
	// through the staleness heuristic, which must key off the operator's
	// --runtime-chart rather than the env's stale recorded chart version. Before
	// the fix, this healed pin was wrongly evicted the same way an explicit
	// override was.
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
	// The tenant's runtime image rides its own project's version line
	// (VERSION symlinked to the project's release, not erun's), so an
	// explicit --runtime-image shares a repository with the deploy's inferred
	// default image but differs only by tag. staleRuntimeImageTrace must not
	// treat that as a leftover pin and substitute the inferred tag.
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
	// A persisted EnvConfig.RuntimeImage must ride into the published
	// chart deploy as imageOverrides.erun-devops: the trace
	// names the override decision and the helm command carries the
	// --set-string. A full reference is used verbatim.
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
	// A registry-qualified runtime image with NO tag (the
	// `--runtime-image ghcr.io/sophium/erun-devops` shape) must be
	// pinned to the env's runtime version, not passed through bare:
	// a tagless override makes Kubernetes default the pull to :latest,
	// which the release flow never publishes (ImagePullBackOff). The
	// override in the helm command must carry :<version>, not :latest.
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
	// `--runtime-image` installs the canonical published erun-devops
	// chart with the chosen image as imageOverrides.erun-devops, pinned to
	// --version, EVEN when the env carries a repo-local runtime chart
	// (SeedDevopsRepo materializes team-devops/k8s/team-devops). The trace
	// names the override decision and reroutes to the published chart
	// instead of installing the local team-devops chart by reference, so an
	// operator can bootstrap an env on the ERun base image before its own
	// <tenant>-devops image is built. The tagless override is pinned to the
	// deploy version (not :latest).
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
	// `--runtime-image` on a local env whose <tenant>-devops module has
	// no k8s chart tree (os.ReadDir(.../k8s) → fs.ErrNotExist) must bootstrap
	// on the published erun-devops chart by reference instead of failing spec
	// resolution ("open .../k8s: no such file or directory"). The desktop's
	// ERun-base picker deploys a bare tenant this way before its own
	// <tenant>-devops chart exists. Contrast dry_run_no_devops_module_bootstraps_published_runtime
	// (no override → bootstraps published runtime) and dry_run_runtime_image_override_uses_published_chart
	// (chart present → reroutes). The module exists (docker + VERSION) but has
	// no k8s tree, mirroring validation-agent-devops.
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
	// The env's persisted runtimeimage still names the stock erun-devops
	// image, but this deploy confirms the tenant's own frs-devops chart (the
	// staleness check already resolves the CORRECT image for THIS deploy) --
	// the persisted runtimeimage field must heal to match too, or a later deploy that does not repeat this
	// exact chart confirmation would read the same stale stock pin back.
	// Real-run: persistence only happens outside --dry-run. runtimeimage
	// must heal to the bare tenant name (self-maintaining, no registry, no
	// tag) in the same write as runtimeversion -- one coordinate, one
	// commit.
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
	// The severe case: this env's last confirmed deploy actually
	// ran the tenant's own frs-devops image (runtimerunningimage), but the
	// persisted runtimeimage still names stock erun-devops and this
	// deploy's own chart confirmation does not this time land on the
	// tenant's own chart (so the existing staleness check, which is keyed
	// off the chart, does not fire) -- the wrong tag resolves fine
	// (erun-devops:1.0.86 is a real erun release), so only comparing
	// release lines against the last confirmed deploy catches it. No
	// kubectl/helm stub is declared: if the refusal did not fire before
	// any cluster interaction, the command would instead fail with
	// "executable file not found", which would show up in the golden.
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
	// The legitimate case that must keep working with no new friction: an
	// env that deliberately rides the stock erun-devops image on erun's own
	// release line, redeployed
	// at a newer erun version. Both the recorded runtimeimage and the last
	// confirmed runtimerunningimage name the same (stock) release line, so
	// neither the persisted-image healing nor the new cross-line guard
	// changes anything observable here.
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
	// root AGENTS.md: an unclassifiable pairing must never silently pass as
	// fine, but must also never block a configuration this guard merely
	// could not classify. runtimerunningimage here parses to no component
	// name at all (an ends-in-"/" reference), so the guard cannot compare
	// release lines -- it must trace that explicitly and let the deploy
	// proceed rather than refuse or stay silent.
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
	// The reported mistake: frs is a separate product on its own version line --
	// environments run ghcr.io/sophium/frs-devops -- but `erun deploy frs
	// build --version 1.0.304` was accepted, because the runtime chart
	// ladder falls through to the shared erun-devops chart when the
	// tenant's own umbrella is not published at that version. The chart is
	// then an erun coordinate while the image the same deploy derives
	// (frs-devops:1.0.304, a tag frs's line never published) is a frs one:
	// the pod sat in Init:ImagePullBackOff and the environment was down ~11
	// minutes before helm's "Progress deadline exceeded" -- a rollout
	// timeout naming anything but the cause. This env's last confirmed
	// deploy ran the frs line, so the mismatch is classifiable and the
	// deploy must refuse before any cluster interaction, naming both lines
	// and the version. No kubectl/helm stub is declared: a refusal that did
	// not fire before the rollout would show up in the golden as the
	// command it wrongly planned.
	//
	// Only a chart the search produced is checked. A tenant that states the
	// stock erun-devops chart in its own config while running its own image
	// line is naming erun's line on purpose, and that coordinate keeps
	// deploying as stated -- TestStockRuntimePinMoveLeavesADeliberateCoordinateAlone
	// (erun-common) is that half, and is what this guard has to stay clear
	// of.
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
