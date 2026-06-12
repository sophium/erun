package integration

import (
	"os"
	"os/exec"
	"path/filepath"
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
		// Regression for commit a7b4d08: when cwd has no devops context, the
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

	t.Run("dry_run_no_devops_module_errors", func(t *testing.T) {
		// Exercises the no-candidates end of chart discovery: a local env
		// whose project root has no *-devops module anywhere must fail with
		// "helm chart not found in current directory".
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when no devops module exists, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_no_devops_module_errors", normalize.Apply(result.Combined))
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
		result := erun.Run(t, []string{"deploy", "team", "prod", "--version", "1.0.0", "--no-snapshot", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
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
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-snapshot", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_tenant_aliases_resolve_oidc_issuers", normalize.Apply(result.Combined))
	})

	t.Run("real_run_remote_env_embedded_chart_via_stubs", func(t *testing.T) {
		// Real-run deploy of a remote env with no local checkout: the
		// embedded default-devops chart must be materialized into a temp
		// dir (prepareHelmChartForDeploy + copyDirectory) before helm
		// upgrade runs against it. The helm/kubectl stubs exit 0 so the
		// rollout completes; dry-run cannot reach the materialization —
		// it short-circuits before the filesystem copy.
		setup := env.New(t)
		fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/real_run_remote_env_embedded_chart_via_stubs", normalize.Apply(result.Combined))
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
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-snapshot"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/real_run_preflight_starts_stopped_cloud_context", normalize.Apply(result.Combined))
	})

	t.Run("publish_traces_helm_package_and_push_before_upgrade", func(t *testing.T) {
		// --publish runs `helm package` then `helm push oci://<registry>` in
		// the chart's parent directory before the helm upgrade. The trace
		// must show the real commands so dry-run is auditable; both must
		// appear in the resolved spec ahead of the helm upgrade line.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedProjectK8sConfig(t, setup, "containerregistry: registry.example/test\n")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--publish", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/publish_traces_helm_package_and_push_before_upgrade", normalize.Apply(result.Combined))
	})

	t.Run("publish_real_run_invokes_helm_package_and_push_via_stubs", func(t *testing.T) {
		// Real-run path: --publish without --dry-run exercises the
		// runHelmCommand exec branch in helm_chart_publish.go. The helm
		// stub exits 0 for any argv so package + push both succeed, and
		// the deploy still drives helm upgrade after the publish step.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedProjectK8sConfig(t, setup, "containerregistry: registry.example/test\n")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--publish", "--no-snapshot"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		out := normalize.Apply(result.Combined)
		if !strings.Contains(out, "==> Publishing team-devops <VERSION> to oci://registry.example/test") {
			t.Fatalf("expected ==> Publishing info line in output:\n%s", out)
		}
		if !strings.Contains(out, "==> Deployed team/dev <VERSION>") {
			t.Fatalf("expected clean deploy completion after publish, got:\n%s", out)
		}
	})

	t.Run("publish_without_container_registry_errors", func(t *testing.T) {
		// --publish requires a container registry to derive the OCI repo.
		// When the project has none configured, resolution must fail with a
		// clear, actionable error rather than tracing a half-built command
		// or pushing to an empty target.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--publish", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when --publish has no container registry, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/publish_without_container_registry_errors", normalize.Apply(result.Combined))
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

	t.Run("dry_run_outside_devops_with_tenant_env", func(t *testing.T) {
		// Regression for issue #252: when erun deploy <tenant> <env> is
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

	t.Run("dry_run_remote_env_uses_embedded_chart", func(t *testing.T) {
		// Regression: a remote env (Remote=true) has its repopath on the
		// remote host's filesystem (e.g. proxmox1: /home/erun/git/erun) and
		// has no local checkout at all. Deploy from any cwd must still
		// work: the embedded default-devops chart is materialized to a
		// temp dir and used for the helm install. Pre-fix, deploy stat'd
		// the remote repopath locally and failed with
		// "open <remote-path>: no such file or directory".
		setup := env.New(t)
		fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
		// Note: no SeedDevopsRepo — there is no local checkout anywhere.
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: setup.Env()})
		golden.Equal(t, "deploy/dry_run_remote_env_uses_embedded_chart", normalize.Apply(result.Combined))
	})

	t.Run("default_skips_optin_backend_charts", func(t *testing.T) {
		// Regression for issue #271: when a tenant repo contains the runtime
		// chart and the three opt-in backend charts, `erun deploy` without
		// --components must deploy only the runtime chart. The backend
		// charts ship as separate Helm releases and are gated behind the
		// --components flag.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "deploy/default_skips_optin_backend_charts", normalize.Apply(result.Combined))
	})

	t.Run("components_includes_backend_in_deploy_order", func(t *testing.T) {
		// With --components, the opt-in backend charts must deploy in the
		// fixed dependency order (postgres -> db -> api -> runtime),
		// regardless of the order they appear on the command line.
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

	t.Run("project_k8s_plan_groups_parallel_step", func(t *testing.T) {
		// When .erun/config.yaml declares a k8s.deployments plan with a
		// parallel-group step (a list as the item), deploy must group those
		// charts into one step and emit a single "step N (parallel): ..."
		// trace line. Other steps stay serial. Order across steps matches
		// the config, not the alphabetical chart-discovery order.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
		fixture.SeedProjectK8sConfig(t, setup, "environments:\n  dev:\n    k8s:\n      deployments:\n        - [team-devops, erun-backend-postgres]\n        - erun-backend-db\n        - erun-backend-api\n")
		result := erun.Run(t, []string{
			"deploy", "team", "dev",
			"--version", "1.0.0",
			"--components", "erun-backend-postgres,erun-backend-db,erun-backend-api",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "deploy/project_k8s_plan_groups_parallel_step", normalize.Apply(result.Combined))
	})

	t.Run("project_k8s_plan_includes_listed_charts_without_components_flag", func(t *testing.T) {
		// Listing a chart under environments.<env>.k8s.deployments must
		// imply --components for it: a user who has configured the plan
		// should not also have to pass --components=erun-backend-... on
		// every deploy. Without this, the opt-in filter would silently
		// strip the backend charts even though the plan named them.
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

	t.Run("snapshot_conflict_errors", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--snapshot", "--no-snapshot", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for conflicting snapshot flags, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/snapshot_conflict_errors", normalize.Apply(result.Combined))
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
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-snapshot"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "deploy/real_run_via_stubs", normalize.Apply(result.Combined))
	})

	t.Run("real_run_persists_runtime_version_and_registry_to_env_config", func(t *testing.T) {
		// Regression: `erun deploy --version X` updates helm's release
		// appVersion but used to leave EnvConfig.RuntimeVersion at the
		// previously persisted value, so the desktop runtime dialog and
		// `erun list` kept showing the stale string. Real-run deploy now
		// writes the deployed version back to the env config.
		//
		// Issue #363 extends this: the source registry is persisted
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
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.99", "--no-snapshot"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
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
		// Issue #363: when the env has a persisted (RuntimeVersion,
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
		result := erun.Run(t, []string{"deploy", "team", "dev", "--no-snapshot", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_persisted_version_reopen_uses_runtime_registry_provenance", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_explicit_version_uses_project_registry_not_provenance", func(t *testing.T) {
		// Issue #363: an explicit --version is a fresh deploy intent.
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
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.5", "--no-snapshot", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_explicit_version_uses_project_registry_not_provenance", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_persisted_version_without_provenance_uses_project_registry", func(t *testing.T) {
		// Issue #363: legacy envs persisted by older binaries have
		// runtimeversion but no runtimeregistry. On reopen we must NOT
		// invent a provenance — fall back to the project's current
		// containerregistry, the same behaviour callers had before the
		// field existed. The next successful deploy backfills the pair.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedProjectK8sConfig(t, setup, "containerregistry: registry.example/current\n")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--no-snapshot", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_persisted_version_without_provenance_uses_project_registry", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_managed_cloud_traces_helm_set_strings", func(t *testing.T) {
		// Exercises eruncommon.applyCloudProviderDeployMetadata,
		// findCloudContextForKubernetesContext, cloudContextRegionFromName,
		// and the managed-cloud helm --set-string lines that come from
		// per-tenant cloud provider/context resolution. Seeds an env with
		// managedcloud=true, cloudprovideralias=dev, and a matching cloud
		// context that points at the same kubernetes context.
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
		result := erun.Run(t, []string{"deploy", "managed", "prod", "--version", "1.0.0", "--no-snapshot", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_with_managed_cloud_traces_helm_set_strings", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_aws_claude_models_traces_set_strings", func(t *testing.T) {
		// Exercises eruncommon.helmClaudeSetArgs Models + MaxOutputTokens
		// branches and EnvironmentClaudeConfig.NormalizedModels. With
		// claude.usebedrock=true, claude.models=[opus,sonnet,haiku] and
		// claude.maxoutputtokens=8192 set on the env, the resolved helm
		// command must include --set-string claude.useBedrock=1,
		// --set-string claude.availableModels=opus,sonnet,haiku and
		// --set-string claude.maxOutputTokens=8192.
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
		result := erun.Run(t, []string{"deploy", "managed", "prod", "--version", "1.0.0", "--no-snapshot", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
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
		// Counter-driven helm stub: print the pending-operation message
		// on the first `upgrade --install` call, exit 0 on every other
		// invocation (rollback recovery + retry upgrade).
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
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-snapshot"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
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
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-snapshot"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
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

	t.Run("real_run_pod_watch_aborts_on_image_pull_backoff", func(t *testing.T) {
		// kubectl stub reports a pod with one container in
		// ImagePullBackOff. helm sleeps so the watcher fires first and
		// kills it. Locks the structured early-fail error message.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{Stdout: imagePullBackOffPodJSON})
		fixture.StubBinaryWithScript(t, stubs, "helm", "exec sleep 30\n")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		envVars = append(envVars, "ERUN_DEPLOY_POD_WATCH_INTERVAL=100ms")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-snapshot"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
		}
		out := normalize.Apply(result.Combined)
		if !strings.Contains(out, "    pod team-devops-7d4b4c: erun-dind Waiting (ImagePullBackOff)") {
			t.Fatalf("missing pod-watch summary line in output:\n%s", out)
		}
		if !strings.Contains(out, `deploy failed early: pod team-devops-7d4b4c container erun-dind ImagePullBackOff: Back-off pulling image "ghcr.io/sophium/erun-dind:<VERSION>"`) {
			t.Fatalf("missing structured early-fail error in output:\n%s", out)
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
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-snapshot"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
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

	t.Run("dry_run_local_docker_cwd_uses_current_build_for_owning_chart", func(t *testing.T) {
		// Exercises resolveCurrentDockerComponentBuildForDeploy,
		// deployContextOwnsDockerBuild, and resolveDeploySpecForCurrentDockerBuild:
		// when deploy runs from a devops docker build dir
		// (<repo>/team-devops/docker/team-devops) in the local environment
		// with --snapshot, the cwd Dockerfile is resolved as the "current
		// build", the owning chart claims it (the build dir sits inside the
		// chart's module root), and the resolved spec pins that image via an
		// imageOverrides entry instead of resolving a separate component
		// build. The snapshot version is minted once (freezeNow) so build
		// tag, helm appVersion, and the override stay identical.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "local")
		fixture.SeedDevopsRepo(t, setup, "team", "local")
		fixture.SeedDevopsRuntimeDockerfile(t, setup, "team")
		dockerDir := filepath.Join(setup.Cwd, "team-devops", "docker", "team-devops")
		result := erun.Run(t, []string{"deploy", "--snapshot", "--dry-run"}, erun.RunOptions{Cwd: dockerDir, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_local_docker_cwd_uses_current_build_for_owning_chart", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_snapshot_chart_image_scan_resolves_additional_builds", func(t *testing.T) {
		// Exercises the chart image scan that discovers additional docker
		// builds for a deploy: findDockerImagesInChart walks the chart's
		// templates, dockerImageFromChartLine / chartImageValue /
		// chartTemplateImageValue parse the four template shapes seeded
		// below (plain pinned image with trailing comment, quoted
		// {{ .Chart.AppVersion }} tag, printf "%s/name:%s" template, and a
		// fully dynamic {{ .Values }} reference that must be rejected), and
		// resolveAdditionalDockerBuildsForDeploy maps the survivors onto
		// docker build contexts under <tenant>-devops/docker/. The extra
		// image's Dockerfile FROMs a locally-buildable base image so
		// resolveDockerfileBaseImageBuilds prepends the base build, and
		// orderedDockerBuildSpecs places it before its consumer.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		templates := filepath.Join(setup.Cwd, "team-devops", "k8s", "team-devops", "templates")
		if err := os.MkdirAll(templates, 0o755); err != nil {
			t.Fatalf("mkdir templates: %v", err)
		}
		mustWriteFile(t, filepath.Join(templates, "deployment.yaml"), strings.Join([]string{
			"apiVersion: apps/v1",
			"kind: Deployment",
			"spec:",
			"  template:",
			"    spec:",
			"      containers:",
			"        - image: registry.example/test/extra:1.2.3 # chart-pinned helper",
			`        - image: "registry.example/test/worker:{{ .Chart.AppVersion }}"`,
			`        {{- $sidecar := printf "%s/sidecar:%s" .Values.registry .Chart.AppVersion }}`,
			"        - image: {{ .Values.dynamicImage }}",
			"",
		}, "\n"))
		for name, dockerfile := range map[string]string{
			"extra":  "FROM registry.example/test/base:9.9.9\n",
			"worker": "FROM alpine:3.22\n",
			"base":   "FROM alpine:3.22\n",
		} {
			dir := filepath.Join(setup.Cwd, "team-devops", "docker", name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", dir, err)
			}
			mustWriteFile(t, filepath.Join(dir, "Dockerfile"), dockerfile)
		}
		result := erun.Run(t, []string{"deploy", "team", "dev", "--snapshot", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_snapshot_chart_image_scan_resolves_additional_builds", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_snapshot_postgres_reset_forces_helm_despite_cached_images", func(t *testing.T) {
		// Locks the #506 fix: a snapshot deploy resolves ResetDatabase=true,
		// and the erun-backend-postgres chart must then run its helm upgrade
		// even when every locally-built image was promoted from the
		// fingerprint cache (resolveDeploySkipHelm's reset branch) — the
		// reset rides in the chart, so skipping helm would drop it. The
		// docker stub is decision input: it answers every `image inspect`
		// with exit 0 so all fp-tags appear present and the builds promote;
		// without it the promotion branch depends on the host's docker
		// state. The expected trace shows the forced-helm decision line and
		// the helm upgrade with --set api.postgres.reset=true.
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
		dockerDir := filepath.Join(setup.Cwd, "team-devops", "docker", "erun-backend-postgres")
		if err := os.MkdirAll(dockerDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dockerDir, err)
		}
		mustWriteFile(t, filepath.Join(dockerDir, "Dockerfile"), "FROM alpine:3.22\n")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
		result := erun.Run(t, []string{
			"deploy", "team", "dev",
			"--snapshot",
			"--components", "erun-backend-postgres",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_snapshot_postgres_reset_forces_helm_despite_cached_images", normalize.Apply(result.Combined))
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
		// newK8sDeployCmd -> ResolveDeploySpec -> resolveDeployVersionOverride
		// (the --version flag wins) -> resolveDeployTarget's default-tenant
		// branch (no tenant/env args) -> resolveDeployContextForTarget's
		// component branch (findComponentHelmChartPath under the tenant repo).
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"devops", "k8s", "deploy", "team-devops", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/devops_k8s_deploy_component_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("real_run_skiphelm_heals_persisted_version_from_deployed_release", func(t *testing.T) {
		// Exercises the cached-deploy heal path (issue #475):
		// PersistRuntimeVersionFromDeploySpecs' SkipHelm arm,
		// resolveRunningRuntimeVersion, and ResolveDeployedHelmReleaseVersion.
		// The docker stub answers every `image inspect` with exit 0 so all
		// fp-tags appear present, the snapshot build promotes, and the spec
		// resolves SkipHelm=true — RunDeploySpec skips both the build and the
		// helm upgrade. The persist step then reads the version the cluster
		// actually runs via the helm stub's `get metadata` JSON and heals
		// EnvConfig.RuntimeVersion to it instead of recording the never-pushed
		// snapshot mint. The stubs are decision input for the real-run flow;
		// no real daemon or cluster is touched.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "local")
		fixture.SeedDevopsRepo(t, setup, "team", "local")
		fixture.SeedDevopsRuntimeDockerfile(t, setup, "team")
		// The deploy spec's container registry comes from the project config
		// (.erun/config.yaml), not the env config; seed it so the heal also
		// persists RuntimeRegistry provenance alongside the version.
		fixture.SeedProjectK8sConfig(t, setup, "containerregistry: registry.example/published\n")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "docker", "")
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinaryWithScript(t, stubs, "helm", strings.Join([]string{
			`case "$1 $2" in`,
			`  "get metadata") printf '{"appVersion":"1.2.3"}\n' ;;`,
			`esac`,
			`exit 0`,
		}, "\n"))
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "kubectl", "helm")...)
		result := erun.Run(t, []string{"deploy", "team", "local", "--snapshot"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "deploy/real_run_skiphelm_heals_persisted_version_from_deployed_release", normalize.Apply(result.Combined))
		// The healed version lives in the env config file, outside the
		// captured streams, so assert the side effect directly.
		raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "team", "local", "config.yaml"))
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		body := string(raw)
		if !strings.Contains(body, "runtimeversion: 1.2.3") {
			t.Fatalf("expected env config healed to runtimeversion: 1.2.3, got:\n%s", body)
		}
		if !strings.Contains(body, "runtimeregistry: registry.example/published") {
			t.Fatalf("expected env config to record runtimeregistry: registry.example/published, got:\n%s", body)
		}
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
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-snapshot"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		out := normalize.Apply(result.Combined)
		if !strings.Contains(out, "deploy: step 1 (parallel): team-devops, erun-backend-postgres") {
			t.Fatalf("expected parallel-step trace line, got:\n%s", out)
		}
		if got := strings.Count(out, "==> Deploying team/dev <VERSION>"); got != 2 {
			t.Fatalf("expected 2 parallel ==> Deploying lines, got %d:\n%s", got, out)
		}
		if got := strings.Count(out, "==> Deployed team/dev <VERSION> in <ELAPSED>"); got != 2 {
			t.Fatalf("expected 2 ==> Deployed completions, got %d:\n%s", got, out)
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
		first := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-snapshot", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
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
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-snapshot", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
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
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-snapshot"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
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
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-snapshot"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
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
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-snapshot"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
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
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-snapshot"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
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
		// First dry-run: capture the params hash from "dedup: ready (..., hash=<HASH>)".
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

// reapedChildPID spawns a short-lived child, waits for it to exit, and
// returns its now-dead PID. Calling isProcessAlive against this PID
// exercises the real signal(0) error path: ESRCH on linux,
// os.ErrProcessDone on darwin. PID reuse is theoretically possible but
// vanishingly unlikely between the wait return and the marker read.
func reapedChildPID(t *testing.T) int {
	t.Helper()
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
