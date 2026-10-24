package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testOpenSnapshotEnvConfigDrivesLocalBuild(t *testing.T) {
	t.Parallel()
	// A local env whose config carries the legacy snapshot=true key
	// migrates to type=local-agent on read, so BuildsHere() is true and
	// `erun open` reaches the local-build branch. allowLocalBuilds is
	// derived from the env type (EnvConfig.BuildsHere()), not a flag.
	setup := env.New(t)
	fixture.SeedTenantEnvWithSnapshot(t, setup, "team", "local", true)
	fixture.SeedDevopsRepo(t, setup, "team", "local")
	fixture.SeedDevopsRuntimeDockerfile(t, setup, "team")
	envVars := stubKubectlNotFound(t, setup)
	envVars = append(envVars, stubDockerNoLocalImages(t, setup)...)
	result := erun.Run(t, []string{"open", "team", "local", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/snapshot_env_config_drives_local_build", normalize.Apply(result.Combined))
}

func testOpenVersionOverrideSkipsLocalBuild(t *testing.T) {
	t.Parallel()
	// --version pins the runtime chart to a specific version; the
	// builder branch must be skipped (no docker build) and the helm
	// upgrade must reference the override version explicitly.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--version", "9.9.9", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/version_override_skips_local_build", normalize.Apply(result.Combined))
}

func testOpenRuntimeImageOverrideUsesDefaultChart(t *testing.T) {
	t.Parallel()
	// --runtime-image rewrites the runtime release to use the
	// embedded default-devops chart with the chosen image. There is
	// no local devops module seeded, so the path exercises
	// applyRuntimeDeployImageOverride's fallback to the embedded
	// chart.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--runtime-image", "ghcr.io/example/custom-runtime", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/runtime_image_override_uses_default_chart", normalize.Apply(result.Combined))
}

func testOpenDefaultTenantEnvironmentResolvesFromRootConfig(t *testing.T) {
	t.Parallel()
	// `erun open` without args must pick up defaulttenant +
	// defaultenvironment from $XDG_CONFIG_HOME/erun. Exercises
	// resolveOpenParams' "no args" branch and OpenParamsForArgs.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/default_tenant_environment_resolves_from_root_config", normalize.Apply(result.Combined))
}

func testOpenKubectlErrorAssumesNotDeployed(t *testing.T) {
	t.Parallel()
	// kubectl stub exits non-zero with a non-NotFound error. In
	// dry-run, shouldDeployRuntime traces "assuming not deployed" and
	// proceeds with the helm upgrade. Locks the dry-run fallback in
	// shouldDeployRuntime (open.go:407-410).
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlGenericError(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/kubectl_error_assumes_not_deployed", normalize.Apply(result.Combined))
}

func testOpenNotInitializedTriggersInitRetry(t *testing.T) {
	t.Parallel()
	// `erun open team dev` with no config triggers
	// resolveOpenWithInitStopForParams' init-fired branch:
	// resolveOpen errors with ErrNotInitialized, shouldRunInitForOpenCommand
	// matches, runInitBeforeOpenForParams runs init in dry-run, and the
	// command exits via initRan=true (open.go:226-230). The init dry-run
	// trace is captured in the golden alongside open's audit line.
	setup := env.New(t)
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/not_initialized_triggers_init_retry", normalize.Apply(result.Combined))
}

func testOpenTenantCloudProviderIssuersFlowIntoRuntimeDeploy(t *testing.T) {
	t.Parallel()
	// Exercises ResolveTenantCloudProviderIssuers +
	// CloudProviderOIDCIssuerURL: when the tenant config names cloud
	// provider aliases and the root config carries their OIDC issuer
	// URLs, the resolved runtime helm plan must pass the deduplicated
	// issuer list via api.oidcAllowedIssuers (visible in the dry-run
	// helm trace). Two aliases share one issuer to lock the dedup
	// branch. The tenant devops chart is seeded because only the
	// tenant-chart spec path runs configureDeployInputMetadata — the
	// materialized default-chart path skips it and would leave the
	// issuer list empty.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	root := filepath.Join(setup.ConfigHome, "erun")
	mustWrite(t, filepath.Join(root, "config.yaml"),
		"defaulttenant: team\n"+
			"cloudproviders:\n"+
			"  - alias: alice+123456789012@aws\n"+
			"    provider: aws\n"+
			"    username: alice\n"+
			"    accountid: \"123456789012\"\n"+
			"    oidcissuerurl: https://oidc.eu-west-2.amazonaws.com/team-issuer\n"+
			"  - alias: bob+123456789012@aws\n"+
			"    provider: aws\n"+
			"    username: bob\n"+
			"    accountid: \"123456789012\"\n"+
			"    oidcissuerurl: https://oidc.eu-west-2.amazonaws.com/team-issuer\n",
	)
	mustWrite(t, filepath.Join(root, "team", "config.yaml"),
		"projectroot: "+setup.Cwd+"\n"+
			"name: team\n"+
			"defaultenvironment: dev\n"+
			"cloudprovideraliases:\n"+
			"  - alice+123456789012@aws\n"+
			"  - bob+123456789012@aws\n",
	)
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/tenant_cloud_provider_issuers_flow_into_runtime_deploy", normalize.Apply(result.Combined))
}

func testOpenDefaultEnvNotConfiguredRunsInitWithTenant(t *testing.T) {
	t.Parallel()
	// `erun open` (no args) against a tenant whose config lacks
	// defaultenvironment: resolveOpen fails with
	// ErrDefaultEnvironmentNotConfigured, the init-retry path resolves
	// init params via initParamsForOpenDefaults — loadOpenDefaultTenant
	// succeeds, loadOpenDefaultEnvironment reports not-configured — and
	// init runs in dry-run with the tenant pre-filled. Locks the
	// tenant-only fallback arm that the fully-seeded scenarios skip.
	// The handed-off init asks to initialize the default environment;
	// declining ("n") ends the run after that single prompt (readline
	// read-ahead allows no second prompt) with init's cancellation
	// message.
	setup := env.New(t)
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(filepath.Join(root, "team"), 0o755); err != nil {
		t.Fatalf("mkdir tenant config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("defaulttenant: team\n"), 0o644); err != nil {
		t.Fatalf("write root config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "team", "config.yaml"),
		[]byte("projectroot: "+setup.Cwd+"\nname: team\n"), 0o644); err != nil {
		t.Fatalf("write tenant config: %v", err)
	}
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars, Stdin: "n\n"})
	golden.Equal(t, "open/default_env_not_configured_runs_init_with_tenant", normalize.Apply(result.Combined))
}

func testOpenTenantFlagOnlyResolvesDefaultEnv(t *testing.T) {
	t.Parallel()
	// `erun open --tenant team` (flag, no positional args) lands in
	// resolveOpenParams' "tenant set, environment empty" switch case
	// (open.go:172-174), with UseDefaultEnvironment=true so the env
	// resolves from the tenant config. OpenParamsForArgs treats a
	// single positional arg as the *environment*, not the tenant, so
	// the tenant-only branch is only reachable via the --tenant flag.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "--tenant", "team", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/tenant_flag_only_resolves_default_env", normalize.Apply(result.Combined))
}

func testOpenEnvironmentPositionalResolvesDefaultTenant(t *testing.T) {
	t.Parallel()
	// `erun open dev` (single positional arg) lands in
	// resolveOpenParams' "tenant empty, environment set" switch case
	// (open.go:169-171), with UseDefaultTenant=true so the tenant
	// resolves from the root config's defaulttenant. Locks the second
	// switch case which existing 0-arg and 2-arg scenarios skip.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/environment_positional_resolves_default_tenant", normalize.Apply(result.Combined))
}

func testOpenRemoteRuntimeImageOverride(t *testing.T) {
	t.Parallel()
	// Remote env + --runtime-image rewrites the runtime release to
	// use the embedded default-devops chart with the chosen image.
	// Locks the RemoteRepo() branch in applyRuntimeDeployImageOverride
	// (open.go:602-603), which the local-env runtime_image scenario
	// does not reach.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--runtime-image", "ghcr.io/example/custom-runtime", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/remote_runtime_image_override", normalize.Apply(result.Combined))
}
