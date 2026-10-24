package integration

import (
	"fmt"
	"os"
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

func TestOpen(t *testing.T) {
	for _, scenario := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"help", testOpenHelp},
		{"refuses_host_environment", testOpenRefusesHostEnvironment},
		{"no_shell_dry_run", testOpenNoShellDryRun},
		{"no_shell_stdout_carries_only_the_eval_able_shell", testOpenNoShellStdoutCarriesOnlyTheEvalAbleShell},
		{"no_shell_interactive_terminal_still_shows_the_ssh_block", testOpenNoShellInteractiveTerminalStillShowsTheSshBlock},
		{"no_tty_dry_run_falls_back_to_no_shell", testOpenNoTtyDryRunFallsBackToNoShell},
		{"no_tty_dry_run_falls_back_to_no_shell_stdin_dev_null", testOpenNoTtyDryRunFallsBackToNoShellStdinDevNull},
		{"alias_prompt_dry_run_accept_traces_append", testOpenAliasPromptDryRunAcceptTracesAppend},
		{"alias_prompt_decline_prints_hint", testOpenAliasPromptDeclinePrintsHint},
		{"alias_prompt_accept_appends_alias_real_run", testOpenAliasPromptAcceptAppendsAliasRealRun},
		{"no_shell_real_run_deploys_published_chart_and_persists_version", testOpenNoShellRealRunDeploysPublishedChartAndPersistsVersion},
		{"vscode_real_run_with_deploy_requires_shell_deploy_errors", testOpenVscodeRealRunWithDeployRequiresShellDeployErrors},
		{"no_shell_real_run_not_deployed_errors", testOpenNoShellRealRunNotDeployedErrors},
		{"no_shell_real_run_check_unreachable_errors_without_claiming_absence", testOpenNoShellRealRunCheckUnreachableErrorsWithoutClaimingAbsence},
		{"no_shell_real_run_check_unreachable_sanitizes_klog_framed_error", testOpenNoShellRealRunCheckUnreachableSanitizesKlogFramedError},
		{"alias_prompt_skipped_when_alias_configured", testOpenAliasPromptSkippedWhenAliasConfigured},
		{"alias_prompt_bash_accept_creates_bashrc", testOpenAliasPromptBashAcceptCreatesBashrc},
		{"alias_powershell_dialect_prints_function_hint", testOpenAliasPowershellDialectPrintsFunctionHint},
		{"snapshot_env_config_drives_local_build", testOpenSnapshotEnvConfigDrivesLocalBuild},
		{"version_override_skips_local_build", testOpenVersionOverrideSkipsLocalBuild},
		{"runtime_image_override_uses_default_chart", testOpenRuntimeImageOverrideUsesDefaultChart},
		{"vscode_without_sshd_errors_with_guidance", testOpenVscodeWithoutSshdErrorsWithGuidance},
		{"intellij_without_sshd_errors_with_guidance", testOpenIntellijWithoutSshdErrorsWithGuidance},
		{"vscode_and_intellij_conflict", testOpenVscodeAndIntellijConflict},
		{"remote_dry_run_traces_port_forwards", testOpenRemoteDryRunTracesPortForwards},
		{"remote_dry_run_aws_alias_propagates_host_credentials", testOpenRemoteDryRunAwsAliasPropagatesHostCredentials},
		{"dry_run_configured_aws_alias_refreshes_host_credentials", testOpenDryRunConfiguredAwsAliasRefreshesHostCredentials},
		{"app_session_dry_run_pure_open_does_not_deploy", testOpenAppSessionDryRunPureOpenDoesNotDeploy},
		{"deploy_flag_dry_run_deploys_runtime_before_opening", testOpenDeployFlagDryRunDeploysRuntimeBeforeOpening},
		{"dry_run_wakes_stopped_runtime_before_forwarding", testOpenDryRunWakesStoppedRuntimeBeforeForwarding},
		{"dry_run_unaffected_by_kubectl_deployment_wait_library_execution_mode", testOpenDryRunUnaffectedByKubectlDeploymentWaitLibraryExecutionMode},
		{"reconnect_dry_run_refuses_to_start_a_stopped_runtime", testOpenReconnectDryRunRefusesToStartAStoppedRuntime},
		{"real_run_kubectl_deployment_wait_library_execution_mode_wakes_a_stopped_runtime", testOpenRealRunKubectlDeploymentWaitLibraryExecutionModeWakesAStoppedRuntime},
		{"reconnect_dry_run_reattaches_a_running_runtime", testOpenReconnectDryRunReattachesARunningRuntime},
		{"dry_run_running_runtime_wake_is_quiet", testOpenDryRunRunningRuntimeWakeIsQuiet},
		{"deploy_flag_dry_run_clears_stop_intent_before_rendering_the_chart", testOpenDeployFlagDryRunClearsStopIntentBeforeRenderingTheChart},
		{"deploy_flag_dry_run_fresh_env_requires_runtime_version", testOpenDeployFlagDryRunFreshEnvRequiresRuntimeVersion},
		{"app_session_ai_dry_run_wraps_dtach_and_launches_claude", testOpenAppSessionAiDryRunWrapsDtachAndLaunchesClaude},
		{"app_session_ai_dry_run_launches_claude_with_model_and_verbose_debug", testOpenAppSessionAiDryRunLaunchesClaudeWithModelAndVerboseDebug},
		{"app_session_ai_dry_run_falls_back_to_available_when_default_dropped", testOpenAppSessionAiDryRunFallsBackToAvailableWhenDefaultDropped},
		{"app_session_ai_dry_run_gateway_auth_disables_remote_control", testOpenAppSessionAiDryRunGatewayAuthDisablesRemoteControl},
		{"app_session_shell_dry_run_wraps_dtach", testOpenAppSessionShellDryRunWrapsDtach},
		{"app_session_contribute_ai_dry_run_preludes_clone", testOpenAppSessionContributeAiDryRunPreludesClone},
		{"vscode_dry_run", testOpenVscodeDryRun},
		{"intellij_dry_run", testOpenIntellijDryRun},
		{"vscode_real_run_writes_known_hosts_and_launches_ide", testOpenVscodeRealRunWritesKnownHostsAndLaunchesIde},
		{"intellij_real_run_writes_jetbrains_config_and_launches_ide", testOpenIntellijRealRunWritesJetbrainsConfigAndLaunchesIde},
		{"intellij_gateway_adopts_forwards_and_launches_gateway", testOpenIntellijGatewayAdoptsForwardsAndLaunchesGateway},
		{"intellij_real_run_linux_bootstraps_via_path_idea", testOpenIntellijRealRunLinuxBootstrapsViaPathIdea},
		{"default_tenant_environment_resolves_from_root_config", testOpenDefaultTenantEnvironmentResolvesFromRootConfig},
		{"kubectl_error_assumes_not_deployed", testOpenKubectlErrorAssumesNotDeployed},
		{"not_initialized_triggers_init_retry", testOpenNotInitializedTriggersInitRetry},
		{"tenant_cloud_provider_issuers_flow_into_runtime_deploy", testOpenTenantCloudProviderIssuersFlowIntoRuntimeDeploy},
		{"default_env_not_configured_runs_init_with_tenant", testOpenDefaultEnvNotConfiguredRunsInitWithTenant},
		{"tenant_flag_only_resolves_default_env", testOpenTenantFlagOnlyResolvesDefaultEnv},
		{"environment_positional_resolves_default_tenant", testOpenEnvironmentPositionalResolvesDefaultTenant},
		{"remote_runtime_image_override", testOpenRemoteRuntimeImageOverride},
		{"persisted_local_port_range_is_honoured", testOpenPersistedLocalPortRangeIsHonoured},
		{"walker_skips_index_claimed_by_other_tenant", testOpenWalkerSkipsIndexClaimedByOtherTenant},
		{"overlapping_persisted_ranges_fail_with_pointer", testOpenOverlappingPersistedRangesFailWithPointer},
		{"misaligned_persisted_range_fails_with_pointer", testOpenMisalignedPersistedRangeFailsWithPointer},
		{"adopts_existing_kubectl_port_forward", testOpenAdoptsExistingKubectlPortForward},
		{"previews_replacing_a_bound_but_dead_port_forward", testOpenPreviewsReplacingABoundButDeadPortForward},
		{"real_run_reestablishes_a_bound_but_dead_port_forward", testOpenRealRunReestablishesABoundButDeadPortForward},
		{"no_shell_real_run_refuses_success_when_a_stale_forward_cannot_be_stopped", testOpenNoShellRealRunRefusesSuccessWhenAStaleForwardCannotBeStopped},
		{"previews_reaping_a_recorded_forward_that_never_bound_its_port", testOpenPreviewsReapingARecordedForwardThatNeverBoundItsPort},
		{"real_run_reaps_a_recorded_forward_that_never_bound_its_port", testOpenRealRunReapsARecordedForwardThatNeverBoundItsPort},
		{"real_run_sweeps_an_orphaned_forward_with_no_matching_state", testOpenRealRunSweepsAnOrphanedForwardWithNoMatchingState},
		{"refuses_to_bind_when_foreign_process_holds_port", testOpenRefusesToBindWhenForeignProcessHoldsPort},
		{"api_forward_targets_the_environments_own_service_port", testOpenApiForwardTargetsTheEnvironmentsOwnServicePort},
		{"deployment_match_ignores_missing_api_port", testOpenDeploymentMatchIgnoresMissingApiPort},
		{"shell_real_run_single_pass_via_kubectl_stub", testOpenShellRealRunSinglePassViaKubectlStub},
		{"no_tty_real_run_keeps_forwards_without_a_shell", testOpenNoTtyRealRunKeepsForwardsWithoutAShell},
		{"real_run_migrates_a_record_from_the_user_config_dir_layout", testOpenRealRunMigratesARecordFromTheUserConfigDirLayout},
		{"real_run_port_forward_start_recovers_from_a_transient_failure", testOpenRealRunPortForwardStartRecoversFromATransientFailure},
		{"real_run_port_forward_start_degrades_after_persistent_transient_failures", testOpenRealRunPortForwardStartDegradesAfterPersistentTransientFailures},
		{"real_run_port_forward_start_survives_a_dying_listeners_socket", testOpenRealRunPortForwardStartSurvivesADyingListenersSocket},
		{"real_run_port_forward_start_reports_a_port_that_never_comes_free", testOpenRealRunPortForwardStartReportsAPortThatNeverComesFree},
		{"dry_run_unaffected_by_kubectl_deployment_get_library_execution_mode", testOpenDryRunUnaffectedByKubectlDeploymentGetLibraryExecutionMode},
		{"real_run_kubectl_deployment_get_library_execution_mode_reaches_the_api_server_directly", testOpenRealRunKubectlDeploymentGetLibraryExecutionModeReachesTheApiServerDirectly},
		{"real_run_kubectl_deployment_wait_library_execution_mode_reaches_the_api_server_directly", testOpenRealRunKubectlDeploymentWaitLibraryExecutionModeReachesTheApiServerDirectly},
		{"shell_real_run_session_taken_over_exits_with_notice", testOpenShellRealRunSessionTakenOverExitsWithNotice},
		{"shell_real_run_failed_rollout_prints_pod_diagnostics", testOpenShellRealRunFailedRolloutPrintsPodDiagnostics},
		{"shell_real_run_reattach_deploy_cycles_once", testOpenShellRealRunReattachDeployCyclesOnce},
		{"shell_real_run_pod_replaced_reattaches", testOpenShellRealRunPodReplacedReattaches},
	} {
		t.Run(scenario.name, scenario.run)
	}
}

