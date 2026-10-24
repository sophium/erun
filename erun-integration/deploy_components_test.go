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

func testDeployDryRunRemoteEnvDeploysTenantComponentChartsByReference(t *testing.T) {
	// A tenant publishes its own component charts (team-backend-api) beyond the
	// fixed erun-* platform set. On a sourceless remote env --components selects
	// them by their published name: each resolves to oci://<registry>/charts/
	// <chart> --version and installs under the release name <chart> — not
	// double-prefixed to team-team-backend-api. Selecting tenant components binds
	// the deploy to the tenant version line, so the coherence check verifies each
	// is published at the version; the ERUN_PUBLISHED_CHART_PROBE_OVERRIDE seam
	// stands in for that registry read (both charts published at 1.0.0 here).
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envVars := append(setup.Env(),
		"ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=team-backend-api:1.0.0,team-powerdns:1.0.0")
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "1.0.0",
		"--components", "team-backend-api,team-powerdns",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	golden.Equal(t, "deploy/dry_run_remote_env_deploys_tenant_component_charts_by_reference", normalize.Apply(result.Combined))
}

func testDeployDryRunRemoteEnvComponentThreadsResolvedOIDCIssuer(t *testing.T) {
	// An empty computed api.oidcAllowedIssuers must not be passed as
	// `--set-string api.oidcAllowedIssuers=`: helm's --set always beats -f,
	// so it would silently clobber whatever the operator configured under that key in the published chart's
	// values.<env>.yaml. Its sibling above
	// (dry_run_remote_env_deploys_published_components) locks the empty case —
	// no --set-string for the key at all, so -f wins. Here a cloud-provider
	// alias resolves a real issuer for this same sourceless component-deploy
	// path, and the flag must still appear, re-scoped under the wrapped
	// erun-backend-api subchart exactly as it did before the fix.
	setup := env.New(t)
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, "team")
	envDir := filepath.Join(tenantDir, "dev")
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	mustWriteFile(t, filepath.Join(root, "config.yaml"),
		"defaulttenant: team\n"+
			"cloudproviders:\n"+
			"  - alias: alpha\n"+
			"    provider: aws\n"+
			"    profile: alpha\n"+
			"    oidcissuerurl: https://oidc.example/shared\n")
	mustWriteFile(t, filepath.Join(tenantDir, "config.yaml"),
		"projectroot: "+setup.Cwd+"\nname: team\ndefaultenvironment: dev\n"+
			"cloudprovideraliases:\n  - alpha\n")
	mustWriteFile(t, filepath.Join(envDir, "config.yaml"),
		"name: dev\nrepopath: /nonexistent-remote/team\nkubernetescontext: test-context\n"+
			"containerregistry: registry.example/test\nruntimeversion: 1.0.0\ntype: remote-agent\n")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=team-backend-api:1.0.0")
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "1.0.0",
		"--components", "team-backend-api",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_remote_env_component_threads_resolved_oidc_issuer", normalize.Apply(result.Combined))
}

func testDeployDefaultSkipsOptinBackendCharts(t *testing.T) {
	// Opt-in-only resolution: when a tenant repo
	// contains the runtime chart and the three backend charts, `erun deploy`
	// with no selection deploys only the runtime chart. Nothing else rides
	// along — the backend charts ship only when explicitly selected via
	// --components, a saved deploy.components set, or the k8s.deployments plan.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "deploy/default_skips_optin_backend_charts", normalize.Apply(result.Combined))
}

func testDeployComponentsIncludesBackendInDeployOrder(t *testing.T) {
	// Opt-in-only: --components deploys exactly the named charts, so the
	// runtime does NOT deploy here (it was not selected). The three backend
	// charts must still deploy in the fixed dependency order (postgres -> db
	// -> api), regardless of the order they appear on the command line.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "1.0.0",
		"--components", "erun-backend-api,erun-backend-db,erun-backend-postgres",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "deploy/components_includes_backend_in_deploy_order", normalize.Apply(result.Combined))
}

func testDeployDryRunUmbrellaComponentBuildsHelmDependencies(t *testing.T) {
	// A local umbrella chart (team-backend-api) declares an OCI
	// dependency on the published erun-backend-api chart. deploy must
	// `helm dependency build` it before helm upgrade --install, or helm
	// fails on the subchart missing from charts/. The dry-run plan traces
	// the build immediately before the upgrade for that chart. Leaf charts
	// with no dependencies get no such line (see
	// components_includes_backend_in_deploy_order, whose backend leaf charts
	// trace no dependency build), and the published-OCI runtime is skipped
	// outright (no local charts/).
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsUmbrellaChart(t, setup, "team", "dev", "team-backend-api", "erun-backend-api")
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "1.0.0",
		"--components", "team-backend-api",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "deploy/dry_run_umbrella_component_builds_helm_dependencies", normalize.Apply(result.Combined))
}

