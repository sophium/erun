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

// stubKubectlStopRunState routes kubectl to a stub that answers the runtime
// run-state read with the given replica counts. That answer is the decision
// input for stop — a trace alone cannot say whether the environment is already
// scaled to zero — so every scenario below pins it explicitly rather than
// letting whatever kubectl sits on the developer's PATH drive the branch.
// (open's sibling helper additionally stubs the adopt probes it needs.)
func stubKubectlStopRunState(t *testing.T, setup env.Setup, desired, ready int) []string {
	t.Helper()
	stubs := setup.Cwd + "/stubs"
	fixture.StubKubectlRuntimeRunState(t, stubs, desired, ready)
	return append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
}

func readSeededEnvConfig(t *testing.T, setup env.Setup, tenant, environment string) string {
	t.Helper()
	path := filepath.Join(setup.ConfigHome, "erun", tenant, environment, "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read env config %s: %v", path, err)
	}
	return string(data)
}

func TestStop(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"stop", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "stop/help", normalize.Apply(result.Combined))
	})

	// A running environment is the main case: the plan must show the scale to
	// zero AND the recorded intent, because the scale patch alone is drift helm
	// reverts on the next upgrade.
	t.Run("dry_run_running_environment", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlStopRunState(t, setup, 1, 1)
		result := erun.Run(t, []string{"stop", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "stop/dry_run_running_environment", normalize.Apply(result.Combined))
	})

	// An environment already scaled to zero needs no scale call, but still needs
	// the intent recorded when the config does not carry it yet — otherwise the
	// next deploy silently brings it back.
	t.Run("dry_run_already_scaled_to_zero", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlStopRunState(t, setup, 0, 0)
		result := erun.Run(t, []string{"stop", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "stop/dry_run_already_scaled_to_zero", normalize.Apply(result.Combined))
	})

	// Fully stopped — scaled to zero and the intent already recorded — is the
	// complete no-op: no scale, no config write.
	t.Run("dry_run_already_stopped_is_a_no_op", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedStoppedTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlStopRunState(t, setup, 0, 0)
		result := erun.Run(t, []string{"stop", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "stop/dry_run_already_stopped_is_a_no_op", normalize.Apply(result.Combined))
	})

	// The scope-defaulted form resolves the same target as `open`/`deploy` do,
	// so an operator inside a tenant project needs no arguments.
	t.Run("dry_run_defaults_to_current_scope", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlStopRunState(t, setup, 1, 1)
		result := erun.Run(t, []string{"stop", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "stop/dry_run_defaults_to_current_scope", normalize.Apply(result.Combined))
	})

	// --tenant/--environment target another environment, matching deploy/open.
	t.Run("dry_run_target_flags", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedTenantEnv(t, setup, "team", "prod")
		envVars := stubKubectlStopRunState(t, setup, 1, 1)
		result := erun.Run(t, []string{"stop", "--tenant", "team", "--environment", "prod", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "stop/dry_run_target_flags", normalize.Apply(result.Combined))
	})

	// Nothing to stop is an explicit error, not a silent success that leaves the
	// operator believing capacity was freed.
	t.Run("not_deployed_fails_informatively", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{
			Stderr:   `Error from server (NotFound): deployments.apps "team-devops" not found`,
			ExitCode: 1,
		})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"stop", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for an undeployed runtime: %s", result.Combined)
		}
		golden.Equal(t, "stop/not_deployed_fails_informatively", normalize.Apply(result.Combined))
	})

	// Real run: the stop is durable. The snapshot cannot see the config file, so
	// the persisted `stopped: true` is asserted directly — the side effect is
	// outside the captured streams.
	t.Run("real_run_records_stop_intent", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlStopRunState(t, setup, 1, 1)
		result := erun.Run(t, []string{"stop", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "stop/real_run_records_stop_intent", normalize.Apply(result.Combined))
		if config := readSeededEnvConfig(t, setup, "team", "dev"); !strings.Contains(config, "stopped: true") {
			t.Fatalf("stop did not record the intent on the env config:\n%s", config)
		}
	})

	// --output json is the orchestrator-facing result; the human summary must
	// not corrupt the payload on stdout.
	t.Run("real_run_output_json", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlStopRunState(t, setup, 1, 1)
		result := erun.Run(t, []string{"stop", "team", "dev", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "stop/real_run_output_json", normalize.Apply(result.Stdout))
	})

	// Stopping an environment that is already scaled to zero must succeed and
	// say so, rather than reporting a stop that did not happen. The distinction
	// only reaches the operator in real-run — dry-run prints the plan, not the
	// summary — so the no-op wording needs its own real-run scenario.
	t.Run("real_run_already_stopped_reports_the_no_op", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlStopRunState(t, setup, 0, 0)
		result := erun.Run(t, []string{"stop", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "stop/real_run_already_stopped_reports_the_no_op", normalize.Apply(result.Combined))
	})
}