func testOpenHelp(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	result := erun.Run(t, []string{"open", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/help", normalize.Apply(result.Combined))
}

func testOpenRefusesHostEnvironment(t *testing.T) {
	t.Parallel()
	// A host env has no pod and no cluster to open a kubectl-exec shell
	// into — its worktree is already the operator's own directory, so open
	// refuses and points there instead of resolving a kubernetes context
	// (of which it has none) or attempting a port-forward.
	setup := env.New(t)
	fixture.SeedHostTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"open", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "open/refuses_host_environment", normalize.Apply(result.Combined))
}

func testOpenNoShellDryRun(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/no_shell_dry_run", normalize.Apply(result.Combined))
}

func testOpenNoShellStdoutCarriesOnlyTheEvalAbleShell(t *testing.T) {
	t.Parallel()
	// The documented alias is
	// `alias team-dev='eval "$(erun open team dev --no-shell)"'`, so stdout
	// IS the script: command substitution hands the child a pipe, never a
	// terminal. An sshd-enabled env used to prepend the human-readable SSH
	// block ("SSH:", "  host: ...", ...) to stdout, and eval ran those
	// prose lines as commands — seven `command not found` errors per alias
	// invocation. Moving the block to stderr is not the fix (#394 silenced
	// stderr on purpose, and the alias does not capture it), so the block
	// must simply be terminal-only.
	//
	// `bash -n` accepts the tainted stdout — the block is syntactically
	// valid shell and only fails at run time — so assert the exact script
	// instead of a syntax check.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHD(t, setup, "team", "dev")
	envVars := append(stubKubectlNotFound(t, setup), "SHELL=/bin/bash")
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	// The remote env's workspace maps to a local worktree under this
	// scenario's private HOME, which is what the script cds into.
	want := "kubectl config use-context 'test-context' >/dev/null &&\n" +
		"kubectl config set-context --current --namespace='team-dev' >/dev/null &&\n" +
		"cd '" + filepath.Join(setup.Home, "git", "team") + "'\n"
	if result.Stdout != want {
		t.Errorf("stdout must be only the eval-able setup script\nwant:\n%s\ngot:\n%s", want, result.Stdout)
	}
}

func testOpenNoShellInteractiveTerminalStillShowsTheSshBlock(t *testing.T) {
	t.Parallel()
	// The counterpart to the scenario above: the terminal-only gate must
	// not delete the block. A human running --no-shell at a terminal still
	// needs the host, alias, port, user, key and workspace, so the same run
	// that stays clean for eval keeps printing it. ERUN_FORCE_TTY=1 is the
	// TTY seam writerIsTerminal already honors, the same lift the
	// alias-prompt scenarios above rely on.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHD(t, setup, "team", "dev")
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1", "SHELL=/bin/bash")
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	// Normalize first: the host address, HOME and temp paths in the block
	// are host-specific, so the assertions below name the normalized tokens
	// the goldens use.
	normalizedStdout := normalize.Apply(result.Stdout)
	for _, wantLine := range []string{
		"SSH:\n",
		"  host: <LOOPBACK>",
		"  alias: erun-team-dev",
		"  port: 17022",
		"  user: erun",
		"  key: none",
		"  workspace: <HOME>/git/team",
	} {
		if !strings.Contains(normalizedStdout, wantLine) {
			t.Errorf("interactive stdout must still show the SSH block; missing %q in:\n%s", wantLine, result.Stdout)
		}
	}
	// The script the alias evaluates must still be there too — the block is
	// additional human output, not a replacement for it.
	if !strings.Contains(result.Stdout, "cd '"+filepath.Join(setup.Home, "git", "team")+"'\n") {
		t.Errorf("interactive stdout must still end with the setup script, got:\n%s", result.Stdout)
	}
}

func testOpenNoTtyDryRunFallsBackToNoShell(t *testing.T) {
	t.Parallel()
	// The stale-forward recovery advice names bare `erun open <tenant>
	// <env>`, which a non-interactive caller (an MCP client, an
	// orchestrator, a script) runs with no TTY on stdin — this harness's
	// default piped stdin stands in for that. Without --no-shell on the
	// command line, open must still take the no-shell branch on its own:
	// a real kubectl exec -it there would read EOF and return almost
	// instantly, exiting before anything supervises the port-forwards
	// just started.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/no_tty_dry_run_falls_back_to_no_shell", normalize.Apply(result.Combined))
}

func testOpenNoTtyDryRunFallsBackToNoShellStdinDevNull(t *testing.T) {
	t.Parallel()
	// The scenario above already runs with stdin unset, which erun.Run
	// binds to an in-memory pipe — never a character device, so it cannot
	// reproduce a stat-based TTY check confusing a non-terminal character
	// device for a terminal. Binding stdin to the real OS null device
	// (StdinFromDevNull) is what actually exercises that: /dev/null is a
	// character device but not a terminal, and a `command < /dev/null`
	// invocation is the conventional way callers make a command
	// non-interactive. Same assertion as the scenario above; the point of
	// this one is the stdin source, not a different resolved plan.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars, StdinFromDevNull: true})
	golden.Equal(t, "open/no_tty_dry_run_falls_back_to_no_shell", normalize.Apply(result.Combined))
}

func testOpenNoShellRealRunDeploysPublishedChartAndPersistsVersion(t *testing.T) {
	// Real-run open against a local env whose runtime deployment does
	// not exist and whose tenant repo has no devops chart. With the
	// scaffold retired there is no chart-creation prompt: the
	// runtime spec resolves to the published erun-devops OCI chart.
	// Covers, in one pass, the branches dry-run cannot reach:
	//   - deployRuntime's real helm deploy of the published chart (helm
	//     stub) wrapped in wrapOpenHelmDeployWithSpinner;
	//   - persistOpenRuntimeVersion's save branch: --version 9.9.9
	//     differs from the persisted 1.0.0, so the env config must be
	//     rewritten with the deployed version (side-effect assert).
	// The kubectl stub reports the deployment NotFound (decision input
	// for shouldDeployRuntime) and runs the port-forward simulator for
	// the post-deploy forwards on the pinned 26100 range.
	// --no-alias-prompt keeps the alias setup out of the way. The golden
	// is stdout-only by design: real-run --no-shell silences stderr so
	// an `eval "$(erun open ... --no-shell)"` alias stays quiet
	// (shouldSilenceNoShellOutput).
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
	stubsDir := filepath.Join(setup.Cwd, "stubs")
	envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
		DeploymentName:     "team-devops",
		ContainerName:      "team-devops",
		DeploymentNotFound: true,
	})...)
	fixture.StubBinary(t, stubsDir, "helm", "")
	envVars = append(envVars, fixture.StubEnv(stubsDir, "helm")...)
	// Real-run --no-shell still emits the setup preamble on stdout; pin
	// DetectHost so its dialect stays POSIX. See openHostOSOverride.
	envVars = append(envVars, openHostOSOverride)
	// The runtime chart ladder must confirm erun-devops published at the
	// version before installing it; the seam stands in for that registry
	// read. Both the persisted 1.0.0 (the deploy-decision phase) and the
	// requested 9.9.9 (the actual deploy) are resolved along the way.
	envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0,erun-devops:9.9.9")
	result := erun.Run(t, []string{"open", "team", "dev", "--version", "9.9.9", "--no-shell", "--no-alias-prompt"}, erun.RunOptions{
		Cwd: setup.Cwd,
		Env: envVars,
	})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/no_shell_real_run_deploys_published_chart_and_persists_version", normalize.Apply(result.Combined))
	// The retired scaffold must not reappear (side effect outside the
	// captured streams).
	if _, err := os.Stat(filepath.Join(setup.Cwd, "team-devops")); !os.IsNotExist(err) {
		t.Errorf("expected no devops scaffold in the tenant repo, stat err=%v", err)
	}
	envCfg, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml"))
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	if !strings.Contains(string(envCfg), "runtimeversion: 9.9.9") {
		t.Errorf("expected runtimeversion 9.9.9 persisted on the env config, got:\n%s", envCfg)
	}
}