func testDeployProjectK8sPlanIncludesListedChartsWithoutComponentsFlag(t *testing.T) {
	// The k8s.deployments plan is a selection tier: with no
	// --components and no saved deploy.components, the plan's charts are the
	// selection, so a user who configured the plan need not pass
	// --components on every deploy. Here the plan names the runtime
	// (team-devops) and the three backends, so all four deploy — grouped and
	// ordered by the plan (team-devops+postgres parallel, then db, then api).
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
	fixture.SeedProjectK8sConfig(t, setup, "environments:\n  dev:\n    k8s:\n      deployments:\n        - [team-devops, erun-backend-postgres]\n        - erun-backend-db\n        - erun-backend-api\n")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "deploy/project_k8s_plan_includes_listed_charts_without_components_flag", normalize.Apply(result.Combined))
}

func testDeployProjectK8sPlanRejectsInvalidStepNode(t *testing.T) {
	// A k8s.deployments step must be either a component name or a
	// list of component names. Anything else (a mapping, a number, …)
	// must surface as a clear error from the project config loader,
	// not silently parse to an empty step.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
	fixture.SeedProjectK8sConfig(t, setup, "environments:\n  dev:\n    k8s:\n      deployments:\n        - {name: erun-devops}\n")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for malformed k8s.deployments step, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/project_k8s_plan_rejects_invalid_step_node", normalize.Apply(result.Combined))
}

func testDeployRejectsInconsistentPlatformConfig(t *testing.T) {
	// A `platform:` block whose serviceszone is not under the configured
	// basedomain is rejected when deploy resolves the project plan, before
	// any chart work — the per-instance platform config must be internally
	// consistent (nothing-hardcoded: the services zone belongs under this
	// deployment's own base domain). Reaches PlatformConfig.Validate via
	// loadProjectK8sPlanForRepo.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  serviceszone: services.kppaas.com\n")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for inconsistent platform config, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/rejects_inconsistent_platform_config", normalize.Apply(result.Combined))
}

func testDeployPlatformConfigThreadsIntoHelmSet(t *testing.T) {
	// A valid `platform:` block flows into every chart's helm command as
	// guarded platform.* --set args, with Resolve's defaults filled in
	// (serviceszone/authhost/nameservers derived from basedomain). Only the
	// platform singletons read them; the runtime chart ignores them. Proves
	// the deploy -> platform-config -> helm wiring end to end, including the
	// resolved auth host reaching the API's trusted-issuer list as
	// api.oidcAllowedIssuers=https://auth.<basedomain> — a platform's control
	// plane trusts its own hosted IdP without an operator patching anything,
	// and the front-door docs link defaulting to docs.<basedomain> so a
	// platform points at its own documentation with nothing configured.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 212.93.120.230\n")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "deploy/platform_config_threads_into_helm_set", normalize.Apply(result.Combined))
}

func testDeployPlatformConfigThreadsWhiteLabelValuesIntoHelmSet(t *testing.T) {
	// An operator's own docs URL, tagline, and logo travel the same
	// platform.* path consoleurl and brand already do, so the instance's
	// pre-sign-in front door reflects its own brand instead of the bundled
	// product defaults. A configured docsurl wins over the
	// docs.<basedomain> default, and a trailing slash is normalized away so
	// the client's own deep links under it do not double up.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  brand: Acme\n  docsurl: https://handbook.acme.example/erun/\n  tagline: Ship it, prove it.\n  logourl: https://cdn.acme.example/logo.svg\n")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "deploy/platform_config_threads_white_label_values_into_helm_set", normalize.Apply(result.Combined))
}

func testDeployRejectsMalformedPlatformLogoURL(t *testing.T) {
	// A logourl that is not an absolute URL would reach a browser as a dead
	// image on the instance's front door, with nothing on the page able to
	// explain it — so it is refused at plan time, and the refusal names the
	// shape expected plus an example under this deployment's own domain
	// rather than only saying "invalid".
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  logourl: cdn.acme.example/logo.svg\n")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a malformed platform logourl, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/rejects_malformed_platform_logo_url", normalize.Apply(result.Combined))
}

