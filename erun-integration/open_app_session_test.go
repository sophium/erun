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
	// open is a pure primitive: it does not deploy. The
	// desktop composes build→push→deploy on create / via the Deploy
	// button and spawns tabs that just open the shell, so the default
	// open must trace that it is not deploying — and nothing else
	// changes: the shell preview (the deployment wait + dtach exec) still
	// runs, which holds the tab until the runtime is reachable.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	// ERUN_FORCE_TTY=1 mirrors the desktop's real invocation: an --app-session
	// tab runs inside a dtach-allocated pty, so stdin really is a TTY there,
	// unlike this harness's piped default. Without it open would now take the
	// non-interactive no-shell branch instead of the shell preview this
	// scenario exists to lock.
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "open-0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/app_session_dry_run_pure_open_does_not_deploy", normalize.Apply(result.Combined))
}

func testOpenAppSessionAiDryRunWrapsDtachAndLaunchesClaude(t *testing.T) {
	t.Parallel()
	// The desktop AI tab runs `erun open --app-session ai --ai`. Without
	// --no-shell the dry-run reaches traceShellPreview, so the bootstrap-script
	// block locks that the remote program is wrapped in a persistent dtach
	// session (so reopening reconnects instead of stranding a parallel claude)
	// and that the cwd-guarded claude is that session's create-time program.
	// With no model pinned, the session starts on the first available
	// model rather than the agent's own default.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	// ERUN_FORCE_TTY=1: see app_session_dry_run_pure_open_does_not_deploy.
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "ai", "--ai", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/app_session_ai_dry_run_wraps_dtach_and_launches_claude", normalize.Apply(result.Combined))
}

func testOpenAppSessionAiDryRunLaunchesClaudeWithModelAndVerboseDebug(t *testing.T) {
	t.Parallel()
	// When the env config sets a default Claude model that is in
	// the env's available models, and opts in to verbose+debug, the AI
	// session's create-time program must carry `--model <m> --verbose
	// --debug` after the env effort, in both branches of the cwd-guarded
	// resume. The launch-ai.sh block in the golden is the contract the
	// desktop AI tab relies on.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithClaude(t, setup, "team", "dev",
		"claude:\n"+
			"  models: [opus, fable]\n"+
			"  defaultmodel: fable\n"+
			"  verbosedebug: true\n"+
			"  effort: high\n")
	// ERUN_FORCE_TTY=1: see app_session_dry_run_pure_open_does_not_deploy.
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "ai", "--ai", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/app_session_ai_dry_run_launches_claude_with_model_and_verbose_debug", normalize.Apply(result.Combined))
}

func testOpenAppSessionAiDryRunFallsBackToAvailableWhenDefaultDropped(t *testing.T) {
	t.Parallel()
	// A chosen default no longer among the env's available models is
	// dropped; the session falls back to the first available model rather
	// than starting on none.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithClaude(t, setup, "team", "dev",
		"claude:\n"+
			"  models: [opus]\n"+
			"  defaultmodel: fable\n")
	// ERUN_FORCE_TTY=1: see app_session_dry_run_pure_open_does_not_deploy.
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "ai", "--ai", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/app_session_ai_dry_run_falls_back_to_available_when_default_dropped", normalize.Apply(result.Combined))
}

func testOpenAppSessionAiDryRunGatewayAuthDisablesRemoteControl(t *testing.T) {
	t.Parallel()
	// The managed AI session enables Claude Code Remote Control by default
	// (named <tenant>/<env>) so it is drivable from the Claude iOS app — but
	// Remote Control pairs through the claude.ai account relay, which the
	// Bedrock/Mantle inference gateways cannot authenticate. When the env
	// routes Claude through a gateway the launch must omit --remote-control
	// entirely; this golden locks that gate.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithClaude(t, setup, "team", "dev",
		"claude:\n"+
			"  usemantle: true\n")
	// ERUN_FORCE_TTY=1: see app_session_dry_run_pure_open_does_not_deploy.
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "ai", "--ai", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/app_session_ai_dry_run_gateway_auth_disables_remote_control", normalize.Apply(result.Combined))
}

func testOpenAppSessionShellDryRunWrapsDtach(t *testing.T) {
	t.Parallel()
	// The ERun and custom "Terminal N" tabs run `erun open --app-session open-N`:
	// the same persistent dtach session but running a plain interactive shell —
	// no claude launch and no contribute prelude in the launcher body.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	// ERUN_FORCE_TTY=1: see app_session_dry_run_pure_open_does_not_deploy.
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "open-0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/app_session_shell_dry_run_wraps_dtach", normalize.Apply(result.Combined))
}

func testOpenAppSessionContributeAiDryRunPreludesClone(t *testing.T) {
	t.Parallel()
	// The contribute-AI tab runs `erun open --app-session contribute-ai
	// --contribute --ai`. The persistent dtach launcher must prepend the
	// contribute prelude (contribute toolchain on PATH, cd into the cloned
	// repo) before launching the cwd-guarded claude, all inside the
	// reattachable session.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	// ERUN_FORCE_TTY=1: see app_session_dry_run_pure_open_does_not_deploy.
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "contribute-ai", "--contribute", "--ai", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/app_session_contribute_ai_dry_run_preludes_clone", normalize.Apply(result.Combined))
}