func testOpenNoShellRealRunNotDeployedErrors(t *testing.T) {
	t.Parallel()
	// open is a pure primitive and does not deploy. A port-forward that
	// can't bind is no longer fatal, so a genuinely undeployed runtime is
	// caught up front by deployment presence — the run fails fast with an
	// actionable "run `erun deploy`" error before any forward starts, rather
	// than being inferred from a downstream port-forward timeout. The kubectl
	// NotFound stub is the decision input; no ports are needed because the
	// run errors before the forwards.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when the runtime is not deployed, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "open/no_shell_real_run_not_deployed_errors", normalize.Apply(result.Combined))
}

func testOpenNoShellRealRunCheckUnreachableErrorsWithoutClaimingAbsence(t *testing.T) {
	t.Parallel()
	// The deployment presence check can fail without ever getting an
	// answer — no context, an unreachable API server, credentials or
	// permissions refused. That is not evidence the deployment is absent,
	// so ensureRuntimeDeployed must surface the check's own classified
	// error (naming the cause, carrying kubectl's real output) rather than
	// the "is not deployed; run erun deploy" text reserved for a confirmed
	// absence. The generic-error kubectl stub ("Unable to connect to the
	// server: ... i/o timeout") is the decision input; no ports are needed
	// because the run errors before the forwards.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlGenericError(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when the deployment check could not resolve an answer, got 0:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "is not deployed") {
		t.Fatalf("a check that could not ask the cluster must never read as absence, got:\n%s", result.Combined)
	}
	golden.Equal(t, "open/no_shell_real_run_check_unreachable_errors_without_claiming_absence", normalize.Apply(result.Combined))
}

