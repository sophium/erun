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

// stubKubectlStopRunState answers the run-state read with fixed replica counts, which
// decide stop's branch.
func stubKubectlStopRunState(t *testing.T, setup env.Setup, desired, ready int) []string {
	t.Helper()
	stubs := setup.Cwd + "/stubs"
	fixture.StubKubectlRuntimeRunState(t, stubs, desired, ready)
	return append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
}

// stubKubectlScalingStopTarget answers the run-state read with the last scale, so a
// real stop's post-scale confirmation sees zero.
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
	t.Run("help", testStopHelp)

	// The scale patch alone is drift helm reverts; the intent must be recorded too.
	t.Run("dry_run_running_environment", testStopDryRunRunningEnvironment)

	t.Run("dry_run_already_scaled_to_zero", testStopDryRunAlreadyScaledToZero)

	t.Run("dry_run_already_stopped_is_a_no_op", testStopDryRunAlreadyStoppedIsANoOp)

	t.Run("dry_run_defaults_to_current_scope", testStopDryRunDefaultsToCurrentScope)

	t.Run("dry_run_target_flags", testStopDryRunTargetFlags)

	t.Run("not_deployed_fails_informatively", testStopNotDeployedFailsInformatively)

	t.Run("real_run_records_stop_intent", testStopRealRunRecordsStopIntent)

	// A stop must survive the desktop respawning `erun open` per session tab.
	t.Run("real_run_stop_survives_a_session_reconnect", testStopRealRunStopSurvivesASessionReconnect)

	t.Run("real_run_output_json", testStopRealRunOutputJson)

	// Dry-run prints the plan, not the summary, so the no-op wording needs a real run.
	t.Run("real_run_already_stopped_reports_the_no_op", testStopRealRunAlreadyStoppedReportsTheNoOp)

	// Stop scales only the runtime; it must name the platform components left running,
	// excluding the runtime itself.
	t.Run("real_run_names_the_components_it_left_running", testStopRealRunNamesTheComponentsItLeftRunning)

	t.Run("dry_run_already_stopped_names_the_components_it_leaves_running", testStopDryRunAlreadyStoppedNamesTheComponentsItLeavesRunning)
}

func testStopHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"stop", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "stop/help", normalize.Apply(result.Combined))
}

func testStopDryRunRunningEnvironment(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlStopRunState(t, setup, 1, 1)
	result := erun.Run(t, []string{"stop", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "stop/dry_run_running_environment", normalize.Apply(result.Combined))
}

func testStopDryRunAlreadyScaledToZero(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlStopRunState(t, setup, 0, 0)
	result := erun.Run(t, []string{"stop", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "stop/dry_run_already_scaled_to_zero", normalize.Apply(result.Combined))
}

func testStopDryRunAlreadyStoppedIsANoOp(t *testing.T) {
	setup := env.New(t)
	fixture.SeedStoppedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlStopRunState(t, setup, 0, 0)
	result := erun.Run(t, []string{"stop", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "stop/dry_run_already_stopped_is_a_no_op", normalize.Apply(result.Combined))
}

func testStopDryRunDefaultsToCurrentScope(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlStopRunState(t, setup, 1, 1)
	result := erun.Run(t, []string{"stop", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "stop/dry_run_defaults_to_current_scope", normalize.Apply(result.Combined))
}

func testStopDryRunTargetFlags(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedTenantEnv(t, setup, "team", "prod")
	envVars := stubKubectlStopRunState(t, setup, 1, 1)
	result := erun.Run(t, []string{"stop", "--tenant", "team", "--environment", "prod", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "stop/dry_run_target_flags", normalize.Apply(result.Combined))
}

func testStopNotDeployedFailsInformatively(t *testing.T) {
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
}

func testStopRealRunRecordsStopIntent(t *testing.T) {
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
}

func testStopRealRunStopSurvivesASessionReconnect(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	fixture.StubKubectlScalingRuntime(t, stubs, "team-devops", 1)
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

	// Leg two is the desktop's --reconnect respawn: it must fail and keep the stop.
	reconnected := erun.Run(t, []string{"open", "team", "dev", "--app-session", "open-0", "--reconnect", "--no-shell", "--no-alias-prompt"},
		erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if reconnected.ExitCode == 0 {
		t.Fatalf("a session reconnect must not succeed against a stopped environment:\n%s", reconnected.Combined)
	}
	if config := readSeededEnvConfig(t, setup, "team", "dev"); !strings.Contains(config, "stopped: true") {
		t.Fatalf("the reconnect erased the recorded stop:\n%s", config)
	}

	// Leg three is the operator's open: it reads 0 replicas and wakes.
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
}

func testStopRealRunOutputJson(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlScalingStopTarget(t, setup, 1)
	result := erun.Run(t, []string{"stop", "team", "dev", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "stop/real_run_output_json", normalize.Apply(result.Stdout))
}

func testStopRealRunAlreadyStoppedReportsTheNoOp(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlStopRunState(t, setup, 0, 0)
	result := erun.Run(t, []string{"stop", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "stop/real_run_already_stopped_reports_the_no_op", normalize.Apply(result.Combined))
}

func testStopRealRunNamesTheComponentsItLeftRunning(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnvDeployingComponents(t, setup, "team", "dev")
	envVars := stubKubectlScalingStopTarget(t, setup, 1)
	result := erun.Run(t, []string{"stop", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "stop/real_run_names_the_components_it_left_running", normalize.Apply(result.Combined))
	if strings.Contains(result.Combined, "team-devops keep") {
		t.Fatalf("the stopped runtime must not be named among the components left running:\n%s", result.Combined)
	}
}

func testStopDryRunAlreadyStoppedNamesTheComponentsItLeavesRunning(t *testing.T) {
	setup := env.New(t)
	fixture.SeedStoppedTenantEnvDeployingComponents(t, setup, "team", "dev")
	envVars := stubKubectlStopRunState(t, setup, 0, 0)
	result := erun.Run(t, []string{"stop", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "stop/dry_run_already_stopped_names_the_components_it_leaves_running", normalize.Apply(result.Combined))
}