func testDeployComponentsRejectsUnknownName(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "1.0.0",
		"--components", "bogus",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for unknown component, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/components_rejects_unknown_name", normalize.Apply(result.Combined))
}

func testDeployDefaultDeploysRuntimeOnlyNotStrayNonOptinChart(t *testing.T) {
	// Opt-in-only: a non-opt-in, non-runtime chart (here team-docs)
	// present in the tree must NOT deploy by default — only the runtime does.
	// A chart outside the opt-in set must not deploy by elimination (e.g. a
	// disabled docs chart shipped on every deploy).
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsComponentChart(t, setup, "team", "dev", "team-docs")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "deploy/default_deploys_runtime_only_not_stray_non_optin_chart", normalize.Apply(result.Combined))
}

func testDeploySavedDeployComponentsDriveSelectionWithoutFlag(t *testing.T) {
	// The per-machine saved set (EnvConfig.deploy.components) is the selection
	// tier below --components: with no --components, deploy resolves to
	// exactly the saved charts. Here the saved set is the postgres backend
	// only, so deploy rolls out postgres alone — the runtime is NOT added (a
	// non-empty saved selection that does not name it).
	setup := env.New(t)
	fixture.SeedTenantEnvWithDeployComponents(t, setup, "team", "dev", []string{"erun-backend-postgres"})
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "deploy/saved_deploy_components_drive_selection_without_flag", normalize.Apply(result.Combined))
}

func testDeploySavedDeployComponentsShadowingPlanRefusesToDeploy(t *testing.T) {
	// When a saved deploy.components set wins over a repo k8s.deployments
	// plan that names more, a plain deploy must not silently roll out the
	// stale subset forever (a component added to the plan after the saved
	// selection was written would otherwise sit a release behind unnoticed).
	// It refuses instead,
	// naming exactly what the plan asked for beyond the saved set and both
	// ways out — adopt it via `erun init --components`, or return to the
	// plan by clearing the saved selection.
	setup := env.New(t)
	fixture.SeedTenantEnvWithDeployComponents(t, setup, "team", "dev", []string{"erun-backend-postgres"})
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
	fixture.SeedProjectK8sConfig(t, setup, "environments:\n  dev:\n    k8s:\n      deployments:\n        - [team-devops, erun-backend-postgres]\n        - erun-backend-db\n        - erun-backend-api\n")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a saved selection shadowing the repo plan, got 0:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "helm upgrade") {
		t.Fatalf("expected the refusal to stop before any helm invocation, got:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/saved_deploy_components_shadowing_plan_refuses_to_deploy", normalize.Apply(result.Combined))
}

func testDeployExplicitComponentsStillNarrowsAndDoesNotResurrectShadowedPlanExtras(t *testing.T) {
	// --components is its own, higher-precedence selection tier, resolved
	// before a saved selection or the repo plan ever applies. This is the
	// same shadowed setup as saved_deploy_components_shadowing_plan_refuses_to_deploy
	// (saved omits what the plan names), but this run passes --components
	// explicitly, naming exactly the saved subset. Two properties must both
	// hold: the flag bypasses the saved-vs-plan guard entirely (an operator
	// naming a subset explicitly is narrowing on purpose for that one run,
	// and that must keep working exactly as before), and the deploy must
	// install only what was named — the fix must never react to a shadowed
	// saved selection by widening what actually gets installed, which would
	// resurrect a component an operator could have deliberately narrowed
	// away.
	setup := env.New(t)
	fixture.SeedTenantEnvWithDeployComponents(t, setup, "team", "dev", []string{"erun-backend-postgres"})
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
	fixture.SeedProjectK8sConfig(t, setup, "environments:\n  dev:\n    k8s:\n      deployments:\n        - [team-devops, erun-backend-postgres]\n        - erun-backend-db\n        - erun-backend-api\n")
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "1.0.0",
		"--components", "erun-backend-postgres",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("expected exit 0 for an explicit --components run bypassing the saved-selection guard, got %d:\n%s", result.ExitCode, result.Combined)
	}
	if got := strings.Count(result.Combined, "helm upgrade --install"); got != 1 {
		t.Fatalf("expected exactly one helm invocation (the explicitly named component only), got %d:\n%s", got, result.Combined)
	}
	if strings.Contains(result.Combined, "erun-backend-db") || strings.Contains(result.Combined, "erun-backend-api") {
		t.Fatalf("expected the plan-only components to never appear in the deploy, got:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/explicit_components_still_narrows_and_does_not_resurrect_shadowed_plan_extras", normalize.Apply(result.Combined))
}