func testOpenNoShellRealRunCheckUnreachableSanitizesKlogFramedError(t *testing.T) {
	t.Parallel()
	// erun#1766: the same unreachable-cluster path above, but with the
	// exact klog "Unhandled Error" shape client-go actually emits
	// (severity, timestamp, goroutine id, Go source location wrapping
	// the real cause). Before the fix, checkKubernetesDeploymentWithContext
	// pasted this verbatim, so the operator-facing error carried
	// kubectl-internal noise around the one sentence that named the
	// unreachable address. It must instead read as the cause alone,
	// with the kube context named from erun's own config.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlKlogFramedError(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when the deployment check could not resolve an answer, got 0:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "is not deployed") {
		t.Fatalf("a check that could not ask the cluster must never read as absence, got:\n%s", result.Combined)
	}
	golden.Equal(t, "open/no_shell_real_run_check_unreachable_sanitizes_klog_framed_error", normalize.Apply(result.Combined))
}

func testOpenRemoteDryRunTracesPortForwards(t *testing.T) {
	t.Parallel()
	// Exercises cmd/api_port_forward.go and cmd/mcp_port_forward.go:
	// for a remote environment, --dry-run must trace the kubectl
	// port-forward commands that would be started for both API and
	// MCP. With kubectl stubbed to NotFound the runtime helm upgrade
	// and the port-forward traces both appear from the same dry-run
	// output, so the scenario locks both paths in one golden.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/remote_dry_run_traces_port_forwards", normalize.Apply(result.Combined))
}

