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
	// Regression: when cwd has no devops context, the
	// deploy command must still be registered so the desktop UI's
	// `erun deploy <tenant> <env> --version X` invocation can land its
	// flags. Pre-fix, this returned the root help and "unknown flag:
	// --version". Lives unskipped so the integration suite fails until
	// erun-cli/cmd/root.go always registers deployCommand().
	setup := env.New(t)
	result := erun.Run(t, []string{"deploy", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/help_outside_devops_cwd", normalize.Apply(result.Combined))
}

func testDeployVersionFlagRecognizedOutsideDevopsCwd(t *testing.T) {
	// A second regression check: even when the flag is set on a real
	// deploy attempt, "unknown flag: --version" must not appear. The
	// command will still fail (no env or no chart) but for a sensible
	// reason rather than flag parsing. Lives unskipped so the suite
	// fails until the deploy registration fix lands.
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
	// The reported failure: an environment initialised before anything
	// provisioned a platform alias records no platform-alias Secret, so the
	// runtime chart mounts nothing, the entrypoint's seeder correctly finds
	// nothing, and the pod can never call the platform API -- `erun review
	// list` among them. Deploying it from a host that is signed in is the one
	// step such an environment takes that can fix it, and this is what that
	// deploy now renders: the Secret applied, its name threaded into the helm
	// upgrade so the chart mounts it, and the name recorded on the
	// environment so every later deploy keeps it. Before the change this
	// deploy was byte-for-byte the same as an env whose host had nothing to
	// give -- no Secret, no --set, and the pod left unable to reach the
	// platform.
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
	// A host env has no pod and no cluster at all, so deploy must refuse it
	// by name instead of resolving a helm plan that cannot run anywhere.
	setup := env.New(t)
	fixture.SeedHostTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "deploy/refuses_host_environment", normalize.Apply(result.Combined))
}

