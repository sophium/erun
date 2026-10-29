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
	// Legacy snapshot=true migrates to type=local-agent, so open builds locally.
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
	// --version skips the docker build and pins the helm upgrade to the override.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--version", "9.9.9", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/version_override_skips_local_build", normalize.Apply(result.Combined))
}

func testOpenRuntimeImageOverrideUsesDefaultChart(t *testing.T) {
	t.Parallel()
	// No devops module is seeded, so the embedded default-devops chart is used.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--runtime-image", "ghcr.io/example/custom-runtime", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/runtime_image_override_uses_default_chart", normalize.Apply(result.Combined))
}

func testOpenDefaultTenantEnvironmentResolvesFromRootConfig(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/default_tenant_environment_resolves_from_root_config", normalize.Apply(result.Combined))
}

func testOpenKubectlErrorAssumesNotDeployed(t *testing.T) {
	t.Parallel()
	// A non-NotFound kubectl error in dry run is treated as not deployed.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlGenericError(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/kubectl_error_assumes_not_deployed", normalize.Apply(result.Combined))
}

func testOpenNotInitializedTriggersInitRetry(t *testing.T) {
	t.Parallel()
	// With no config, open runs init in dry run and exits after it.
	setup := env.New(t)
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/not_initialized_triggers_init_retry", normalize.Apply(result.Combined))
}

func testOpenTenantCloudProviderIssuersFlowIntoRuntimeDeploy(t *testing.T) {
	t.Parallel()
	// Two aliases share one issuer to exercise dedup. The tenant chart is seeded
	// because only that path passes oidcAllowedIssuers.
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
	// With no defaultenvironment, init runs with the tenant pre-filled. Declining
	// ends the run after the single prompt readline read-ahead allows.
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
	// A single positional arg is the environment, so the tenant-only case is
	// reachable only via --tenant.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "--tenant", "team", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/tenant_flag_only_resolves_default_env", normalize.Apply(result.Combined))
}

func testOpenEnvironmentPositionalResolvesDefaultTenant(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/environment_positional_resolves_default_tenant", normalize.Apply(result.Combined))
}

func testOpenRemoteRuntimeImageOverride(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--runtime-image", "ghcr.io/example/custom-runtime", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/remote_runtime_image_override", normalize.Apply(result.Combined))
}