func testOpenRemoteDryRunAwsAliasPropagatesHostCredentials(t *testing.T) {
	t.Parallel()
	// Locks the deploy plumbing that ships host AWS credentials into a
	// remote runtime: attaching an AWS cloud alias to the env (the operator
	// opting it into acting on their behalf) makes the helm command include
	// `--set cloudContext.useHostCredentials=true` so the chart sets
	// AWS_PROFILE=erun-host on the runtime container. There is no separate
	// toggle — the alias association alone drives it. The
	// desktop refresher writes the matching profile into the pod's
	// ~/.aws/credentials at runtime — that path is tested in erun-mcp.
	// The alias here is deliberately not registered in the root config, so
	// this golden also locks the degraded arm of open's credential refresh:
	// an unresolvable alias warns and the session still opens.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithAWSAlias(t, setup, "team", "dev")
	envVars := stubKubectlNotFound(t, setup)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/remote_dry_run_aws_alias_propagates_host_credentials", normalize.Apply(result.Combined))
}

func testOpenDryRunConfiguredAwsAliasRefreshesHostCredentials(t *testing.T) {
	t.Parallel()
	// Injected host credentials are temporary and nothing else renews them,
	// so open refreshes them at the moment the operator declares they are
	// about to use the env. The plan must show the wait and the exec that
	// rewrites the erun-host profile, and the resolved region — this env has
	// a provider alias but no cloud context, the shape that produced both
	// the expired-credentials and the empty-AWS_REGION failures. The
	// run-state stub answers the pre-forward replica probe with a running
	// runtime — an unremarkable answer, because this scenario is about the
	// credential refresh, not about the wake branch its sibling locks.
	setup := env.New(t)
	fixture.SeedLocalTenantEnvWithAWSAlias(t, setup, "team", "dev", "ops+123456789012@aws", "eu-west-2", "")
	envVars := stubKubectlRunState(t, setup, 1, 1)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/dry_run_configured_aws_alias_refreshes_host_credentials", normalize.Apply(result.Combined))
}

