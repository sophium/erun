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

// stubKubectlScalingStopTarget is stubKubectlStopRunState's stateful sibling:
// the run-state read answers with whatever the last `kubectl scale` set, so a
// real-run stop's own post-scale confirmation sees the zero it just asked for.
// Real-run scenarios need it — with the fixed-answer stub the deployment claims
// it still wants a replica after the scale, which is exactly the "the stop did
// not take effect" case the confirmation is there to catch.
func stubKubectlScalingStopTarget(t *testing.T, setup env.Setup, desired int) []string {
	t.Helper()
	stubs := setup.Cwd + "/stubs"
	fixture.StubKubectlScalingRuntime(t, stubs, "team-devops", desired)
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
	t.Parallel()
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
		envVars := stubKubectlScalingStopTarget(t, setup, 1)
		result := erun.Run(t, []string{"stop", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "stop/real_run_records_stop_intent", normalize.Apply(result.Combined))
		if config := readSeededEnvConfig(t, setup, "team", "dev"); !strings.Contains(config, "stopped: true") {
			t.Fatalf("stop did not record the intent on the env config:\n%s", config)
		}
	})

	// The regression this suite exists to pin: a stop must survive the desktop
	// respawning its session tabs. Stopping drops every attached session, the
	// desktop respawns `erun open` per tab, and before the fix each respawn
	// cleared the recorded stop and scaled the runtime back up — so an
	// environment with a tab open could not be stopped at all. The sequence runs
	// against a stub that remembers what the last `kubectl scale` set, so each
	// leg genuinely observes the previous leg's cluster state.
	t.Run("real_run_stop_survives_a_session_reconnect", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubKubectlScalingRuntime(t, stubs, "team-devops", 1)
		// The operator open in leg three plans port-forwards, so it needs the
		// same holder-probe stubs and darwin pin every `open` scenario uses.
		stubLsofNoHolder(t, stubs)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "lsof", "ps")...)
		envVars = append(envVars, openHostOSOverride)

		stopped := erun.Run(t, []string{"stop", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if stopped.ExitCode != 0 {
			t.Fatalf("stop exit %d: %s", stopped.ExitCode, stopped.Combined)
		}
		if config := readSeededEnvConfig(t, setup, "team", "dev"); !strings.Contains(config, "stopped: true") {
			t.Fatalf("stop did not record the intent:\n%s", config)
		}

		// Leg two is the desktop's tab respawn: same `erun open`, plus the
		// --reconnect the desktop attaches to every automatic respawn. It must
		// fail rather than wake, and must leave the recorded stop alone.
		reconnected := erun.Run(t, []string{"open", "team", "dev", "--app-session", "open-0", "--reconnect", "--no-shell", "--no-alias-prompt"},
			erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if reconnected.ExitCode == 0 {
			t.Fatalf("a session reconnect must not succeed against a stopped environment:\n%s", reconnected.Combined)
		}
		if config := readSeededEnvConfig(t, setup, "team", "dev"); !strings.Contains(config, "stopped: true") {
			t.Fatalf("the reconnect erased the recorded stop:\n%s", config)
		}

		// Leg three is the operator opening the environment, which is still the
		// one gesture that starts it: the plan reads 0 replicas from the stub —
		// proof leg two scaled nothing — and wakes.
		opened := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"},
			erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if opened.ExitCode != 0 {
			t.Fatalf("operator open exit %d: %s", opened.ExitCode, opened.Combined)
		}

		golden.Equal(t, "stop/real_run_stop_survives_a_session_reconnect", strings.Join([]string{
			"$ erun stop team dev",
			normalize.Apply(stopped.Combined),
			"$ erun open team dev --app-session open-0 --reconnect --no-shell --no-alias-prompt",
			normalize.Apply(reconnected.Combined),
			"$ erun open team dev --no-shell --no-alias-prompt --dry-run",
			normalize.Apply(opened.Combined),
		}, "\n"))
	})

	// --output json is the orchestrator-facing result; the human summary must
	// not corrupt the payload on stdout.
	t.Run("real_run_output_json", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlScalingStopTarget(t, setup, 1)
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
