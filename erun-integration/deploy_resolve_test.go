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

func testDeployHelpOutsideDevopsCwd(t *testing.T) {
	// deploy must stay registered without a devops context so the desktop's --version lands.
	setup := env.New(t)
	result := erun.Run(t, []string{"deploy", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/help_outside_devops_cwd", normalize.Apply(result.Combined))
}

func testDeployVersionFlagRecognizedOutsideDevopsCwd(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"deploy", "missing", "missing", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "deploy/version_flag_recognized_outside_devops_cwd", normalize.Apply(result.Combined))
}

func testDeployDryRunFromDevopsCwd(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "deploy/dry_run_from_devops_cwd", normalize.Apply(result.Combined))
}

func testDeployDryRunRetrofitsThePlatformAliasOnAPreExistingEnv(t *testing.T) {
	// A signed-in host deploy applies the platform-alias Secret, threads its name
	// into helm, and records it so later deploys keep it.
	setup := env.New(t)
	fixture.SeedTenantEnvWithSignedInERunPlatformAlias(t, setup, "team", "dev", "erun+test@erun")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_retrofits_the_platform_alias_on_a_pre_existing_env", normalize.Apply(result.Combined))
}

func testDeployRefusesHostEnvironment(t *testing.T) {
	setup := env.New(t)
	fixture.SeedHostTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "deploy/refuses_host_environment", normalize.Apply(result.Combined))
}

func testDeployDryRunFromDevopsModuleRoot(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	moduleRoot := filepath.Join(setup.Cwd, "team-devops")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: moduleRoot, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_from_devops_module_root", normalize.Apply(result.Combined))
}

func testDeployDryRunFromNestedDevopsSubdir(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	nested := filepath.Join(setup.Cwd, "team-devops", "notes")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", nested, err)
	}
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: nested, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_from_nested_devops_subdir", normalize.Apply(result.Combined))
}

func testDeployDryRunFromK8sDir(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	k8sDir := filepath.Join(setup.Cwd, "team-devops", "k8s")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: k8sDir, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_from_k8s_dir", normalize.Apply(result.Combined))
}

func testDeployDryRunConfiguredK8sPath(t *testing.T) {
	// No <tenant>-devops module exists, so without the override deploy would use the published chart.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectPathsConfig(t, setup, "", "", "deploy/k8s", "", "")
	fixture.SeedK8sChartAt(t, filepath.Join(setup.Cwd, "deploy", "k8s"), "team-devops", "team", "dev")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_configured_k8s_path", normalize.Apply(result.Combined))
}

func testDeployDryRunGitignoredProjectConfigWarns(t *testing.T) {
	// A gitignored project config works on this machine only, so deploy warns.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedGitignoredProjectConfig(t, setup, "{}\n")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_gitignored_project_config_warns", normalize.Apply(result.Combined))
}

func testDeployConfiguredK8sPathWrongNameErrors(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectPathsConfig(t, setup, "", "", "charts", "", "")
	fixture.SeedK8sChartAt(t, filepath.Join(setup.Cwd, "charts"), "team-devops", "team", "dev")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a misnamed configured k8s path, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/configured_k8s_path_wrong_name_errors", normalize.Apply(result.Combined))
}

func testDeployDryRunFromGitProjectRootDetectsTenantDevops(t *testing.T) {
	// The repo dir is named "team" so findProjectRoot's detected tenant matches.
	setup := env.New(t)
	repo := filepath.Join(setup.Cwd, "team")
	fixture.SeedGitRepo(t, repo)
	fixture.SeedDevopsRepoAt(t, repo, "team", "dev")
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
		[]byte("projectroot: "+repo+"\nname: team\ndefaultenvironment: dev\n"), 0o644); err != nil {
		t.Fatalf("tenant cfg: %v", err)
	}
	envBody := "name: dev\nrepopath: " + repo + "\nkubernetescontext: test-context\ncontainerregistry: registry.example/test\nruntimeversion: 1.0.0\n"
	if err := os.WriteFile(filepath.Join(envDir, "config.yaml"), []byte(envBody), 0o644); err != nil {
		t.Fatalf("env cfg: %v", err)
	}
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: repo, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_from_git_project_root_detects_tenant_devops", normalize.Apply(result.Combined))
}

func testDeployDryRunMultipleDevopsModulesErrors(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsRepoAt(t, setup.Cwd, "other", "dev")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for ambiguous devops modules, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_multiple_devops_modules_errors", normalize.Apply(result.Combined))
}