// seedIntelliJOpenEnv seeds team/dev as a deployed remote environment whose
// SSHD forward answers ssh-keyscan with hostKey, plus the public key open
// installs and the IntelliJ options dir, and returns the stubbed environment.
func seedIntelliJOpenEnv(t *testing.T, setup env.Setup, stubsDir, hostKey, optionsDir string) []string {
	t.Helper()
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", 26100)
	sshDir := filepath.Join(setup.Home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("mkdir ~/.ssh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519.pub"), []byte("ssh-ed25519 AAAATESTPUB user@example\n"), 0o644); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
		DeploymentName: "team-devops",
		ContainerName:  "team-devops",
		RepoPath:       "/home/erun/git/team",
		SSHDEnabled:    true,
		MCPPort:        26100,
		SSHPort:        26122,
	})...)
	fixture.StubBinary(t, stubsDir, "ssh-keyscan", "[127.0.0.1]:26122 ssh-ed25519 "+hostKey)
	envVars = append(envVars, fixture.StubEnv(stubsDir, "ssh-keyscan")...)
	// Pre-create the IntelliJ *options* dir: production globs for
	// JetBrains/IntelliJIdea*/options (open_ide.go::intelliJOptionsCandidates)
	// and silently skips the jetbrainsconfig writers when no candidate
	// matches. Seeding only the version dir (without options/) is the
	// bug that kept internal/jetbrainsconfig at 0% while this scenario
	// stayed green.
	if err := os.MkdirAll(optionsDir, 0o755); err != nil {
		t.Fatalf("mkdir IntelliJ options: %v", err)
	}
	return envVars
}

