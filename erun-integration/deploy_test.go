package integration

import (
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestDeploy(t *testing.T) {
	t.Run("help_outside_devops_cwd", func(t *testing.T) {
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
	})

	t.Run("version_flag_recognized_outside_devops_cwd", func(t *testing.T) {
		// A second regression check: even when the flag is set on a real
		// deploy attempt, "unknown flag: --version" must not appear. The
		// command will still fail (no env or no chart) but for a sensible
		// reason rather than flag parsing. Lives unskipped so the suite
		// fails until the deploy registration fix lands.
		setup := env.New(t)
		result := erun.Run(t, []string{"deploy", "missing", "missing", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "deploy/version_flag_recognized_outside_devops_cwd", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_from_devops_cwd", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "deploy/dry_run_from_devops_cwd", normalize.Apply(result.Combined))
	})

	t.Run("refuses_host_environment", func(t *testing.T) {
		// A host env has no pod and no cluster at all, so deploy must refuse it
		// by name instead of resolving a helm plan that cannot run anywhere.
		setup := env.New(t)
		fixture.SeedHostTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "deploy/refuses_host_environment", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_stopped_env_renders_replicas_zero", func(t *testing.T) {
		// deploy reconciles the operator's stop rather than overriding it: an env
		// carrying `stopped: true` threads --set stopped=true so the chart renders
		// replicas: 0, and a helm upgrade cannot silently restart a pod the
		// operator deliberately scaled away. deploy never wakes — `erun open`
		// does, which is also why deploy's version dedup skipping the helm call
		// cannot leave the run/stop state inconsistent. A running env emits no
		// such --set (the sibling dry_run_from_devops_cwd golden shows its absence).
		setup := env.New(t)
		fixture.SeedStoppedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_stopped_env_renders_replicas_zero", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_platform_account_binds_cluster_admin", func(t *testing.T) {
		// An env flagged platformaccount:true threads --set platformAccount=true
		// into the runtime helm command, so the chart binds the env's SA to
		// cluster-admin (the <release>-platform ClusterRoleBinding) — the grant
		// that lets in-pod `erun terraform apply` of the cluster edge create
		// namespaces and CRDs. An off-by-default env emits no such --set (the
		// sibling dry_run_from_devops_cwd golden shows its absence).
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		existing, err := os.ReadFile(envConfigPath)
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		mustWriteFile(t, envConfigPath, string(existing)+"platformaccount: true\n")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_platform_account_binds_cluster_admin", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_mcp_auth_public_key", func(t *testing.T) {
		// --mcp-auth-public-key makes the runtime deploy require the per-env MCP
		// edge to authenticate bearer tokens signed by the desktop public key:
		// the key is applied out-of-band as a <release>-mcp-auth Secret and the
		// file:// issuer + per-env audience ride as mcpAuth.* helm values on the
		// runtime (team-devops) chart only.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		keyPath := filepath.Join(t.TempDir(), "desktopid.pub")
		if err := os.WriteFile(keyPath, []byte("-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAtestkeytestkeytestkeytestkeytestkeytestke=\n-----END PUBLIC KEY-----\n"), 0o600); err != nil {
			t.Fatalf("write public key fixture: %v", err)
		}
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--mcp-auth-public-key", keyPath, "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_with_mcp_auth_public_key", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_copies_images_from_to_before_deploy", func(t *testing.T) {
		// When the project registry list marks a FROM source and a TO
		// destination, deploy mirrors every image the cluster pulls (the
		// erun-devops runtime image plus any locally-built component) from FROM
		// to each TO with a manifest-aware `docker buildx imagetools create`
		// before the helm upgrade, and the cluster pulls from the DEPLOY (TO)
		// registry. The copy is a dry-run trace line gated behind real-run.
		setup := env.New(t)
		fixture.SeedTenantEnvNoRegistry(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedProjectK8sConfig(t, setup,
			"containerregistries:\n"+
				"    - registry: ghcr.io/sophium\n"+
				"      roles: [build, from]\n"+
				"    - registry: registry.internal/team\n"+
				"      roles: [to, deploy]\n",
		)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_copies_images_from_to_before_deploy", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_from_devops_module_root", func(t *testing.T) {
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
	})

	t.Run("dry_run_from_nested_devops_subdir", func(t *testing.T) {
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
	})

	t.Run("dry_run_from_k8s_dir", func(t *testing.T) {
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
	})

	t.Run("dry_run_configured_k8s_path", func(t *testing.T) {
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
	})

	t.Run("configured_k8s_path_wrong_name_errors", func(t *testing.T) {
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
	})

	t.Run("dry_run_from_git_project_root_detects_tenant_devops", func(t *testing.T) {
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
	})

	t.Run("dry_run_multiple_devops_modules_errors", func(t *testing.T) {
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
	})

	t.Run("dry_run_scope_defaulted_early_failure_names_resolved_env", func(t *testing.T) {
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
	})

	t.Run("dry_run_unresolvable_scope_failure_omits_env_pair", func(t *testing.T) {
		// The other arm of the same header: with no tenant configured at all
		// there is no env to name, so the header drops the tenant/environment
		// pair entirely instead of falling back to a bare separator.
		setup := env.New(t)
		result := erun.Run(t, []string{"deploy", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit with no tenant configured, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_unresolvable_scope_failure_omits_env_pair", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_no_devops_module_bootstraps_published_runtime", func(t *testing.T) {
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
	})

	t.Run("dry_run_unconfigured_alias_derives_provider_and_region", func(t *testing.T) {
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
	})

	t.Run("dry_run_tenant_aliases_resolve_oidc_issuers", func(t *testing.T) {
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
	})

	t.Run("dry_run_aws_alias_without_cloud_context_resolves_region_from_alias", func(t *testing.T) {
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
	})

	t.Run("dry_run_aws_alias_resolves_region_from_ecr_registry_host", func(t *testing.T) {
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
	})

	t.Run("real_run_remote_env_published_chart_via_stubs", func(t *testing.T) {
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
	})

	t.Run("real_run_refreshes_image_pull_secret_via_stubbed_kubectl", func(t *testing.T) {
		// Real-run counterpart of the dry-run image-pull-secret scenarios:
		// proves the kubectl apply actually executes (Command + stdin
		// manifest + CombinedOutput), not just that dry-run traces it. The
		// kubectl stub exits 0 so the apply — and the rollout after it —
		// both succeed.
		setup := env.New(t)
		fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
		envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		existing, err := os.ReadFile(envConfigPath)
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		mustWriteFile(t, envConfigPath, string(existing)+"imagepullsecrets:\n    - ecr-pull\n")

		dockerCfgDir := filepath.Join(setup.Cwd, "docker-inline")
		if err := os.MkdirAll(dockerCfgDir, 0o755); err != nil {
			t.Fatalf("mkdir docker config dir: %v", err)
		}
		encoded := base64.StdEncoding.EncodeToString([]byte("AWS:s3cret-token"))
		dockerCfg := fmt.Sprintf(`{"auths":{"registry.example":{"auth":%q}}}`, encoded)
		if err := os.WriteFile(filepath.Join(dockerCfgDir, "config.json"), []byte(dockerCfg), 0o644); err != nil {
			t.Fatalf("write docker config: %v", err)
		}

		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0", "DOCKER_CONFIG="+dockerCfgDir)
		envVars = append(envVars, fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if strings.Contains(result.Combined, "s3cret-token") || strings.Contains(result.Combined, encoded) {
			t.Fatalf("the resolved credential must never appear in trace output: %s", result.Combined)
		}
		golden.Equal(t, "deploy/real_run_refreshes_image_pull_secret_via_stubbed_kubectl", normalize.Apply(result.Combined))
	})

	t.Run("real_run_new_worktree_volume_announces_the_adoption", func(t *testing.T) {
		// The regression this exists for: a deploy that first introduces the
		// dedicated worktree volume to an environment whose checkout still lives
		// on the home volume reported plain success while mounting an empty
		// claim over that checkout. The claim's absence is a decision input a
		// trace cannot supply, so kubectl is stubbed to report it NotFound; the
		// deploy must then name the worktree path, the volume it moves onto, and
		// where the pre-move copy is kept.
		setup := env.New(t)
		fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "helm", "")
		// The ERUN_PUBLISHED_CHART_PROBE_OVERRIDE seam marks erun-devops published
		// so the runtime chart ladder confirms it instead of refusing.
		envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
		envVars = append(envVars, fixture.StubKubectlWorktreeClaim(t, stubs, fixture.KubectlWorktreeClaimStubSpec{
			ClaimName: "team-devops-worktree",
			Stderr:    `Error from server (NotFound): persistentvolumeclaims "team-devops-worktree" not found`,
			ExitCode:  1,
		})...)
		envVars = append(envVars, fixture.StubEnv(stubs, "helm")...)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/real_run_new_worktree_volume_announces_the_adoption", normalize.Apply(result.Combined))
	})

	t.Run("real_run_unreadable_worktree_volume_still_announces", func(t *testing.T) {
		// A cluster the claim read fails against is "unknown", never "settled":
		// staying quiet is the exact failure being fixed, so the notice prints
		// anyway and the read's error is traced beside it.
		setup := env.New(t)
		fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "helm", "")
		// The ERUN_PUBLISHED_CHART_PROBE_OVERRIDE seam marks erun-devops published
		// so the runtime chart ladder confirms it instead of refusing.
		envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
		envVars = append(envVars, fixture.StubKubectlWorktreeClaim(t, stubs, fixture.KubectlWorktreeClaimStubSpec{
			ClaimName: "team-devops-worktree",
			Stderr:    "error: You must be logged in to the server (Unauthorized)",
			ExitCode:  1,
		})...)
		envVars = append(envVars, fixture.StubEnv(stubs, "helm")...)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/real_run_unreadable_worktree_volume_still_announces", normalize.Apply(result.Combined))
	})

	t.Run("real_run_remote_env_tenant_umbrella_pulls_bundled_values", func(t *testing.T) {
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
	})

	t.Run("real_run_recreates_deployment_on_immutable_selector_change", func(t *testing.T) {
		// Real-run deploy where the installed Deployment's immutable
		// spec.selector differs from the chart's: helm aborts the first
		// upgrade with "field is immutable". DeployHelmChart classifies that as
		// a HelmImmutableSelectorError and RunHelmDeploy recovers — deleting the
		// named Deployment (its PVCs are separate objects and survive) and
		// retrying the upgrade, which the fail-first helm stub lets succeed.
		// This branch is reachable only in real-run (the recovery fires on a
		// helm side-effect failure, not a pre-action decision), so it is a
		// stub-driven non-dry-run scenario. It locks the recover+retry trace.
		setup := env.New(t)
		fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinaryFailFirstThenSucceed(t, stubs, "helm",
			`Error: UPGRADE FAILED: cannot patch "team-devops" with kind Deployment: Deployment.apps "team-devops" is invalid: spec.selector: Invalid value: {"matchLabels":{"app":"erun-devops"}}: field is immutable`, 1)
		fixture.StubBinary(t, stubs, "docker", "")
		// The ERUN_PUBLISHED_CHART_PROBE_OVERRIDE seam marks erun-devops published
		// so the runtime chart ladder confirms it instead of refusing.
		envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
		envVars = append(envVars, fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/real_run_recreates_deployment_on_immutable_selector_change", normalize.Apply(result.Combined))
	})

	t.Run("real_run_copies_images_from_to_via_stubs", func(t *testing.T) {
		// Real-run deploy of an env whose registry list marks a FROM source and
		// a TO destination: before helm upgrade, the runtime image is mirrored
		// from FROM to TO with `docker buildx imagetools create` (executed
		// through the docker stub), and the cluster pulls from the DEPLOY (TO)
		// registry. This proves the copy ACTION runs in real-run, not just its
		// dry-run trace.
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
		mustWriteFile(t, filepath.Join(tenantDir, "config.yaml"),
			"projectroot: /nonexistent-remote/team\nname: team\ndefaultenvironment: dev\n")
		mustWriteFile(t, filepath.Join(envDir, "config.yaml"),
			"name: dev\nrepopath: /nonexistent-remote/team\nkubernetescontext: test-context\ntype: remote-agent\nruntimeversion: 1.0.0\n"+
				"containerregistries:\n"+
				"    - registry: ghcr.io/sophium\n      roles: [build, from]\n"+
				"    - registry: registry.internal/team\n      roles: [to, deploy]\n")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "docker", "")
		// The ERUN_PUBLISHED_CHART_PROBE_OVERRIDE seam marks erun-devops published
		// so the runtime chart ladder confirms it instead of refusing.
		envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
		envVars = append(envVars, fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/real_run_copies_images_from_to_via_stubs", normalize.Apply(result.Combined))
	})

	t.Run("real_run_preflight_starts_stopped_cloud_context", func(t *testing.T) {
		// Exercises CloudContextPreflight end to end: the env's kubernetes
		// context belongs to a managed cloud context whose live AWS state is
		// "stopped", so the deploy must start the instance (full
		// StartCloudContext flow through the aws stub) before any kubectl or
		// helm call targets the cluster. Dry-run cannot reach the start: the
		// dry-run describe-instances canned answer reports "running" by
		// design so plans never spuriously start contexts.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		// Pin an always-permitting working-hours window so the preflight's
		// gate clears no matter when the suite runs; without it the default
		// 08:00-20:00 local window fails this scenario outside office hours.
		// The permitting and gated arms themselves are locked by
		// context_test.go with explicit windows.
		envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		envBody, err := os.ReadFile(envConfigPath)
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		envBody = append(envBody, []byte("idle:\n  workinghours: 00:00-23:59\n  timezone: UTC\n")...)
		if err := os.WriteFile(envConfigPath, envBody, 0o644); err != nil {
			t.Fatalf("write env config: %v", err)
		}
		// The cloud context owns the env's kubernetes context name.
		seedCloudConfigWithContexts(t, setup, contextYAMLItem("test-context", "dev", "us-east-1", "i-0123456789abcdef0"))
		stubs := setup.Cwd + "/stubs"
		profileARN := "arn:aws:iam::123456789012:instance-profile/erun-test-context-host-stop"
		envVars := append(setup.Env(), fixture.StubAWSCloudContext(t, stubs, fixture.AWSCloudContextStubSpec{
			RoleName:             "erun-test-context-host-stop",
			InstanceProfileARN:   profileARN,
			ProfileRoleName:      "erun-test-context-host-stop",
			ActiveAssociationID:  "iip-assoc-0aa11bb22cc33dd44",
			ActiveAssociationARN: profileARN,
			InstanceStates:       "i-0123456789abcdef0\tstopped",
		})...)
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars = append(envVars, fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/real_run_preflight_starts_stopped_cloud_context", normalize.Apply(result.Combined))
	})

	t.Run("version_required_without_switch_errors", func(t *testing.T) {
		// deploy is a pure consume operation: with no --version and no
		// --current it must fail with an actionable "version required" error
		// rather than building the working tree.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when deploy has no version and no --current, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/version_required_without_switch_errors", normalize.Apply(result.Combined))
	})

	t.Run("force_flag_appears_in_trace", func(t *testing.T) {
		// --force surfaces in the deploy trace so dry-run callers can see
		// the cache-bypass decision, and it propagates to the resolved
		// plan: SkipHelm cannot short-circuit when fingerprint promotion
		// is disabled, so the helm upgrade always runs.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--force", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "deploy/force_flag_appears_in_trace", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_env_deploy_timeout_overrides_default", func(t *testing.T) {
		// The env's deploy.timeout (7m0s) flows into the helm `upgrade
		// --timeout` arg, overriding the 5m0s default. Locks per-env rollout
		// timeout resolution (config > default).
		setup := env.New(t)
		fixture.SeedTenantEnvWithDeployTimeout(t, setup, "team", "dev", "7m0s")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_env_deploy_timeout_overrides_default", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_rollout_timeout_flag_overrides_env_config", func(t *testing.T) {
		// --rollout-timeout 9m beats the env's deploy.timeout (7m0s), which in
		// turn beats the default. Locks the full precedence chain (flag > env
		// config > default) at the resolved helm `--timeout` arg.
		setup := env.New(t)
		fixture.SeedTenantEnvWithDeployTimeout(t, setup, "team", "dev", "7m0s")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--rollout-timeout", "9m", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_rollout_timeout_flag_overrides_env_config", normalize.Apply(result.Combined))
	})

	t.Run("rollout_timeout_flag_invalid_duration_errors", func(t *testing.T) {
		// A malformed --rollout-timeout fails the deploy loudly rather than
		// silently falling back to the default.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--rollout-timeout", "bogus", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit on a malformed rollout timeout:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/rollout_timeout_flag_invalid_duration_errors", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_max_cpu_memory_storage_flags_apply_namespace_quota", func(t *testing.T) {
		// All three of --max-cpu/--max-memory/--max-storage together trace the
		// kubectl apply that would create the namespace's ResourceQuota +
		// LimitRange, alongside the existing namespace-ensure trace.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--max-cpu", "4", "--max-memory", "8Gi", "--max-storage", "80Gi", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_max_cpu_memory_storage_flags_apply_namespace_quota", normalize.Apply(result.Combined))
	})

	t.Run("max_cpu_memory_storage_flags_partial_set_errors", func(t *testing.T) {
		// A namespace ResourceQuota with only some resources capped would leave
		// Kubernetes to admit unbounded amounts of the rest, so a partial set
		// (here, --max-cpu alone) fails loudly rather than silently applying an
		// incomplete quota.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--max-cpu", "4", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit on a partial namespace quota:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/max_cpu_memory_storage_flags_partial_set_errors", normalize.Apply(result.Combined))
	})

	t.Run("env_deploy_timeout_invalid_duration_errors", func(t *testing.T) {
		// A malformed per-env deploy.timeout fails the deploy loudly at spec
		// resolution (EnvironmentDeployConfig.Resolve) rather than reverting to
		// the default.
		setup := env.New(t)
		fixture.SeedTenantEnvWithDeployTimeout(t, setup, "team", "dev", "nonsense")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit on a malformed env deploy timeout:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/env_deploy_timeout_invalid_duration_errors", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_pinned_version_installs_buildable_chart_without_building", func(t *testing.T) {
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
	})

	t.Run("real_run_pinned_version_missing_image_errors", func(t *testing.T) {
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
	})

	t.Run("dry_run_outside_devops_with_tenant_env", func(t *testing.T) {
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
	})

	t.Run("dry_run_remote_env_refuses_when_no_runtime_chart_is_confirmed", func(t *testing.T) {
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
	})

	t.Run("dry_run_refuses_shared_chart_confirmed_only_at_a_different_version", func(t *testing.T) {
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
	})

	t.Run("dry_run_remote_env_prefers_tenant_published_runtime_chart", func(t *testing.T) {
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
	})

	t.Run("dry_run_remote_env_tenant_umbrella_explicit_runtime_image_wins", func(t *testing.T) {
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
	})

	t.Run("dry_run_remote_env_cluster_registry_chart_from_runtime_image", func(t *testing.T) {
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
	})

	t.Run("dry_run_runtime_chart_resolves_from_the_platform_registry", func(t *testing.T) {
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
	})

	t.Run("dry_run_runtime_chart_prefers_the_deploy_registry_over_the_platform_registry", func(t *testing.T) {
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
	})

	t.Run("dry_run_operator_set_runtime_registry_stands_over_a_different_resolution", func(t *testing.T) {
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
	})

	t.Run("real_run_records_the_registry_the_runtime_chart_resolved_from", func(t *testing.T) {
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
	})

	t.Run("real_run_records_the_deploy_registry_when_the_chart_resolves_there", func(t *testing.T) {
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
	})

	t.Run("real_run_keeps_an_operator_set_runtime_registry", func(t *testing.T) {
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
	})

	t.Run("dry_run_remote_env_image_pull_secrets", func(t *testing.T) {
		// A tenant umbrella image can be a private ghcr package. The env's
		// imagepullsecrets list rides into the runtime deploy as
		// imagePullSecrets[i].name, re-scoped under the erun-devops subchart key so
		// the runtime pod authenticates the pull. Orthogonal to the umbrella's
		// own-image default (both appear); an env with no list emits nothing.
		setup := env.New(t)
		fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
		envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		existing, err := os.ReadFile(envConfigPath)
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		mustWriteFile(t, envConfigPath, string(existing)+"imagepullsecrets:\n    - ghcr-pull\n")
		envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=team-devops:1.0.0")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_remote_env_image_pull_secrets", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_remote_env_image_pull_secret_refreshed_from_host_credential", func(t *testing.T) {
		// An ECR authorization token expires after twelve hours (#1256), so a
		// pull secret named once via --image-pull-secret and never refreshed
		// eventually rots. When the host running `erun deploy` already has a
		// credential for the deploy registry (docker config, or the AWS CLI
		// for ECR), deploy re-mints the named Secret from it instead of
		// leaving whatever it held days ago. DOCKER_CONFIG points the host
		// resolver at this isolated dir, the same seam the init registry
		// credential scenario uses; the fixture's containerregistry
		// (registry.example/test) is deliberately addressed here.
		setup := env.New(t)
		fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
		envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		existing, err := os.ReadFile(envConfigPath)
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		mustWriteFile(t, envConfigPath, string(existing)+"imagepullsecrets:\n    - ecr-pull\n")

		dockerCfgDir := filepath.Join(setup.Cwd, "docker-inline")
		if err := os.MkdirAll(dockerCfgDir, 0o755); err != nil {
			t.Fatalf("mkdir docker config dir: %v", err)
		}
		encoded := base64.StdEncoding.EncodeToString([]byte("AWS:s3cret-token"))
		dockerCfg := fmt.Sprintf(`{"auths":{"registry.example":{"auth":%q}}}`, encoded)
		if err := os.WriteFile(filepath.Join(dockerCfgDir, "config.json"), []byte(dockerCfg), 0o644); err != nil {
			t.Fatalf("write docker config: %v", err)
		}

		envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=team-devops:1.0.0", "DOCKER_CONFIG="+dockerCfgDir)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if strings.Contains(result.Combined, "s3cret-token") || strings.Contains(result.Combined, encoded) {
			t.Fatalf("the resolved credential must never appear in trace output: %s", result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_remote_env_image_pull_secret_refreshed_from_host_credential", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_remote_env_image_pull_secret_covers_runtime_image_override_registry", func(t *testing.T) {
		// #1328: a runtimeimage pinned to a registry other than the env's
		// containerRegistry (e.g. built into one registry, deployed from
		// another) left the refreshed pull secret uncovered for the registry
		// the pod actually pulls from — before this fix, resolution only ever
		// tried containerRegistry's host, so a credential known for the
		// image's own registry was never even attempted. Here DOCKER_CONFIG
		// carries a credential for the runtimeimage's own registry
		// (otherregistry.example) but NOT for the fixture's containerregistry
		// (registry.example): before the fix this applied no secret at all
		// ("no host credential resolved for registry.example; leaving
		// ecr-pull untouched" and nothing else tried); after the fix the
		// image's own registry still resolves and the secret is applied.
		setup := env.New(t)
		fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
		envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		existing, err := os.ReadFile(envConfigPath)
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		mustWriteFile(t, envConfigPath, string(existing)+"imagepullsecrets:\n    - ecr-pull\nruntimeimage: otherregistry.example/team-devops\n")

		dockerCfgDir := filepath.Join(setup.Cwd, "docker-inline")
		if err := os.MkdirAll(dockerCfgDir, 0o755); err != nil {
			t.Fatalf("mkdir docker config dir: %v", err)
		}
		encoded := base64.StdEncoding.EncodeToString([]byte("AWS:s3cret-token"))
		dockerCfg := fmt.Sprintf(`{"auths":{"otherregistry.example":{"auth":%q}}}`, encoded)
		if err := os.WriteFile(filepath.Join(dockerCfgDir, "config.json"), []byte(dockerCfg), 0o644); err != nil {
			t.Fatalf("write docker config: %v", err)
		}

		envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=team-devops:1.0.0", "DOCKER_CONFIG="+dockerCfgDir)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if strings.Contains(result.Combined, "s3cret-token") || strings.Contains(result.Combined, encoded) {
			t.Fatalf("the resolved credential must never appear in trace output: %s", result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_remote_env_image_pull_secret_covers_runtime_image_override_registry", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_remote_env_umbrella_ignores_stale_stock_runtimeimage", func(t *testing.T) {
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
	})

	t.Run("dry_run_remote_env_umbrella_honours_tenants_own_runtimeimage_tag", func(t *testing.T) {
		// A pin naming the tenant's OWN team-devops image at a tag that disagrees
		// with the deploy version used to be discarded as "provably redundant"
		// and silently replaced with a guessed team-devops:<erun-version> tag.
		// That guess conflated two independent version lines: the tenant's own
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
	})

	t.Run("dry_run_remote_env_stated_chart_ignores_stale_stock_runtimeimage", func(t *testing.T) {
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
	})

	t.Run("dry_run_runtime_chart_flag_rescues_a_stock_runtime_image_override", func(t *testing.T) {
		// erun#1249: an env recorded at an old runtimechart version, deployed with
		// --version/--runtime-image/--runtime-chart together moving it forward.
		// Before the fix, the image inference read the env's stale recorded chart
		// version -- the --runtime-chart override was only applied to the deploy
		// spec afterward -- concluded the stock erun-devops image could not be
		// published "on another line", and silently substituted a tenant image
		// tag that was never built (ImagePullBackOff). The operator's own image
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
	})

	t.Run("dry_run_runtime_chart_flag_alone_heals_a_persisted_stock_runtimeimage_memo", func(t *testing.T) {
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
	})

	t.Run("dry_run_remote_env_explicit_runtime_image_wins_on_the_tenants_own_version_line", func(t *testing.T) {
		// erun#1249 second variant: the tenant's runtime image rides its own
		// project's version line (VERSION symlinked to the project's release, not
		// erun's), so an explicit --runtime-image shares a repository with the
		// deploy's inferred default image but differs only by tag. Before the fix,
		// staleRuntimeImageTrace treated that as a leftover pin and silently
		// substituted the inferred tag, which the operator had never published.
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
	})

	t.Run("dry_run_remote_env_deploys_published_components", func(t *testing.T) {
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
	})

	t.Run("dry_run_runtime_env_unreachable_repo_path_falls_back_to_local_plan", func(t *testing.T) {
		// #1116: a runtime env's configured repo path names an in-pod checkout
		// (e.g. /home/erun/git/frs) that does not exist on the host running this
		// deploy — the normal case for a runtime env driven from an operator's
		// laptop. Clearing (or never setting) deploy.components used to silently
		// drop selection to the runtime chart alone instead of following the repo
		// plan, because the plan read depended solely on that unreachable
		// configured path. Deploy now also tries the project rooted at its own
		// working directory (which here is exactly where the plan lives, the
		// normal case for a host sitting inside the tenant checkout), so the
		// plan's four components are selected without --components. No saved
		// deploy.components on the env, so this exercises the plan selection
		// tier specifically, not just a fallback trace.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedProjectK8sConfig(t, setup, "environments:\n  dev:\n    k8s:\n      deployments:\n        - team-devops\n        - [erun-backend-postgres, erun-backend-db]\n        - erun-backend-api\n")
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
			"name: dev\nrepopath: /nonexistent-remote/team\nkubernetescontext: test-context\n"+
				"containerregistry: registry.example/test\nruntimeversion: 1.0.0\ntype: runtime\n")
		envVars := append(setup.Env(),
			"ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=team-devops:1.0.0,erun-backend-postgres:1.0.0,erun-backend-db:1.0.0,erun-backend-api:1.0.0")
		result := erun.Run(t, []string{
			"deploy", "team", "dev",
			"--version", "1.0.0",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_runtime_env_unreachable_repo_path_falls_back_to_local_plan", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_remote_env_deploys_tenant_component_charts_by_reference", func(t *testing.T) {
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
	})

	t.Run("dry_run_remote_env_component_threads_resolved_oidc_issuer", func(t *testing.T) {
		// Regression: an empty computed api.oidcAllowedIssuers used
		// to be passed as `--set-string api.oidcAllowedIssuers=` regardless, and
		// helm's --set always beats -f, so it silently clobbered whatever the
		// operator configured under that key in the published chart's
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
	})

	t.Run("dry_run_remote_env_tenant_artifacts_require_published_charts", func(t *testing.T) {
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
	})

	t.Run("dry_run_stated_runtime_chart_is_exempt_from_the_tenant_chart_mandate", func(t *testing.T) {
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
	})

	t.Run("dry_run_runtime_env_mount_source_clones_at_release_ref", func(t *testing.T) {
		// A runtime env is sourceless by default (worktreeStorage=none). Opting
		// into MountSource (with a RepoURL) flips its runtime worktree to a PVC
		// checkout the pod clones at boot: the runtime deploy carries
		// worktreeStorage=pvc plus repoUrl and repoRef=v<version> (the release
		// tag), and the trace names the mutable-source decision. This is the
		// opt-in real-time-patching path; a plain runtime deploy is unaffected.
		setup := env.New(t)
		fixture.SeedRuntimeTenantEnvNoVersion(t, setup, "team", "dev")
		envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		existing, err := os.ReadFile(envConfigPath)
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		mustWriteFile(t, envConfigPath, string(existing)+"mountsource: true\nrepourl: https://github.com/sophium/erun.git\n")
		envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		// The ref is the release TAG v<version>, but normalize.Apply collapses
		// both "1.0.0" and "v1.0.0" to <VERSION>, so the snapshot cannot prove
		// the "v" prefix survived — assert it on the raw output (the value is
		// masked by normalization, per erun-integration/AGENTS.md).
		if !strings.Contains(result.Combined, "repoRef=v1.0.0") {
			t.Fatalf("expected repoRef=v1.0.0 (the release tag) in deploy command; got:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_runtime_env_mount_source_clones_at_release_ref", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_remote_env_custom_runtime_image", func(t *testing.T) {
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
	})

	t.Run("dry_run_env_states_its_runtime_chart_in_config", func(t *testing.T) {
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
	})

	t.Run("dry_run_runtime_chart_flag_beats_the_env_config_chart", func(t *testing.T) {
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
	})

	t.Run("dry_run_remote_env_runtime_chart_and_image_on_separate_lines", func(t *testing.T) {
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
	})

	t.Run("dry_run_runtime_chart_override_off_a_tenant_umbrella_drops_the_subchart_scope", func(t *testing.T) {
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
	})

	t.Run("dry_run_runtime_chart_without_version_keeps_the_deploy_version", func(t *testing.T) {
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
	})

	t.Run("dry_run_remote_env_runtime_image_without_tag", func(t *testing.T) {
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
	})

	t.Run("dry_run_runtime_image_override_uses_published_chart", func(t *testing.T) {
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
	})

	t.Run("dry_run_runtime_image_override_no_k8s_tree_uses_published_chart", func(t *testing.T) {
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
	})

	t.Run("dry_run_remote_env_no_repo_path_deploys_published_chart", func(t *testing.T) {
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
	})

	t.Run("dry_run_remote_env_values_overlay", func(t *testing.T) {
		// A published-chart deploy has no chart directory to host the
		// operator's values.<env>.yaml overlay; the env config dir's
		// values.yaml is its home. When present, the deploy traces the
		// overlay decision and the helm command carries -f.
		setup := env.New(t)
		fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
		mustWriteFile(t, filepath.Join(setup.ConfigHome, "erun", "team", "dev", "values.yaml"), "claude:\n  model: test-model\n")
		envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_remote_env_values_overlay", normalize.Apply(result.Combined))
	})

	t.Run("default_skips_optin_backend_charts", func(t *testing.T) {
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
	})

	t.Run("components_includes_backend_in_deploy_order", func(t *testing.T) {
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
	})

	t.Run("dry_run_umbrella_component_builds_helm_dependencies", func(t *testing.T) {
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
	})

	t.Run("dry_run_runtime_umbrella_rescopes_set_values", func(t *testing.T) {
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
	})

	t.Run("project_k8s_plan_groups_parallel_step", func(t *testing.T) {
		// When .erun/config.yaml declares a k8s.deployments plan with a
		// parallel-group step (a list as the item), deploy must group those
		// charts into one step and emit a single "step N (parallel): ..."
		// trace line. Other steps stay serial. Order across steps matches
		// the config, not the alphabetical chart-discovery order. The runtime
		// (team-devops) is selected explicitly so the parallel step's two members
		// both deploy (opt-in-only: an unselected runtime would not).
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
		fixture.SeedProjectK8sConfig(t, setup, "environments:\n  dev:\n    k8s:\n      deployments:\n        - [team-devops, erun-backend-postgres]\n        - erun-backend-db\n        - erun-backend-api\n")
		result := erun.Run(t, []string{
			"deploy", "team", "dev",
			"--version", "1.0.0",
			"--components", "team-devops,erun-backend-postgres,erun-backend-db,erun-backend-api",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "deploy/project_k8s_plan_groups_parallel_step", normalize.Apply(result.Combined))
	})

	t.Run("project_k8s_plan_includes_listed_charts_without_components_flag", func(t *testing.T) {
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
	})

	t.Run("project_k8s_plan_rejects_invalid_step_node", func(t *testing.T) {
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
	})

	t.Run("rejects_inconsistent_platform_config", func(t *testing.T) {
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
	})

	t.Run("platform_config_threads_into_helm_set", func(t *testing.T) {
		// A valid `platform:` block flows into every chart's helm command as
		// guarded platform.* --set args, with Resolve's defaults filled in
		// (serviceszone/authhost/nameservers derived from basedomain). Only the
		// platform singletons read them; the runtime chart ignores them. Proves
		// the deploy -> platform-config -> helm wiring end to end, including the
		// resolved auth host reaching the API's trusted-issuer list as
		// api.oidcAllowedIssuers=https://auth.<basedomain> — a platform's control
		// plane trusts its own hosted IdP without an operator patching anything.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 212.93.120.230\n")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "deploy/platform_config_threads_into_helm_set", normalize.Apply(result.Combined))
	})

	t.Run("components_rejects_unknown_name", func(t *testing.T) {
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
	})

	t.Run("default_deploys_runtime_only_not_stray_non_optin_chart", func(t *testing.T) {
		// Opt-in-only: a non-opt-in, non-runtime chart (here team-docs)
		// present in the tree must NOT deploy by default — only the runtime does.
		// Locks the stray-default fix: previously any chart outside the
		// hardcoded opt-in set deployed by elimination (e.g. a disabled docs
		// chart shipped on every deploy).
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDevopsComponentChart(t, setup, "team", "dev", "team-docs")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "deploy/default_deploys_runtime_only_not_stray_non_optin_chart", normalize.Apply(result.Combined))
	})

	t.Run("component_only_tree_bootstraps_published_runtime", func(t *testing.T) {
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
	})

	t.Run("saved_deploy_components_drive_selection_without_flag", func(t *testing.T) {
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
	})

	t.Run("saved_deploy_components_shadowing_plan_reports_what_was_lost", func(t *testing.T) {
		// When a saved deploy.components set wins over a repo
		// k8s.deployments plan that names more, the divergence from the reviewed
		// plan must be visible at normal (non -vv) verbosity, naming exactly what
		// the plan asked for beyond the saved set.
		setup := env.New(t)
		fixture.SeedTenantEnvWithDeployComponents(t, setup, "team", "dev", []string{"erun-backend-postgres"})
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
		fixture.SeedProjectK8sConfig(t, setup, "environments:\n  dev:\n    k8s:\n      deployments:\n        - [team-devops, erun-backend-postgres]\n        - erun-backend-db\n        - erun-backend-api\n")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "deploy/saved_deploy_components_shadowing_plan_reports_what_was_lost", normalize.Apply(result.Combined))
	})

	t.Run("real_run_via_stubs", func(t *testing.T) {
		// Drive the non-dry-run helm/kubectl runners via stub binaries so
		// the deploy execution path (deploy.go's run* helpers, post-helm
		// kubectl waits, helm-recovery branches) gets covered.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "deploy/real_run_via_stubs", normalize.Apply(result.Combined))
	})

	t.Run("real_run_failure_shows_deploy_failed_header_at_default_verbosity", func(t *testing.T) {
		// A report claimed the "==> Deploy failed tenant/env: reason" header
		// (activityDeployFailedLineRe's contract in the desktop) went through
		// ctx.Trace and was invisible below -vv, making the parser dead code.
		// That does not hold against this codebase: common.Context.Trace
		// has aliased Logger.Info (visible at default verbosity; only
		// TraceCommand's raw argv is gated to -vv) since the primitives split in
		// #559, well before this issue was filed. This scenario locks that
		// contract in — the header must appear at default verbosity (no
		// --dry-run, no -v/-vv) on a real helm failure — as a regression guard,
		// not a fix. A whole-output golden is deliberately not used here: the
		// helm stub's stderr capture through cmd.Run()'s buffer is not always
		// flushed by the time the process exits, so the detail after "reason:"
		// is not reliably reproducible; the header itself is.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinaryAdvanced(t, stubs, "helm", fixture.StubBinarySpec{
			Stderr:   "Error: UPGRADE FAILED: post-upgrade hooks failed: timed out waiting for the condition",
			ExitCode: 1,
		})
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a failing helm upgrade, got 0:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "==> Deploy failed team/dev:") {
			t.Fatalf("expected the header at default verbosity (no -vv), got:\n%s", result.Combined)
		}
	})

	t.Run("real_run_persists_runtime_version_and_registry_to_env_config", func(t *testing.T) {
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
	})

	t.Run("dry_run_persisted_version_reopen_uses_runtime_registry_provenance", func(t *testing.T) {
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
	})

	t.Run("dry_run_explicit_version_uses_project_registry_not_provenance", func(t *testing.T) {
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
	})

	t.Run("dry_run_persisted_version_without_provenance_uses_project_registry", func(t *testing.T) {
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
	})

	t.Run("dry_run_with_managed_cloud_traces_helm_set_strings", func(t *testing.T) {
		// Exercises eruncommon.applyCloudProviderDeployMetadata,
		// findCloudContextForKubernetesContext, cloudContextRegionFromName,
		// and the managed-cloud helm --set-string lines that come from
		// per-tenant cloud provider/context resolution.
		setup := env.New(t)
		seedCloudContextConfig(t, setup, "edge")
		root := filepath.Join(setup.ConfigHome, "erun")
		tenantDir := filepath.Join(root, "managed")
		envDir := filepath.Join(tenantDir, "prod")
		for _, dir := range []string{tenantDir, envDir} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", dir, err)
			}
		}
		if err := os.WriteFile(filepath.Join(tenantDir, "config.yaml"),
			[]byte("name: managed\nprojectroot: "+setup.Cwd+"\ndefaultenvironment: prod\n"), 0o644); err != nil {
			t.Fatalf("tenant cfg: %v", err)
		}
		envBody := "name: prod\nrepopath: " + setup.Cwd + "\nkubernetescontext: edge\ncontainerregistry: registry.example/test\nruntimeversion: 1.0.0\nmanagedcloud: true\ncloudprovideralias: dev\n"
		if err := os.WriteFile(filepath.Join(envDir, "config.yaml"), []byte(envBody), 0o644); err != nil {
			t.Fatalf("env cfg: %v", err)
		}
		fixture.SeedDevopsRepo(t, setup, "managed", "prod")
		result := erun.Run(t, []string{"deploy", "managed", "prod", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_with_managed_cloud_traces_helm_set_strings", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_aws_claude_models_traces_set_strings", func(t *testing.T) {
		// Exercises eruncommon.helmClaudeSetArgs' Models + MaxOutputTokens
		// branches and NormalizedModels: claude.usebedrock/models/maxoutputtokens
		// resolve into the runtime chart's claude.useBedrock/availableModels/
		// maxOutputTokens helm --set-string args.
		setup := env.New(t)
		seedCloudContextConfig(t, setup, "edge")
		root := filepath.Join(setup.ConfigHome, "erun")
		tenantDir := filepath.Join(root, "managed")
		envDir := filepath.Join(tenantDir, "prod")
		for _, dir := range []string{tenantDir, envDir} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", dir, err)
			}
		}
		if err := os.WriteFile(filepath.Join(tenantDir, "config.yaml"),
			[]byte("name: managed\nprojectroot: "+setup.Cwd+"\ndefaultenvironment: prod\n"), 0o644); err != nil {
			t.Fatalf("tenant cfg: %v", err)
		}
		envBody := "name: prod\n" +
			"repopath: " + setup.Cwd + "\n" +
			"kubernetescontext: edge\n" +
			"containerregistry: registry.example/test\n" +
			"runtimeversion: 1.0.0\n" +
			"managedcloud: true\n" +
			"cloudprovideralias: dev\n" +
			"claude:\n" +
			"  usebedrock: true\n" +
			"  models: [opus, sonnet, haiku]\n" +
			"  maxoutputtokens: 8192\n"
		if err := os.WriteFile(filepath.Join(envDir, "config.yaml"), []byte(envBody), 0o644); err != nil {
			t.Fatalf("env cfg: %v", err)
		}
		fixture.SeedDevopsRepo(t, setup, "managed", "prod")
		result := erun.Run(t, []string{"deploy", "managed", "prod", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_with_aws_claude_models_traces_set_strings", normalize.Apply(result.Combined))
	})

	t.Run("real_run_helm_pending_recovery_via_auto_recover_env", func(t *testing.T) {
		// Exercises wrapHelmDeployWithReleaseRecovery + the production
		// helm-recovery path: a stubbed `helm` exits with the pending
		// upgrade error message on the first invocation, succeeds on
		// the retry, and exits 0 for the recovery rollback. The new
		// ERUN_AUTO_RECOVER_HELM=1 env var bypasses the interactive
		// confirmation prompt so this can run from the harness.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "docker", "")
		counter := filepath.Join(stubs, "helm-counter")
		fixture.StubBinaryWithScript(t, stubs, "helm", strings.Join([]string{
			`first_arg="$1"`,
			`if [ "$first_arg" = "upgrade" ]; then`,
			`  count=0`,
			`  if [ -f '` + counter + `' ]; then count=$(cat '` + counter + `'); fi`,
			`  count=$((count + 1))`,
			`  printf '%s' "$count" > '` + counter + `'`,
			`  if [ "$count" = "1" ]; then`,
			`    printf '%s\n' "Error: UPGRADE FAILED: another operation (install/upgrade/rollback) is in progress" >&2`,
			`    exit 1`,
			`  fi`,
			`fi`,
			`exit 0`,
		}, "\n"))
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		envVars = append(envVars, "ERUN_AUTO_RECOVER_HELM=1")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/real_run_helm_pending_recovery_via_auto_recover_env", normalize.Apply(result.Combined))
	})

	t.Run("real_run_pod_watch_logs_clean_rollout", func(t *testing.T) {
		// Exercises the in-flight pod watcher started by DeployHelmChart.
		// The kubectl stub returns a pod owned by this helm release with
		// every container Running+Ready, so the watcher prints a single
		// status line and lets helm finish naturally. Locks the dry-run
		// trace's "watching pods" promise to a real-run summary line.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{Stdout: cleanRolloutPodJSON})
		fixture.StubBinaryWithScript(t, stubs, "helm", "sleep 0.5\nexit 0\n")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		envVars = append(envVars, "ERUN_DEPLOY_POD_WATCH_INTERVAL=100ms")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		out := normalize.Apply(result.Combined)
		if !strings.Contains(out, "    pod team-devops-aaaaaa: erun-devops Running (Ready), erun-dind Running (Ready)") {
			t.Fatalf("missing pod-watch summary line in output:\n%s", out)
		}
		if !strings.Contains(out, "==> Deployed team/dev <VERSION>") {
			t.Fatalf("expected clean deploy completion in output:\n%s", out)
		}
	})

	t.Run("real_run_pod_watch_waits_through_image_pull_backoff", func(t *testing.T) {
		// kubectl stub reports a pod with one container in ImagePullBackOff
		// whose message is the generic "Back-off pulling image" (no permanent
		// rejection). A slow/large pull legitimately cycles through this state,
		// so the watcher must NOT abort: it keeps waiting and lets helm finish.
		// helm exits 0 quickly to stand in for the pull eventually succeeding.
		// Proves the "wait while the image is still pulling" contract and the
		// "Pulling image (...)" status labelling.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{Stdout: imagePullBackOffPodJSON})
		fixture.StubBinaryWithScript(t, stubs, "helm", "sleep 0.5\nexit 0\n")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		envVars = append(envVars, "ERUN_DEPLOY_POD_WATCH_INTERVAL=100ms")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("expected clean exit (pull-in-progress must not abort), got %d:\n%s", result.ExitCode, result.Combined)
		}
		out := normalize.Apply(result.Combined)
		if !strings.Contains(out, "    pod team-devops-7d4b4c: erun-dind Pulling image (ImagePullBackOff)") {
			t.Fatalf("missing pull-in-progress status line in output:\n%s", out)
		}
		if strings.Contains(out, "deploy failed early") {
			t.Fatalf("watcher must not abort while the image is still pulling:\n%s", out)
		}
		if !strings.Contains(out, "==> Deployed team/dev <VERSION>") {
			t.Fatalf("expected deploy to complete once helm finished:\n%s", out)
		}
	})

	t.Run("real_run_pod_watch_aborts_on_permanent_image_pull_failure", func(t *testing.T) {
		// kubectl stub reports a container in ErrImagePull whose message is a
		// permanent registry rejection ("manifest unknown") — retrying will
		// never succeed (typo'd/absent tag, bad auth). helm sleeps so the
		// watcher fires first and kills it. Proves the "fail fast on a real
		// failure" half of the pull-aware policy.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{Stdout: permanentImagePullFailurePodJSON})
		fixture.StubBinaryWithScript(t, stubs, "helm", "exec sleep 30\n")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		envVars = append(envVars, "ERUN_DEPLOY_POD_WATCH_INTERVAL=100ms")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit on a permanent pull failure, got 0:\n%s", result.Combined)
		}
		out := normalize.Apply(result.Combined)
		if !strings.Contains(out, "deploy failed early: pod team-devops-7d4b4c container erun-dind ErrImagePull") {
			t.Fatalf("missing structured early-fail error in output:\n%s", out)
		}
		if !strings.Contains(out, "manifest unknown") {
			t.Fatalf("missing permanent pull-failure message in output:\n%s", out)
		}
	})

	t.Run("real_run_pod_watch_aborts_on_crashloop_after_threshold", func(t *testing.T) {
		// kubectl stub reports a CrashLoopBackOff with restartCount above
		// the threshold. The watcher kills helm and surfaces the last
		// terminated message so the user sees why the container is
		// crashing, not just helm's generic timeout.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{Stdout: crashLoopPodJSON})
		fixture.StubBinaryWithScript(t, stubs, "helm", "exec sleep 30\n")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		envVars = append(envVars, "ERUN_DEPLOY_POD_WATCH_INTERVAL=100ms")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
		}
		out := normalize.Apply(result.Combined)
		if !strings.Contains(out, "deploy failed early: pod team-devops-crash container erun-devops CrashLoopBackOff") {
			t.Fatalf("missing structured early-fail error in output:\n%s", out)
		}
		if !strings.Contains(out, "exited with code 137") {
			t.Fatalf("missing last-terminated message in output:\n%s", out)
		}
	})

	t.Run("real_run_pod_watch_waits_out_the_unschedulable_grace_period", func(t *testing.T) {
		// kubectl stub reports a pod stuck PodScheduled=False/Unschedulable on
		// every poll. A brief unschedulable window is normal (the scheduler
		// re-evaluates on cluster changes), so the watcher must not abort on the
		// first observation — only once the grace period elapses. The grace is
		// shrunk via ERUN_DEPLOY_POD_WATCH_UNSCHEDULED_GRACE (the production
		// default is 30s, too slow for a test) rather than asserting a wall-clock
		// sleep tied to that default; see resolveUnscheduledGracePeriod.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{Stdout: unschedulablePodJSON})
		fixture.StubBinaryWithScript(t, stubs, "helm", "exec sleep 30\n")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		envVars = append(envVars, "ERUN_DEPLOY_POD_WATCH_INTERVAL=50ms", "ERUN_DEPLOY_POD_WATCH_UNSCHEDULED_GRACE=150ms")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit once the grace period elapses, got 0:\n%s", result.Combined)
		}
		out := normalize.Apply(result.Combined)
		if !strings.Contains(out, "deploy failed early: pod team-devops-pending Unschedulable") {
			t.Fatalf("missing structured early-fail error naming the pod-level (no container) reason, got:\n%s", out)
		}
		if !strings.Contains(out, "0/1 nodes are available: 1 Insufficient cpu, 1 Insufficient memory") {
			t.Fatalf("missing the scheduler's own message verbatim, got:\n%s", out)
		}
		if !strings.Contains(out, "    pod team-devops-pending: Pending (Unschedulable: 0/1 nodes are available: 1 Insufficient cpu, 1 Insufficient memory)") {
			t.Fatalf("missing the pod-status summary line surfacing the reason during the grace period, got:\n%s", out)
		}
	})

	t.Run("real_run_published_chart_not_found_reports_actionable_error", func(t *testing.T) {
		// Safety net: when the resolved published runtime chart is not pullable
		// at the requested version (a registry that has since evicted the tag, or
		// a race between the probe and the pull), helm upgrade exits non-zero with
		// a registry "not found" on stderr. DeployHelmChart must classify that
		// into a PublishedChartNotFoundError naming the version + registry and
		// pointing at the recovery (deploy a released version / publish first),
		// instead of surfacing a bare "exit status 1". The
		// ERUN_PUBLISHED_CHART_PROBE_OVERRIDE seam confirms erun-devops published
		// so resolution proceeds to the real pull instead of refusing up front;
		// the helm stub then fails that pull with a 404-shaped message, so the
		// failure is isolated to the chart pull, not the search that precedes it.
		setup := env.New(t)
		fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "docker", "")
		// helm errors land on stderr; the stub scans every arg (the real argv
		// carries --install, --wait, … before "upgrade") and fails the upgrade
		// with a registry 404 shape.
		fixture.StubBinaryWithScript(t, stubs, "helm", strings.Join([]string{
			`for a in "$@"; do`,
			`  if [ "$a" = "upgrade" ]; then`,
			`    printf '%s\n' 'Error: failed to perform "FetchReference" on source: registry.example/test/charts/erun-devops: not found' >&2`,
			`    exit 1`,
			`  fi`,
			`done`,
			`exit 0`,
		}, "\n"))
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when the published chart is not found, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/real_run_published_chart_not_found_reports_actionable_error", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_snapshot_version_resets_postgres_database", func(t *testing.T) {
		// Locks the contract under pure deploy: installing a snapshot
		// version resolves ResetDatabase=true (deployResetsDatabase), so the
		// erun-backend-postgres chart's helm upgrade carries
		// --set api.postgres.reset=true. deploy installs by reference (no build);
		// the docker stub answers every `manifest inspect` with exit 0 so the
		// install-existing image-presence check passes without a registry.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
		templates := filepath.Join(setup.Cwd, "team-devops", "k8s", "erun-backend-postgres", "templates")
		if err := os.MkdirAll(templates, 0o755); err != nil {
			t.Fatalf("mkdir templates: %v", err)
		}
		mustWriteFile(t, filepath.Join(templates, "postgres.yaml"), strings.Join([]string{
			"apiVersion: apps/v1",
			"kind: Deployment",
			"spec:",
			"  template:",
			"    spec:",
			"      containers:",
			"        - image: registry.example/team/erun-backend-postgres:18.3 # pinned wrapper",
			"",
		}, "\n"))
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
		result := erun.Run(t, []string{
			"deploy", "team", "dev",
			"--version", "1.0.5-snapshot-20260101000000",
			"--components", "erun-backend-postgres",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_snapshot_version_resets_postgres_database", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_from_chart_cwd_resolves_single_chart_context", func(t *testing.T) {
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
	})

	t.Run("devops_k8s_deploy_component_dry_run", func(t *testing.T) {
		// Exercises the `devops k8s deploy COMPONENT` single-component path:
		// --version wins as the version override, no tenant/env args resolves
		// the default tenant, and the component resolves to its chart under the
		// tenant repo.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"devops", "k8s", "deploy", "team-devops", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/devops_k8s_deploy_component_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("real_run_parallel_step_deploys_charts_concurrently", func(t *testing.T) {
		// Exercises runDeployStep's real-run parallel branch (the goroutine
		// fan-out + errors.Join), which dry-run never takes because dry-run
		// always runs specs serially for stable trace ordering. The project
		// k8s plan groups the runtime chart and erun-backend-postgres into one
		// parallel step; both helm releases deploy via exit-0 stubs. The two
		// goroutines interleave their Info lines nondeterministically, so the
		// whole stream cannot be goldened — assert the deterministic
		// parallel-step trace line and count both rollouts instead.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
		fixture.SeedProjectK8sConfig(t, setup, "environments:\n  dev:\n    k8s:\n      deployments:\n        - [team-devops, erun-backend-postgres]\n")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		out := normalize.Apply(result.Combined)
		if !strings.Contains(out, "deploy: step 1 (parallel): team-devops, erun-backend-postgres") {
			t.Fatalf("expected parallel-step trace line, got:\n%s", out)
		}
		// The runtime chart names only the env, while the non-runtime
		// component names itself after a ` · ` separator so a component
		// rollout is not mistaken for a full-env redeploy. Both rollouts
		// still appear; exactly one of each pair names the component.
		if got := strings.Count(out, "==> Deploying team/dev"); got != 2 {
			t.Fatalf("expected 2 parallel ==> Deploying lines, got %d:\n%s", got, out)
		}
		if got := strings.Count(out, "==> Deploying team/dev · erun-backend-postgres"); got != 1 {
			t.Fatalf("expected the component ==> Deploying line to name the release, got %d:\n%s", got, out)
		}
		if got := strings.Count(out, "==> Deployed team/dev"); got != 2 {
			t.Fatalf("expected 2 ==> Deployed completions, got %d:\n%s", got, out)
		}
		if got := strings.Count(out, "==> Deployed team/dev · erun-backend-postgres"); got != 1 {
			t.Fatalf("expected the component ==> Deployed line to name the release, got %d:\n%s", got, out)
		}
	})

	t.Run("real_run_dedup_skip_when_identical_marker_alive", func(t *testing.T) {
		// Real-run arm of the singleflight skip: a live marker (our own pid)
		// with the identical params hash and a fresh StartedAt makes the
		// second deploy a no-op success without ever invoking helm. The hash
		// comes from a first dry-run against the same resolved spec; the
		// real run must use -vv because the helm argv (and therefore the
		// params hash) embeds verbosity flags and dry-run always traces at
		// raised verbosity — a verbosity-0 real run computes a hash the
		// dry-run probe never reports. The marker must also be recent or the
		// max-age reclaim arm would fire first.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		first := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if first.ExitCode != 0 {
			t.Fatalf("first dry-run exited %d:\n%s", first.ExitCode, first.Combined)
		}
		hash := extractDedupHash(t, first.Combined)
		fixture.SeedDeployInflightMarker(t, setup, "test-context", "team-dev", "team-devops", fixture.DeployInflightRecord{
			PID:         os.Getpid(),
			StartedAt:   time.Now().UTC().Format(time.RFC3339),
			ParamsHash:  hash,
			Tenant:      "team",
			Environment: "dev",
			Version:     "1.0.0",
		})
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/real_run_dedup_skip_when_identical_marker_alive", normalize.Apply(result.Combined))
	})

	t.Run("real_run_dedup_reclaims_aged_marker", func(t *testing.T) {
		// Real-run max-age reclaim arm: the marker's pid is alive (our own),
		// but its StartedAt (the fixture's fixed 2026-05-10 default) is far
		// past the 15-minute deploy ceiling, so the deploy reclaims the
		// marker and proceeds. The reclaim trace embeds the marker's current
		// wall-clock age, which no normalization rule can pin down, so this
		// scenario asserts the stable fragments instead of a whole-stream
		// golden.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDeployInflightMarker(t, setup, "test-context", "team-dev", "team-devops", fixture.DeployInflightRecord{
			PID:         os.Getpid(),
			ParamsHash:  "0000000000000000",
			Tenant:      "team",
			Environment: "dev",
			Version:     "1.0.0",
		})
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		out := normalize.Apply(result.Combined)
		if !strings.Contains(out, "dedup: reclaim (release=test-context-team-dev-team-devops, marker age ") {
			t.Fatalf("expected aged-marker reclaim trace, got:\n%s", out)
		}
		if !strings.Contains(out, "exceeds max 15m0s)") {
			t.Fatalf("expected max-age fragment in reclaim trace, got:\n%s", out)
		}
		if !strings.Contains(out, "==> Deployed team/dev <VERSION>") {
			t.Fatalf("expected deploy to proceed after reclaim, got:\n%s", out)
		}
	})

	t.Run("real_run_dedup_replaces_unreadable_marker", func(t *testing.T) {
		// Real-run unreadable-marker arm: a corrupt marker (truncated JSON)
		// must not block the deploy — the runner traces the replacement,
		// removes the file, claims a fresh marker, and proceeds.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		markerPath := fixture.SeedDeployInflightMarker(t, setup, "test-context", "team-dev", "team-devops", fixture.DeployInflightRecord{
			PID: os.Getpid(),
		})
		if err := os.WriteFile(markerPath, []byte("{"), 0o600); err != nil {
			t.Fatalf("corrupt marker: %v", err)
		}
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/real_run_dedup_replaces_unreadable_marker", normalize.Apply(result.Combined))
	})

	t.Run("real_run_dedup_conflict_on_different_params", func(t *testing.T) {
		// Real-run conflict arm: a live, fresh marker with a different params
		// hash means another deploy with different intent owns the release;
		// the second invocation must fail fast with
		// HelmReleaseConcurrentDeployError instead of stomping the live helm
		// upgrade.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDeployInflightMarker(t, setup, "test-context", "team-dev", "team-devops", fixture.DeployInflightRecord{
			PID:         os.Getpid(),
			StartedAt:   time.Now().UTC().Format(time.RFC3339),
			ParamsHash:  "0000000000000000",
			Tenant:      "team",
			Environment: "dev",
			Version:     "1.0.0-other",
		})
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit on conflicting live deploy, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/real_run_dedup_conflict_on_different_params", normalize.Apply(result.Combined))
	})

	t.Run("real_run_dedup_reclaim_when_marker_pid_dead", func(t *testing.T) {
		// Real-run dead-pid reclaim arm: a fresh marker whose recorded pid is
		// already reaped is leftover state from a crashed deploy; the runner
		// reclaims it and proceeds with the rollout.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDeployInflightMarker(t, setup, "test-context", "team-dev", "team-devops", fixture.DeployInflightRecord{
			PID:         reapedChildPID(t),
			StartedAt:   time.Now().UTC().Format(time.RFC3339),
			ParamsHash:  "0000000000000000",
			Tenant:      "team",
			Environment: "dev",
			Version:     "1.0.0",
		})
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/real_run_dedup_reclaim_when_marker_pid_dead", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_dedup_skip_when_identical_marker_alive", func(t *testing.T) {
		// When another erun deploy is in flight against the same release with
		// the same params hash, dry-run reports "would skip" and exits 0.
		// We seed the marker with our own pid (always alive during this
		// test) and the params hash erun --dry-run will compute on the
		// first run. The second run sees the live identical marker.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		first := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if first.ExitCode != 0 {
			t.Fatalf("first dry-run exited %d:\n%s", first.ExitCode, first.Combined)
		}
		hash := extractDedupHash(t, first.Combined)
		fixture.SeedDeployInflightMarker(t, setup, "test-context", "team-dev", "team-devops", fixture.DeployInflightRecord{
			PID:         os.Getpid(),
			ParamsHash:  hash,
			Tenant:      "team",
			Environment: "dev",
			Version:     "1.0.0",
		})
		second := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if second.ExitCode != 0 {
			t.Fatalf("second dry-run exited %d:\n%s", second.ExitCode, second.Combined)
		}
		out := normalize.Apply(second.Combined)
		if !strings.Contains(out, "dedup: would skip") {
			t.Fatalf("expected 'dedup: would skip' trace, got:\n%s", out)
		}
		if !strings.Contains(out, "==> Skipping team/dev <VERSION> (identical deploy already in progress)") {
			t.Fatalf("expected ==> Skipping info line, got:\n%s", out)
		}
	})

	t.Run("dry_run_dedup_conflict_on_different_params", func(t *testing.T) {
		// A live in-flight deploy with a different params hash should fail
		// the second invocation with HelmReleaseConcurrentDeployError so two
		// callers with conflicting intent surface the conflict instead of
		// stomping on each other's helm release.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDeployInflightMarker(t, setup, "test-context", "team-dev", "team-devops", fixture.DeployInflightRecord{
			PID:         os.Getpid(),
			ParamsHash:  "0000000000000000",
			Tenant:      "team",
			Environment: "dev",
			Version:     "1.0.0-other",
		})
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit on conflicting deploy, got 0:\n%s", result.Combined)
		}
		out := normalize.Apply(result.Combined)
		if !strings.Contains(out, "another erun deploy is in progress") {
			t.Fatalf("expected concurrent-deploy error, got:\n%s", out)
		}
		if !strings.Contains(out, "release \"team-devops\"") {
			t.Fatalf("expected release pointer in error, got:\n%s", out)
		}
	})

	t.Run("dry_run_dedup_reclaim_when_marker_pid_dead", func(t *testing.T) {
		// A leftover marker whose pid is no longer running should not block
		// a fresh dry-run; it reports "would reclaim" so the user can see
		// the stale-state recovery path was taken.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		// Use a real reaped child PID (positive, dead) instead of PID 0.
		// PID 0 short-circuits in isProcessAlive without ever calling
		// Signal(0); a reaped PID forces the live signal-error path that
		// surfaces darwin's os.ErrProcessDone vs. linux's ESRCH. Without
		// that distinction the marker stays "alive" on darwin and deploy
		// is locked out for the full 15-minute max-age fallback.
		deadPID := reapedChildPID(t)
		fixture.SeedDeployInflightMarker(t, setup, "test-context", "team-dev", "team-devops", fixture.DeployInflightRecord{
			PID:         deadPID,
			ParamsHash:  "0000000000000000",
			Tenant:      "team",
			Environment: "dev",
			Version:     "1.0.0",
		})
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("dry-run exited %d (expected 0 since prior pid is dead):\n%s", result.ExitCode, result.Combined)
		}
		out := normalize.Apply(result.Combined)
		if !strings.Contains(out, "dedup: would reclaim") {
			t.Fatalf("expected 'dedup: would reclaim' trace, got:\n%s", out)
		}
	})

	t.Run("dry_run_rethreads_recorded_mcp_auth_public_key", func(t *testing.T) {
		// Regression: a plain version bump of an env whose MCP edge authenticates
		// used to emit no mcpAuth values at all, and since deploy does not
		// --reuse-values the chart defaults turned a publicly reachable,
		// token-authenticated edge (whose `raw` tool runs commands in the pod) into
		// an open one. Auth is now sticky: the env records the key path the last
		// authenticated deploy trusted, and a deploy with no --mcp-auth-public-key
		// rethreads it, rendering the same mcpAuth.* values and re-applying the
		// key Secret.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		keyPath := seedMCPAuthPublicKey(t, setup)
		appendEnvConfig(t, setup, "team", "dev", "mcpauthpublickeypath: "+keyPath+"\n")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_rethreads_recorded_mcp_auth_public_key", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_no_mcp_auth_drops_recorded_key", func(t *testing.T) {
		// Turning authentication off is explicit: --no-mcp-auth resolves no
		// mcpAuth values even though the env has a recorded key, so the rendered
		// release leaves the edge loopback-only. The sibling
		// dry_run_rethreads_recorded_mcp_auth_public_key golden shows the same env
		// keeping auth without the flag.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		keyPath := seedMCPAuthPublicKey(t, setup)
		appendEnvConfig(t, setup, "team", "dev", "mcpauthpublickeypath: "+keyPath+"\n")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-mcp-auth", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_no_mcp_auth_drops_recorded_key", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_refuses_to_downgrade_live_mcp_auth", func(t *testing.T) {
		// The env's MCP auth predates the recorded key (it was enabled by an older
		// desktop deploy), so nothing in the config says "authenticated" — only
		// the live release does. deploy reads it and refuses rather than roll out a
		// release that silently drops authentication, naming the flag that turns it
		// off on purpose. The ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE seam answers the
		// live-release read so the scenario needs no cluster (env.Env() sets it to
		// unknown globally, which is why sibling scenarios never reach helm).
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		envVars := append(setup.Env(), "ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE=enabled")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for a deploy that would strip live MCP auth:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_refuses_to_downgrade_live_mcp_auth", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_no_mcp_auth_clears_live_mcp_auth", func(t *testing.T) {
		// The opt-out is the way past the downgrade guard: with the same
		// live-auth-enabled release as dry_run_refuses_to_downgrade_live_mcp_auth,
		// --no-mcp-auth resolves and rolls out an unauthenticated edge.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		envVars := append(setup.Env(), "ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE=enabled")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-mcp-auth", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_no_mcp_auth_clears_live_mcp_auth", normalize.Apply(result.Combined))
	})

	t.Run("real_run_records_then_clears_mcp_auth_public_key", func(t *testing.T) {
		// Persisting the key path is a real-run side effect outside the captured
		// streams, so it is asserted by reading the env config: an authenticated
		// deploy records the path (which is what makes the next redeploy sticky),
		// and --no-mcp-auth forgets it.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		keyPath := seedMCPAuthPublicKey(t, setup)
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)

		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.1", "--mcp-auth-public-key", keyPath}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if body := readEnvConfig(t, setup, "team", "dev"); !strings.Contains(body, "mcpauthpublickeypath: "+keyPath) {
			t.Fatalf("expected the env config to record mcpauthpublickeypath: %s, got:\n%s", keyPath, body)
		}

		result = erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.2", "--no-mcp-auth"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if body := readEnvConfig(t, setup, "team", "dev"); strings.Contains(body, "mcpauthpublickeypath:") {
			t.Fatalf("expected --no-mcp-auth to clear mcpauthpublickeypath, got:\n%s", body)
		}
	})

	t.Run("real_run_failed_rollout_still_records_mcp_auth_public_key", func(t *testing.T) {
		// Regression: the key was recorded only after the whole deploy succeeded,
		// so a rollout that failed once helm had already applied it left the live
		// release trusting a key the env could not name — and the next deploy's
		// downgrade refusal had nothing to rethread, blocking the very redeploy
		// that would have healed the environment. The key is now recorded where it
		// is applied, so the failed rollout below still leaves the env naming it and
		// the follow-up deploy rethreads it instead of being refused.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		keyPath := seedMCPAuthPublicKey(t, setup)
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "docker", "")
		// A silent failure: the stub's own stderr is inherited by the subprocess
		// and races erun's ordered output, so the golden would not be reproducible
		// if it printed one. The exit code alone drives the failure under test.
		fixture.StubBinaryAdvanced(t, stubs, "helm", fixture.StubBinarySpec{ExitCode: 1})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)

		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.1", "--mcp-auth-public-key", keyPath}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for a failed rollout:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/real_run_failed_rollout_still_records_mcp_auth_public_key", normalize.Apply(result.Combined))
		body := readEnvConfig(t, setup, "team", "dev")
		if !strings.Contains(body, "mcpauthpublickeypath: "+keyPath) {
			t.Fatalf("expected the failed rollout to still record mcpauthpublickeypath: %s, got:\n%s", keyPath, body)
		}
		// The version stays a post-success write: the rollout that would have made
		// it the running one failed.
		if strings.Contains(body, "runtimeversion: 1.0.1") {
			t.Fatalf("expected the failed rollout to leave the recorded runtime version alone, got:\n%s", body)
		}

		// The recovery the record exists for: with the live release still
		// authenticated, the next deploy rethreads the recorded key rather than
		// being refused as a downgrade.
		recovery := append(setup.Env(), "ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE=enabled|file:///etc/erun/mcp-auth/desktopid.pub|team-devops-mcp-auth")
		result = erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.2", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: recovery})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_recovers_after_a_failed_authenticated_rollout", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_refusal_names_the_desktop_identity_key_the_release_trusts", func(t *testing.T) {
		// A release that predates the recorded key can only be recovered by
		// re-supplying the key it already trusts, so the refusal reads that key out
		// of the release's own Secret and — when it is this host's desktop identity
		// — names the path to pass, instead of leaving the operator to work out
		// which file the edge trusts and whether re-supplying it would rotate that
		// trust. The kubectl stub stands in for the Secret read; the live-release
		// answer comes from the ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE seam.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		trustedKey := "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAdesktopidentitydesktopidentitydesktopidenti=\n-----END PUBLIC KEY-----\n"
		identityPath := fixture.SeedDesktopIdentityPublicKey(t, setup, trustedKey)
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", base64.StdEncoding.EncodeToString([]byte(trustedKey)))
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		envVars = append(envVars, "ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE=enabled|file:///etc/erun/mcp-auth/desktopid.pub|team-devops-mcp-auth")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for a deploy that would strip live MCP auth:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_refusal_names_the_desktop_identity_key_the_release_trusts", normalize.Apply(result.Combined))
		// The identity path is collapsed to a token by normalization, so the
		// golden cannot tell one path from another: assert the concrete one the
		// operator would paste against the raw capture.
		if !strings.Contains(result.Combined, "--mcp-auth-public-key "+identityPath) {
			t.Fatalf("expected the refusal to name %s as the key to re-supply, got:\n%s", identityPath, result.Combined)
		}
	})

	t.Run("dry_run_refusal_names_the_secret_when_the_key_is_not_this_host_s", func(t *testing.T) {
		// Same refusal from a host whose desktop identity is a different key: the
		// message names the Secret and the fingerprint instead of a path, because
		// pasting this host's key would rotate the edge's trust rather than restore
		// it.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDesktopIdentityPublicKey(t, setup, "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAanotherhostanotherhostanotherhostanotherho=\n-----END PUBLIC KEY-----\n")
		trustedKey := "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAdesktopidentitydesktopidentitydesktopidenti=\n-----END PUBLIC KEY-----\n"
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", base64.StdEncoding.EncodeToString([]byte(trustedKey)))
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		envVars = append(envVars, "ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE=enabled|file:///etc/erun/mcp-auth/desktopid.pub|team-devops-mcp-auth")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for a deploy that would strip live MCP auth:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_refusal_names_the_secret_when_the_key_is_not_this_host_s", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_refusal_for_an_oidc_release_points_at_the_issuer_not_a_key", func(t *testing.T) {
		// An env whose edge authenticates against the tenant's OIDC issuer holds no
		// local key at all, so telling it to supply --mcp-auth-public-key would send
		// the operator after a file that should not exist. The refusal names the
		// issuer the release trusts and the env setting that lost it. No kubectl
		// stub: this arm never reads the key Secret, and its absence is the point.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		envVars := append(setup.Env(), "ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE=enabled|https://issuer.example/realms/team|")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for a deploy that would strip live MCP auth:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_refusal_for_an_oidc_release_points_at_the_issuer_not_a_key", normalize.Apply(result.Combined))
	})

	t.Run("in_pod_local_agent_runtime_deploy_refused", func(t *testing.T) {
		// Regression: calling a local-agent env's own in-pod MCP deploy resolved
		// every environment-shaping value from the in-pod config projection —
		// default ports, the in-pod mount path as worktreeHostPath, default pod
		// resources, the project's deploy registry for the chart — so the rollout
		// reshaped the env and cut the channel that asked for it. The in-pod
		// marker is the chart-set ERUN_TENANT/ERUN_ENVIRONMENT pair, which the
		// harness sets here to stand in for running inside the pod.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		envVars := append(setup.Env(), "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for an in-pod local-agent runtime deploy:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/in_pod_local_agent_runtime_deploy_refused", normalize.Apply(result.Combined))
	})

	t.Run("in_pod_remote_agent_runtime_deploy_allowed", func(t *testing.T) {
		// The guard is scoped to local-agent envs: a remote-agent env owns its
		// worktree inside the pod, so deploying itself in-pod stays supported.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		envVars := append(setup.Env(), "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev", "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/in_pod_remote_agent_runtime_deploy_allowed", normalize.Apply(result.Combined))
	})

	t.Run("in_pod_local_agent_component_deploy_allowed", func(t *testing.T) {
		// A component chart carries no environment shape, so an in-pod
		// local-agent deploy that selects only components keeps working — the
		// guard fires on the runtime chart alone.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
		envVars := append(setup.Env(), "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--components", "erun-backend-api", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/in_pod_local_agent_component_deploy_allowed", normalize.Apply(result.Combined))
	})

	t.Run("real_run_reports_stale_runtime_port_forward", func(t *testing.T) {
		// Regression: a runtime rollout replaces the pod, which orphans the
		// env's `kubectl port-forward` — the local socket keeps listening while
		// every request fails with EOF, so the MCP endpoint looks broken rather
		// than disconnected, and the deploy said nothing. deploy does not own
		// port-forward lifecycle (`erun open` starts and tracks them), so it
		// reports the condition and names the repair command.
		//
		// The orphan is modelled faithfully: a listener that accepts and closes
		// without answering, exactly what a forward to a deleted pod does. It
		// binds :0 so the scenario never contends for a fixed host port.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		port := startOrphanedPortForwardListener(t)
		seedMCPPortForwardState(t, setup, "team", "dev", port)
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/real_run_reports_stale_runtime_port_forward", normalize.Apply(result.Combined))
	})

	t.Run("real_run_silent_when_env_has_no_tracked_port_forward", func(t *testing.T) {
		// An env nobody opened on this host has no tracked forward, so there is
		// nothing to repair and the deploy stays quiet. Together with
		// real_run_reports_stale_runtime_port_forward this locks both arms; the
		// only difference between the two goldens is the warning.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/real_run_silent_when_env_has_no_tracked_port_forward", normalize.Apply(result.Combined))
	})
}

// seedMCPAuthPublicKey writes a PEM public key inside the scenario's temp tree so
// the path normalizes in goldens.
func seedMCPAuthPublicKey(t *testing.T, setup env.Setup) string {
	t.Helper()
	keyPath := filepath.Join(setup.Home, "desktopid.pub")
	mustWriteFile(t, keyPath, "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAtestkeytestkeytestkeytestkeytestkeytestke=\n-----END PUBLIC KEY-----\n")
	return keyPath
}

func envConfigPathFor(setup env.Setup, tenant, environment string) string {
	return filepath.Join(setup.ConfigHome, "erun", tenant, environment, "config.yaml")
}

func readEnvConfig(t *testing.T, setup env.Setup, tenant, environment string) string {
	t.Helper()
	raw, err := os.ReadFile(envConfigPathFor(setup, tenant, environment))
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	return string(raw)
}

func appendEnvConfig(t *testing.T, setup env.Setup, tenant, environment, body string) {
	t.Helper()
	mustWriteFile(t, envConfigPathFor(setup, tenant, environment), readEnvConfig(t, setup, tenant, environment)+body)
}

// assertEnvConfigContains checks the env config erun persisted. Persisted state
// sits outside the streams the goldens snapshot, so it needs its own assertion.
func assertEnvConfigContains(t *testing.T, setup env.Setup, tenant, environment string, want ...string) {
	t.Helper()
	body := readEnvConfig(t, setup, tenant, environment)
	for _, line := range want {
		if !strings.Contains(body, line) {
			t.Fatalf("expected env config to record %q, got:\n%s", line, body)
		}
	}
}

// assertEnvConfigLacks is the other half: a dry run traces what it would write,
// and the proof that it wrote nothing is on disk, not in the streams.
func assertEnvConfigLacks(t *testing.T, setup env.Setup, tenant, environment string, unwanted ...string) {
	t.Helper()
	body := readEnvConfig(t, setup, tenant, environment)
	for _, line := range unwanted {
		if strings.Contains(body, line) {
			t.Fatalf("expected env config not to record %q, got:\n%s", line, body)
		}
	}
}

// deployStubEnv declares the external binaries a real-run deploy reaches. The
// scenario PATH is scrubbed, so nothing ambient stands in for them; the stubs
// exit 0 so the rollout completes and the post-deploy persist runs.
func deployStubEnv(t *testing.T, setup env.Setup, extra ...string) []string {
	t.Helper()
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	return append(envVars, extra...)
}

// seedMCPPortForwardState records a tracked MCP port-forward for the env, the
// state `erun open` writes when it starts or adopts one.
func seedMCPPortForwardState(t *testing.T, setup env.Setup, tenant, environment string, localPort int) {
	t.Helper()
	path := portForwardStateFile(setup, "mcp", tenant, environment)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	mustWriteFile(t, path, fmt.Sprintf(
		`{"tenant":%q,"environment":%q,"kubernetesContext":"test-context","namespace":"%s-%s","localPort":%d}`,
		tenant, environment, tenant, environment, localPort))
}

// startOrphanedPortForwardListener binds a local port that accepts connections
// and closes them without answering — what a kubectl port-forward to a deleted
// pod does, and the condition deploy must report.
func startOrphanedPortForwardListener(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port
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

// extractDedupHash pulls the live params hash off a "dedup: ready (release=..., hash=<HEX>)"
// line emitted by erun deploy --dry-run -vv. Tests use this to seed an
// identical-hash marker for the dedup-skip path. The raw output is captured
// before normalization so the hash is the real 16-char hex value, not the
// <HASH> placeholder.
func extractDedupHash(t *testing.T, raw string) string {
	t.Helper()
	const marker = "dedup: ready ("
	idx := strings.Index(raw, marker)
	if idx < 0 {
		t.Fatalf("dedup-ready trace not found in:\n%s", raw)
	}
	rest := raw[idx:]
	hashIdx := strings.Index(rest, "hash=")
	if hashIdx < 0 {
		t.Fatalf("hash= field not found in dedup trace:\n%s", rest)
	}
	hashStart := hashIdx + len("hash=")
	end := hashStart
	for end < len(rest) && (isHex(rest[end])) {
		end++
	}
	if end == hashStart {
		t.Fatalf("could not parse hash from dedup trace:\n%s", rest)
	}
	return rest[hashStart:end]
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// reapedChildPID returns the PID of an already-exited child, so isProcessAlive
// hits the real signal(0) error path (ESRCH on linux, os.ErrProcessDone on
// darwin). PID reuse between the wait and the marker read is theoretically
// possible but vanishingly unlikely.
func reapedChildPID(t *testing.T) int {
	t.Helper()
	// The reaped-pid reclaim path is Unix liveness semantics (Signal(0) →
	// ESRCH/os.ErrProcessDone). Windows checks liveness via the process handle
	// and recycles PIDs aggressively, so a freshly reaped PID reads as
	// non-deterministically alive — the reclaim behaviour itself is covered on
	// Unix, so skip the seeding here rather than ship a flaky test.
	if runtime.GOOS == "windows" {
		t.Skip("reaped-pid reclaim relies on Unix signal liveness; Windows PID reuse is non-deterministic")
	}
	// Spawn and reap a real child to get a positive, dead PID.
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatalf("seed reaped child: %v", err)
	}
	pid := cmd.ProcessState.Pid()
	if pid <= 0 {
		t.Fatalf("seed reaped child returned invalid pid %d", pid)
	}
	return pid
}

const cleanRolloutPodJSON = `{
  "items": [
    {
      "metadata": {
        "name": "team-devops-aaaaaa",
        "annotations": {"meta.helm.sh/release-name": "team-devops"}
      },
      "status": {
        "phase": "Running",
        "containerStatuses": [
          {"name": "erun-devops", "ready": true, "restartCount": 0, "state": {"running": {"startedAt": "2026-05-09T12:00:00Z"}}},
          {"name": "erun-dind", "ready": true, "restartCount": 0, "state": {"running": {"startedAt": "2026-05-09T12:00:00Z"}}}
        ]
      }
    }
  ]
}`

const imagePullBackOffPodJSON = `{
  "items": [
    {
      "metadata": {
        "name": "team-devops-7d4b4c",
        "annotations": {"meta.helm.sh/release-name": "team-devops"}
      },
      "status": {
        "phase": "Pending",
        "containerStatuses": [
          {
            "name": "erun-dind",
            "ready": false,
            "restartCount": 0,
            "state": {"waiting": {"reason": "ImagePullBackOff", "message": "Back-off pulling image \"ghcr.io/sophium/erun-dind:1.0.0\""}}
          }
        ]
      }
    }
  ]
}`

const permanentImagePullFailurePodJSON = `{
  "items": [
    {
      "metadata": {
        "name": "team-devops-7d4b4c",
        "annotations": {"meta.helm.sh/release-name": "team-devops"}
      },
      "status": {
        "phase": "Pending",
        "containerStatuses": [
          {
            "name": "erun-dind",
            "ready": false,
            "restartCount": 0,
            "state": {"waiting": {"reason": "ErrImagePull", "message": "rpc error: code = NotFound desc = failed to pull and unpack image \"ghcr.io/sophium/erun-dind:1.0.0\": failed to resolve reference: ghcr.io/sophium/erun-dind:1.0.0: manifest unknown"}}
          }
        ]
      }
    }
  ]
}`

const crashLoopPodJSON = `{
  "items": [
    {
      "metadata": {
        "name": "team-devops-crash",
        "annotations": {"meta.helm.sh/release-name": "team-devops"}
      },
      "status": {
        "phase": "Running",
        "containerStatuses": [
          {
            "name": "erun-devops",
            "ready": false,
            "restartCount": 3,
            "state": {"waiting": {"reason": "CrashLoopBackOff", "message": "back-off 5m restarting failed container"}},
            "lastState": {"terminated": {"reason": "Error", "exitCode": 137, "message": "exited with code 137"}}
          }
        ]
      }
    }
  ]
}`

// unschedulablePodJSON has no containerStatuses at all: the pod was never
// admitted to a node, so the only place its failure reason lives is
// status.conditions.
const unschedulablePodJSON = `{
  "items": [
    {
      "metadata": {
        "name": "team-devops-pending",
        "annotations": {"meta.helm.sh/release-name": "team-devops"}
      },
      "status": {
        "phase": "Pending",
        "conditions": [
          {"type": "PodScheduled", "status": "False", "reason": "Unschedulable", "message": "0/1 nodes are available: 1 Insufficient cpu, 1 Insufficient memory"}
        ]
      }
    }
  ]
}`