func testDeployDryRunScopeDefaultedEarlyFailureNamesResolvedEnv(t *testing.T) {
	// Tenant/environment resolve before any early failure so the header never
	// renders as "==> Deploy failed /:".
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsRepoAt(t, setup.Cwd, "other", "dev")
	result := erun.Run(t, []string{"deploy", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for ambiguous devops modules, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_scope_defaulted_early_failure_names_resolved_env", normalize.Apply(result.Combined))
}

func testDeployDryRunUnresolvableScopeFailureOmitsEnvPair(t *testing.T) {
	// With no tenant, the header drops the pair, and the error names deploy (not open).
	setup := env.New(t)
	result := erun.Run(t, []string{"deploy", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit with no tenant configured, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_unresolvable_scope_failure_omits_env_pair", normalize.Apply(result.Combined))
}

func testDeployDryRunDefaultEnvironmentNotConfiguredNamesOpenAndRecovery(t *testing.T) {
	setup := env.New(t)
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(filepath.Join(root, "team"), 0o755); err != nil {
		t.Fatalf("mkdir tenant config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("defaulttenant: team\n"), 0o644); err != nil {
		t.Fatalf("write root config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "team", "config.yaml"), []byte("name: team\n"), 0o644); err != nil {
		t.Fatalf("write tenant config: %v", err)
	}
	result := erun.Run(t, []string{"deploy", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit with no default environment configured, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_default_environment_not_configured_names_open_and_recovery", normalize.Apply(result.Combined))
}

func testDeployDryRunNoDevopsModuleBootstrapsPublishedRuntime(t *testing.T) {
	// With no local charts, an empty selection bootstraps the runtime from the
	// published erun-devops chart once the probe override confirms it.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("expected exit 0 bootstrapping the published runtime, got %d:\n%s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_no_devops_module_bootstraps_published_runtime", normalize.Apply(result.Combined))
}

func testDeployDryRunUnconfiguredAliasDerivesProviderAndRegion(t *testing.T) {
	// Provider derives from the alias shape and region from the context name suffix.
	setup := env.New(t)
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, "team")
	envDir := filepath.Join(tenantDir, "prod")
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("defaulttenant: team\n"), 0o644); err != nil {
		t.Fatalf("root cfg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tenantDir, "config.yaml"),
		[]byte("projectroot: "+setup.Cwd+"\nname: team\ndefaultenvironment: prod\n"), 0o644); err != nil {
		t.Fatalf("tenant cfg: %v", err)
	}
	envBody := "name: prod\nrepopath: " + setup.Cwd + "\nkubernetescontext: erun-001-team-eu-west-2\ncontainerregistry: registry.example/test\nruntimeversion: 1.0.0\nmanagedcloud: true\ncloudprovideralias: ops+123456789012@aws\n"
	if err := os.WriteFile(filepath.Join(envDir, "config.yaml"), []byte(envBody), 0o644); err != nil {
		t.Fatalf("env cfg: %v", err)
	}
	fixture.SeedDevopsRepo(t, setup, "team", "prod")
	result := erun.Run(t, []string{"deploy", "team", "prod", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_unconfigured_alias_derives_provider_and_region", normalize.Apply(result.Combined))
}

func testDeployDryRunTenantAliasesResolveOIDCIssuers(t *testing.T) {
	// Covers an explicit issuer, a duplicate, and one derived from a non-awsapps SSO URL.
	setup := env.New(t)
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, "team")
	envDir := filepath.Join(tenantDir, "dev")
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	rootBody := "defaulttenant: team\n" +
		"cloudproviders:\n" +
		"  - alias: alpha\n" +
		"    provider: aws\n" +
		"    profile: alpha\n" +
		"    oidcissuerurl: https://oidc.example/shared\n" +
		"  - alias: beta\n" +
		"    provider: aws\n" +
		"    profile: beta\n" +
		"    oidcissuerurl: https://oidc.example/shared\n" +
		"  - alias: gamma\n" +
		"    provider: aws\n" +
		"    profile: gamma\n" +
		"    ssostarturl: https://idp.example/realm\n"
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(rootBody), 0o644); err != nil {
		t.Fatalf("root cfg: %v", err)
	}
	tenantBody := "projectroot: " + setup.Cwd + "\nname: team\ndefaultenvironment: dev\n" +
		"cloudprovideraliases:\n  - alpha\n  - beta\n  - gamma\n"
	if err := os.WriteFile(filepath.Join(tenantDir, "config.yaml"), []byte(tenantBody), 0o644); err != nil {
		t.Fatalf("tenant cfg: %v", err)
	}
	envBody := "name: dev\nrepopath: " + setup.Cwd + "\nkubernetescontext: test-context\ncontainerregistry: registry.example/test\nruntimeversion: 1.0.0\n"
	if err := os.WriteFile(filepath.Join(envDir, "config.yaml"), []byte(envBody), 0o644); err != nil {
		t.Fatalf("env cfg: %v", err)
	}
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_tenant_aliases_resolve_oidc_issuers", normalize.Apply(result.Combined))
}

func testDeployDryRunAWSAliasWithoutCloudContextResolvesRegionFromAlias(t *testing.T) {
	// An empty AWS_REGION would override the pod profile's region, so the alias'
	// Identity Center region is the fallback.
	setup := env.New(t)
	fixture.SeedLocalTenantEnvWithAWSAlias(t, setup, "team", "dev", "ops+123456789012@aws", "eu-west-2", "")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_aws_alias_without_cloud_context_resolves_region_from_alias", normalize.Apply(result.Combined))
}

func testDeployDryRunAWSAliasResolvesRegionFromECRRegistryHost(t *testing.T) {
	// Last fallback: the region encoded in the tenant's ECR registry host.
	setup := env.New(t)
	fixture.SeedLocalTenantEnvWithAWSAlias(t, setup, "team", "dev", "ops+123456789012@aws", "", "123456789012.dkr.ecr.eu-west-1.amazonaws.com/team")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_aws_alias_resolves_region_from_ecr_registry_host", normalize.Apply(result.Combined))
}

func testDeployDryRunOutsideDevopsWithTenantEnv(t *testing.T) {
	// From a cwd outside the devops module (e.g. desktop from $HOME), the tenant
	// project root drives chart discovery.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: setup.Env()})
	golden.Equal(t, "deploy/dry_run_outside_devops_with_tenant_env", normalize.Apply(result.Combined))
}

func testDeployDryRunRuntimeChartOverrideOffATenantUmbrellaDropsTheSubchartScope(t *testing.T) {
	// Naming the canonical chart drops the subchart nesting (helm silently ignores
	// values for an unwrapped subchart) but leaves the tenant's image untouched.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=team-devops:1.0.0")
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "1.0.0",
		"--runtime-chart", "oci://ghcr.io/sophium/charts/erun-devops:1.2.3",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "oci://ghcr.io/sophium/charts/erun-devops --version 1.2.3") {
		t.Fatalf("named chart not installed at its own version: %s", result.Combined)
	}
	if !strings.Contains(result.Combined, "--set-string imageOverrides.erun-devops=registry.example/test/team-devops:1.0.0") {
		t.Fatalf("image override not in the installed chart's scope: %s", result.Combined)
	}
	if strings.Contains(result.Combined, "erun-devops.imageOverrides") {
		t.Fatalf("values still nested under a subchart the named chart does not wrap: %s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_runtime_chart_override_off_a_tenant_umbrella_drops_the_subchart_scope", normalize.Apply(result.Combined))
}

func testDeployComponentsConfigScopesComponentChartLookup(t *testing.T) {
	// components.<name>.k8s scopes the lookup, so the decoy chart cannot collide.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedProjectComponentsConfig(t, setup, fixture.ComponentPathsConfig{
		Name: "ingest-worker",
		K8s:  "harnesses/ingest-worker/k8s",
	})
	fixture.SeedK8sChartAt(t, filepath.Join(setup.Cwd, "harnesses", "ingest-worker", "k8s"), "ingest-worker", "team", "dev")
	fixture.SeedK8sChartAt(t, filepath.Join(setup.Cwd, "decoy-devops", "k8s"), "ingest-worker", "team", "dev")
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "1.0.0",
		"--components", "ingest-worker",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/components_config_scopes_component_chart_lookup", normalize.Apply(result.Combined))
}

func testDeployDryRunFromChartCwdResolvesSingleChartContext(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	chart := fixture.SeedDevopsRepo(t, setup, "team", "dev")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: chart, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_from_chart_cwd_resolves_single_chart_context", normalize.Apply(result.Combined))
}