// seedFakeIntelliJApp installs a fake IntelliJ IDEA.app: one lib jar so the
// Gateway classpath resolves, and a java shim that records its argv to the
// returned log.
func seedFakeIntelliJApp(t *testing.T, setup env.Setup) (contentsDir, javaLog string) {
	t.Helper()
	contentsDir = filepath.Join(setup.Home, "Applications", "IntelliJ IDEA.app", "Contents")
	if err := os.MkdirAll(filepath.Join(contentsDir, "lib"), 0o755); err != nil {
		t.Fatalf("mkdir IDEA lib: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentsDir, "lib", "app.jar"), []byte("jar"), 0o644); err != nil {
		t.Fatalf("write IDEA lib jar: %v", err)
	}
	javaLog = filepath.Join(setup.Cwd, "java-launcher.log")
	javaDir := filepath.Join(contentsDir, "jbr", "Contents", "Home", "bin")
	if err := os.MkdirAll(javaDir, 0o755); err != nil {
		t.Fatalf("mkdir IDEA jbr bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(javaDir, "java"),
		[]byte("#!/bin/sh\nprintf '%s\\n' \"$*\" > '"+javaLog+"'\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write java shim: %v", err)
	}
	return contentsDir, javaLog
}

// markIntelliJProjectOpenedWith injects the latestUsedIde block IntelliJ
// itself records after a successful remote session.
func markIntelliJProjectOpenedWith(t *testing.T, optionsDir, contentsDir string) {
	t.Helper()
	recentPath := filepath.Join(optionsDir, "sshRecentConnections.v2.xml")
	recentBody, err := os.ReadFile(recentPath)
	if err != nil {
		t.Fatalf("read sshRecentConnections.v2.xml: %v", err)
	}
	latestUsedIDE := `<RecentProjectState>
            <option name="latestUsedIde">
              <RecentProjectInstalledIde>
                <option name="buildNumber" value="243.22562.222" />
                <option name="pathToIde" value="` + contentsDir + `" />
                <option name="productCode" value="IU" />
              </RecentProjectInstalledIde>
            </option>`
	patched := strings.Replace(string(recentBody), "<RecentProjectState>", latestUsedIDE, 1)
	if patched == string(recentBody) {
		t.Fatalf("RecentProjectState not found in:\n%s", recentBody)
	}
	if err := os.WriteFile(recentPath, []byte(patched), 0o600); err != nil {
		t.Fatalf("patch sshRecentConnections.v2.xml: %v", err)
	}
}

func assertGatewayScaffoldingUnwritten(t *testing.T, gatewayCaches string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(gatewayCaches, "config", "info.xml")); !os.IsNotExist(err) {
		t.Errorf("expected dry-run to leave Gateway info.xml unwritten, stat err: %v", err)
	}
	if _, err := os.Stat(filepath.Join(gatewayCaches, "system")); !os.IsNotExist(err) {
		t.Errorf("expected dry-run to leave Gateway system dir uncreated, stat err: %v", err)
	}
}

func assertAdoptedPortForwardState(t *testing.T, setup env.Setup) {
	t.Helper()
	for kind, wantPID := range map[string]int{"mcp": 4242, "sshd": 4243} {
		stateBody, err := os.ReadFile(portForwardStateFile(setup, kind, "team", "dev"))
		if err != nil {
			t.Fatalf("read adopted %s state: %v", kind, err)
		}
		if !strings.Contains(string(stateBody), fmt.Sprintf(`"processId":%d`, wantPID)) {
			t.Errorf("expected adopted %s state to claim PID %d, got:\n%s", kind, wantPID, stateBody)
		}
	}
}

func testOpenIntellijGatewayAdoptsForwardsAndLaunchesGateway(t *testing.T) {
	// Covers the JetBrains Gateway launch path end to end plus foreign
	// port-forward adoption, in three passes over one seeded env:
	//   run 1 (real): registerIntelliJProject writes the JetBrains XML
	//     (no latestUsedIde yet, so the flow falls back to
	//     `open -a 'IntelliJ IDEA'`) and starts the port-forward
	//     simulators that the later passes adopt.
	//   patch: the test injects a latestUsedIde block into
	//     sshRecentConnections.v2.xml, exactly as IntelliJ itself would
	//     after a successful remote session, and deletes the per-env
	//     port-forward state files so the next pass cannot recognise the
	//     simulators as its own.
	//   run 2 (dry-run): with lsof/ps probes presenting run 1's live
	//     simulators as foreign kubectl port-forwards, the dry-run
	//     previews adoption ("would adopt"), traces the Gateway config
	//     scaffolding (mkdir/write-xml) WITHOUT creating it, and traces
	//     the java Gateway launch argv. The golden locks that plan.
	//   run 3 (real): adoption fires for MCP+SSHD ("adopted existing
	//     kubectl port-forward" + state files re-written), the Gateway
	//     scaffolding is created for real, and the fake java binary in
	//     the seeded IntelliJ IDEA.app records the
	//     jetbrains-gateway://connect URI.
	// Pinned to darwin: the standalone Gateway launch
	// (intelliJGatewayDarwinLaunchCommand) is macOS-only.
	skipIfPortsBusy(t, 26100, 26122, 26133)
	setup := env.New(t)
	stubsDir := filepath.Join(setup.Cwd, "stubs")
	optionsDir := filepath.Join(setup.Home, "Library", "Application Support", "JetBrains", "IntelliJIdea2024.3", "options")
	envVars := seedIntelliJOpenEnv(t, setup, stubsDir, "AAAAGATEWAYKEY=", optionsDir)
	contentsDir, javaLog := seedFakeIntelliJApp(t, setup)
	ideLog := filepath.Join(setup.Cwd, "ide-launcher.log")
	fixture.StubBinaryWithScript(t, stubsDir, "open",
		`printf '%s\n' "$*" >> '`+ideLog+`'`+"\n"+`exit 0`+"\n")
	envVars = append(envVars, "PATH="+stubsDir+string(os.PathListSeparator)+setup.PathDir)
	envVars = append(envVars, "ERUN_HOST_OS_OVERRIDE=darwin")

	run1 := erun.Run(t, []string{"open", "team", "dev", "--intellij", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if run1.ExitCode != 0 {
		t.Fatalf("run 1 exit %d: %s", run1.ExitCode, run1.Combined)
	}

	// Mark the project as previously opened with a concrete IDE, the
	// precondition for the Gateway URI path.
	markIntelliJProjectOpenedWith(t, optionsDir, contentsDir)
	// Drop the state files so the simulators read as foreign forwards.
	for _, kind := range []string{"mcp", "sshd"} {
		if err := os.Remove(portForwardStateFile(setup, kind, "team", "dev")); err != nil {
			t.Fatalf("remove %s port-forward state: %v", kind, err)
		}
	}
	stubAdoptHolderProbes(t, setup,
		adoptHolder{
			port: 26100, pid: 4242,
			argv: "kubectl --context test-context --namespace team-dev port-forward deployment/team-devops 26100:26100 --address 127.0.0.1",
		},
		adoptHolder{
			port: 26122, pid: 4243,
			argv: "kubectl --context test-context --namespace team-dev port-forward deployment/team-devops 26122:26122 --address 127.0.0.1",
		},
	)
	envVars = append(envVars, fixture.StubEnv(stubsDir, "lsof", "ps")...)

	run2 := erun.Run(t, []string{"open", "team", "dev", "--intellij", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if run2.ExitCode != 0 {
		t.Fatalf("run 2 exit %d: %s", run2.ExitCode, run2.Combined)
	}
	golden.Equal(t, "open/intellij_gateway_dry_run_previews_adoption_and_launch", normalize.Apply(run2.Combined))
	// The dry-run must not have created the Gateway launch scaffolding
	// (regression guard for the write-before-gate bug). The config dir
	// itself already exists — run 1's registerIntelliJProject writes
	// JetBrains options there for real — so assert on the two artifacts
	// only the launch path creates: config/info.xml and system/.
	gatewayCaches := filepath.Join(setup.Home, "Library", "Caches", "JetBrains", "IntelliJIdea2024.3", "tmp", "JetBrainsGateway")
	assertGatewayScaffoldingUnwritten(t, gatewayCaches)

	run3 := erun.Run(t, []string{"open", "team", "dev", "--intellij", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if run3.ExitCode != 0 {
		t.Fatalf("run 3 exit %d: %s", run3.ExitCode, run3.Combined)
	}
	golden.Equal(t, "open/intellij_gateway_real_run_adopts_and_launches", normalize.Apply(run3.Combined))
	assertAdoptedPortForwardState(t, setup)
	javaArgs := waitForFile(t, javaLog, 5*time.Second)
	if !strings.Contains(javaArgs, "jetbrains-gateway://connect#") {
		t.Errorf("expected java shim to receive a jetbrains-gateway connect URI, got:\n%s", javaArgs)
	}
	if _, err := os.Stat(filepath.Join(gatewayCaches, "config", "info.xml")); err != nil {
		t.Errorf("expected real run to create the Gateway config scaffolding: %v", err)
	}
}
