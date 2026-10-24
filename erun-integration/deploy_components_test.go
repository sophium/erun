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
	// Tenant charts install under their own name, not team-team-*; the probe
	// override stands in for the registry's published-version check.
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
	// helm --set beats -f, so the issuer flag is passed only when non-empty.
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
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "deploy/default_skips_optin_backend_charts", normalize.Apply(result.Combined))
}

func testDeployComponentsIncludesBackendInDeployOrder(t *testing.T) {
	// Backends deploy in dependency order regardless of command-line order.
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
	// An umbrella with an OCI dependency needs `helm dependency build` first;
	// leaf charts and the published runtime get no build line.
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
	// With no --components or saved set, the k8s.deployments plan is the selection.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
	fixture.SeedProjectK8sConfig(t, setup, "environments:\n  dev:\n    k8s:\n      deployments:\n        - [team-devops, erun-backend-postgres]\n        - erun-backend-db\n        - erun-backend-api\n")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "deploy/project_k8s_plan_includes_listed_charts_without_components_flag", normalize.Apply(result.Combined))
}

func testDeployProjectK8sPlanRejectsInvalidStepNode(t *testing.T) {
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
	// The services zone must sit under the configured basedomain.
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
	// Resolve's defaults (serviceszone, authhost, docs.<basedomain>) reach helm,
	// including the auth host in api.oidcAllowedIssuers.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 212.93.120.230\n")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "deploy/platform_config_threads_into_helm_set", normalize.Apply(result.Combined))
}

func testDeployPlatformConfigThreadsWhiteLabelValuesIntoHelmSet(t *testing.T) {
	// A trailing slash on docsurl is normalized so client deep links don't double up.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  brand: Acme\n  docsurl: https://handbook.acme.example/erun/\n  tagline: Ship it, prove it.\n  logourl: https://cdn.acme.example/logo.svg\n")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "deploy/platform_config_threads_white_label_values_into_helm_set", normalize.Apply(result.Combined))
}

func testDeployRejectsMalformedPlatformLogoURL(t *testing.T) {
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
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsComponentChart(t, setup, "team", "dev", "team-docs")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "deploy/default_deploys_runtime_only_not_stray_non_optin_chart", normalize.Apply(result.Combined))
}

func testDeploySavedDeployComponentsDriveSelectionWithoutFlag(t *testing.T) {
	// A non-empty saved set that omits the runtime does not add it.
	setup := env.New(t)
	fixture.SeedTenantEnvWithDeployComponents(t, setup, "team", "dev", []string{"erun-backend-postgres"})
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "deploy/saved_deploy_components_drive_selection_without_flag", normalize.Apply(result.Combined))
}

func testDeploySavedDeployComponentsShadowingPlanRefusesToDeploy(t *testing.T) {
	// A saved set shadowing a larger plan refuses rather than silently deploying
	// a stale subset.
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
	// --components bypasses the saved-vs-plan guard and installs only what it names.
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