func testDeployDryRunFromDevopsModuleRoot(t *testing.T) {
	// Exercises resolveCurrentDevopsK8sDir's first arm: when cwd is the
	// <tenant>-devops module root (not its k8s/ subdir and not a chart
	// dir), the module's own k8s/ directory must drive chart discovery.
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
	// Exercises resolveAncestorDevopsK8sDir: from a nested directory
	// inside the devops module (e.g. team-devops/notes), the walker must
	// climb to the nearest *-devops ancestor and use its k8s/ directory.
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
	// Exercises ResolveCurrentKubernetesDeployContexts' k8s-dir arm:
	// when cwd is the devops module's k8s/ directory itself, every chart
	// underneath it resolves directly without the devops-dir walkers.
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
	// A paths.k8s override in .erun/config.yaml relocates the chart tree out of
	// <tenant>-devops/k8s: deploy resolves the runtime chart from deploy/k8s
	// and traces the configured dir as a decision line. No <tenant>-devops
	// module exists, so without the override deploy would fall back to the
	// published runtime chart.
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
	// A project config that resolves from disk but is excluded by .gitignore
	// silently works on this machine only, since git never sees it. The
	// deploy trace must surface that as a warning instead of staying silent.
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
	// A paths.k8s override pointing at a directory not named "k8s" fails with an
	// error explaining the naming constraint, rather than silently falling back
	// to convention discovery.
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
	// Exercises detectedProjectRootDevopsK8sDir: from a git project root
	// whose repo name equals the tenant, chart discovery must go straight
	// to <root>/<tenant>-devops/k8s instead of scanning every *-devops
	// candidate. The repo dir is named "team" so findProjectRoot's
	// detected tenant matches the configured one.
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
	// Exercises resolveProjectRootDevopsK8sDir's ambiguity guard: two
	// *-devops modules with k8s charts under the project root cannot be
	// disambiguated, so deploy must fail with "multiple devops k8s
	// directories found" instead of picking one arbitrarily.
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
	// Regression: a deploy that takes its target from the current scope
	// rather than explicit args carried an empty tenant/environment through
	// the whole command, so every early failure rendered the header as
	// "==> Deploy failed /:" — a bare separator the desktop cannot attribute
	// to an env — and the per-env trace log was never opened. Same ambiguous
	// -devops-modules failure as the scenario above, with the target left to
	// the default scope: the header must name team/dev.
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
	// The other arm of the same header: with no tenant configured at all
	// there is no env to name, so the header drops the tenant/environment
	// pair entirely instead of falling back to a bare separator. Also locks
	// resolveOpenTenant's inference-permitted-but-unresolved error: it must
	// name the command that actually ran (deploy, not open), not just repeat
	// "default tenant is not configured", and state that command's recovery.
	setup := env.New(t)
	result := erun.Run(t, []string{"deploy", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit with no tenant configured, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_unresolvable_scope_failure_omits_env_pair", normalize.Apply(result.Combined))
}

func testDeployDryRunDefaultEnvironmentNotConfiguredNamesOpenAndRecovery(t *testing.T) {
	// Sibling of the scenario above for resolveOpenEnvironment: a tenant
	// resolves (from the configured default), but that tenant has no
	// default environment, so the error must name the tenant and open's
	// recovery instead of repeating the bare "default environment is not
	// configured".
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
	// Opt-in-only resolution: a local env whose project root has no
	// *-devops module has no local charts, so an empty selection defaults to
	// the runtime and — finding no repo-local runtime chart — bootstraps the
	// env on the published erun-devops chart by reference, once the runtime
	// chart ladder confirms it published at the version (the
	// ERUN_PUBLISHED_CHART_PROBE_OVERRIDE seam stands in for that registry
	// read). This replaces the old "no devops module errors" contract: a
	// configured env can heal its runtime from a confirmed published chart,
	// exit 0.
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
	// Exercises applyCloudProviderDeployMetadata's fallbacks: the env's
	// cloudprovideralias has no configured provider, so the provider
	// derives from the alias shape (cloudProviderFromAlias → "aws"), no
	// cloud context matches the kubernetes context, and the region
	// derives from the context name suffix (cloudContextRegionFromName →
	// eu-west-2). The helm set-strings must carry both derived values.
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
	// Exercises ResolveTenantCloudProviderIssuers +
	// CloudProviderOIDCIssuerURL: a tenant carrying three provider
	// aliases must resolve each issuer (explicit oidcissuerurl, a
	// duplicate that is deduplicated, and one derived from a non-awsapps
	// SSO start URL) into the api.oidcAllowedIssuers helm set-string.
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
	// An env with a provider alias but no cloud context used to thread
	// `cloudContext.region=` — an empty AWS_REGION that overrides the pod
	// profile's own region instead of falling back to it, breaking every AWS
	// call in the pod. The alias' Identity Center region is the fallback, and
	// the helm command must carry it.
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
	// Last region fallback: the alias records no Identity Center region, so
	// the only region left to find is the one the tenant's ECR registry host
	// encodes. Its sibling above locks the alias tier.
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
	// Regression: when erun deploy <tenant> <env> is
	// invoked from a cwd outside the devops module (e.g. the desktop
	// UI launching the binary from $HOME for a remote environment),
	// the resolved tenant project root must drive chart discovery
	// instead of cwd. Pre-fix this hit "helm chart not found in
	// current directory" because resolveCurrentDevopsK8sDir gated
	// chart resolution on cwd == projectRoot.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: setup.Env()})
	golden.Equal(t, "deploy/dry_run_outside_devops_with_tenant_env", normalize.Apply(result.Combined))
}

func testDeployDryRunRuntimeChartOverrideOffATenantUmbrellaDropsTheSubchartScope(t *testing.T) {
	// The value scope belongs to the chart that is actually installed. This env
	// resolves the tenant's own charts/team-devops umbrella, whose runtime
	// values are re-scoped under the erun-devops subchart key; naming the
	// canonical chart instead must drop that nesting, because helm silently
	// ignores values addressed to a subchart the chart does not wrap -- the
	// image override and the required tenant/environment values would land
	// nowhere and the pod would come up wrong rather than failing. The bundled
	// values pull goes with it: a canonical chart ships no per-env values.
	//
	// The image is deliberately untouched -- it stays the tenant's own
	// team-devops at the deploy version. That is the whole point of naming the
	// chart separately: the chart rides erun's release line, the image rides the
	// tenant's, and overriding one must not move the other.
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
	// A declared .erun/config.yaml components.<name>.k8s (erun#1840's
	// monorepo-of-independent-deployables shape) scopes --components' chart
	// lookup to that harness's own k8s root instead of walking the whole
	// project, so a same-named chart elsewhere (here, a decoy under an
	// unrelated -devops module) cannot collide with it. Without the scoping
	// this would resolve "multiple Helm charts found for component".
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
	// When deploy runs with cwd inside the chart directory itself,
	// ResolveCurrentKubernetesDeployContexts takes the direct-context
	// branch: the cwd is the chart, ValidateHelmChartPath vets its
	// Chart.yaml, and exactly one deploy context resolves without the
	// project-root devops/k8s directory scan.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	chart := fixture.SeedDevopsRepo(t, setup, "team", "dev")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: chart, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_from_chart_cwd_resolves_single_chart_context", normalize.Apply(result.Combined))
}
