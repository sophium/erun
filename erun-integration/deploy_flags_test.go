package integration

import (
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testDeployVersionRequiredWithoutSwitchErrors(t *testing.T) {
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
}

func testDeployForceFlagAppearsInTrace(t *testing.T) {
	// --force surfaces in the deploy trace so dry-run callers can see
	// the cache-bypass decision, and it propagates to the resolved
	// plan: SkipHelm cannot short-circuit when fingerprint promotion
	// is disabled, so the helm upgrade always runs.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--force", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "deploy/force_flag_appears_in_trace", normalize.Apply(result.Combined))
}

func testDeployDryRunEnvDeployTimeoutOverridesDefault(t *testing.T) {
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
}

func testDeployDryRunRolloutTimeoutFlagOverridesEnvConfig(t *testing.T) {
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
}

func testDeployRolloutTimeoutFlagInvalidDurationErrors(t *testing.T) {
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
}

func testDeployDryRunMaxCPUMemoryStorageFlagsApplyNamespaceQuota(t *testing.T) {
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
}

func testDeployMaxCPUMemoryStorageFlagsPartialSetErrors(t *testing.T) {
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
}

func testDeployEnvDeployTimeoutInvalidDurationErrors(t *testing.T) {
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
}
