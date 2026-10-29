package integration

import (
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testOpenAppSessionDryRunPureOpenDoesNotDeploy(t *testing.T) {
	t.Parallel()
	// open does not deploy; the shell preview still waits for the runtime.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	// ERUN_FORCE_TTY=1 mirrors the desktop's dtach pty; without it open takes the
	// no-shell branch.
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "open-0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/app_session_dry_run_pure_open_does_not_deploy", normalize.Apply(result.Combined))
}

func testOpenAppSessionAiDryRunWrapsDtachAndLaunchesClaude(t *testing.T) {
	t.Parallel()
	// The claude session is wrapped in persistent dtach so reopening reconnects.
	// With no model pinned, it starts on the first available model.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "ai", "--ai", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/app_session_ai_dry_run_wraps_dtach_and_launches_claude", normalize.Apply(result.Combined))
}

func testOpenAppSessionAiDryRunLaunchesClaudeWithModelAndVerboseDebug(t *testing.T) {
	t.Parallel()
	// The launch-ai.sh block in the golden is the contract the desktop AI tab relies on.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithClaude(t, setup, "team", "dev",
		"claude:\n"+
			"  models: [opus, fable]\n"+
			"  defaultmodel: fable\n"+
			"  verbosedebug: true\n"+
			"  effort: high\n")
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "ai", "--ai", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/app_session_ai_dry_run_launches_claude_with_model_and_verbose_debug", normalize.Apply(result.Combined))
}

func testOpenAppSessionAiDryRunFallsBackToAvailableWhenDefaultDropped(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithClaude(t, setup, "team", "dev",
		"claude:\n"+
			"  models: [opus]\n"+
			"  defaultmodel: fable\n")
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "ai", "--ai", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/app_session_ai_dry_run_falls_back_to_available_when_default_dropped", normalize.Apply(result.Combined))
}

func testOpenAppSessionAiDryRunGatewayAuthDisablesRemoteControl(t *testing.T) {
	t.Parallel()
	// Remote Control pairs through the claude.ai relay, which gateways cannot
	// authenticate, so a gateway launch omits --remote-control.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithClaude(t, setup, "team", "dev",
		"claude:\n"+
			"  usemantle: true\n")
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "ai", "--ai", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/app_session_ai_dry_run_gateway_auth_disables_remote_control", normalize.Apply(result.Combined))
}

func testOpenAppSessionShellDryRunWrapsDtach(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "open-0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/app_session_shell_dry_run_wraps_dtach", normalize.Apply(result.Combined))
}

func testOpenAppSessionContributeAiDryRunPreludesClone(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "contribute-ai", "--contribute", "--ai", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/app_session_contribute_ai_dry_run_preludes_clone", normalize.Apply(result.Combined))
}
