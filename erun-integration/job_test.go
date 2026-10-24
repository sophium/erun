package integration

import (
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

func TestJob(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"help", testJobHelp},
		{"start_refuses_a_dir_that_does_not_exist_by_name", testJobStartRefusesADirThatDoesNotExistByName},
		{"start_runs_the_work_in_a_relative_dir", testJobStartRunsTheWorkInARelativeDir},
		{"await_help", testJobAwaitHelp},
		{"status_help", testJobStatusHelp},
		{"start_dry_run_plans_the_detach", testJobStartDryRunPlansTheDetach},
		{"start_env_dry_run_plans_the_supervisor_argv", testJobStartEnvDryRunPlansTheSupervisorArgv},
		{"start_handoff_dry_run_plans_the_supervisor_argv", testJobStartHandoffDryRunPlansTheSupervisorArgv},
		{"start_exclusive_dry_run_plans_the_environment_claim", testJobStartExclusiveDryRunPlansTheEnvironmentClaim},
		{"start_exclusive_off_environment_dry_run_forwards_the_claim_to_the_edge", testJobStartExclusiveOffEnvironmentDryRunForwardsTheClaimToTheEdge},
		{"agent_start_exclusive_off_environment_dry_run_forwards_the_claim_to_the_edge", testJobAgentStartExclusiveOffEnvironmentDryRunForwardsTheClaimToTheEdge},
		{"an_exclusive_job_refuses_every_other_job_start_and_names_the_holder", testJobAnExclusiveJobRefusesEveryOtherJobStartAndNamesTheHolder},
		{"an_expired_exclusive_claim_is_reclaimed_rather_than_pinning_the_environment", testJobAnExpiredExclusiveClaimIsReclaimedRatherThanPinningTheEnvironment},
		{"a_job_started_by_the_holder_runs_under_its_claim_instead_of_being_refused", testJobAJobStartedByTheHolderRunsUnderItsClaimInsteadOfBeingRefused},
		{"start_env_refuses_a_name_that_could_redirect_the_job", testJobStartEnvRefusesANameThatCouldRedirectTheJob},
		{"start_env_reaches_the_jobs_own_process", testJobStartEnvReachesTheJobsOwnProcess},
		{"start_help", testJobStartHelp},
		{"agent_start_dry_run_plans_the_streaming_invocation", testJobAgentStartDryRunPlansTheStreamingInvocation},
		{"agent_start_refuses_when_the_tool_is_not_available", testJobAgentStartRefusesWhenTheToolIsNotAvailable},
		{"agent_progress_reports_an_authentication_failure_as_the_reason", testJobAgentProgressReportsAnAuthenticationFailureAsTheReason},
		{"agent_progress_reports_a_gateway_reasoning_refusal_as_the_reason", testJobAgentProgressReportsAGatewayReasoningRefusalAsTheReason},
		{"agent_start_refuses_an_unsupported_tool_or_a_command", testJobAgentStartRefusesAnUnsupportedToolOrACommand},
		{"agent_progress_is_readable_while_the_agent_works", testJobAgentProgressIsReadableWhileTheAgentWorks},
		{"agent_progress_is_the_same_shape_for_every_tool", testJobAgentProgressIsTheSameShapeForEveryTool},
		{"await_reports_a_timeout_distinctly_from_a_failure", testJobAwaitReportsATimeoutDistinctlyFromAFailure},
		{"status_reports_a_non_zero_exit_captured_in_the_environment", testJobStatusReportsANonZeroExitCapturedInTheEnvironment},
		{"status_names_why_a_failed_job_failed_instead_of_only_its_exit_code", testJobStatusNamesWhyAFailedJobFailedInsteadOfOnlyItsExitCode},
		{"output_is_readable_while_the_job_runs", testJobOutputIsReadableWhileTheJobRuns},
		{"a_running_job_holds_an_activity_lease", testJobARunningJobHoldsAnActivityLease},
		{"cancel_signals_the_recorded_process_and_records_the_outcome", testJobCancelSignalsTheRecordedProcessAndRecordsTheOutcome},
		{"an_abandoned_job_reads_as_unknown_never_as_success", testJobAnAbandonedJobReadsAsUnknownNeverAsSuccess},
		{"an_abandoned_job_on_the_same_pod_names_a_container_restart_via_kubectl_subprocess", testJobAnAbandonedJobOnTheSamePodNamesAContainerRestartViaKubectlSubprocess},
		{"an_abandoned_job_on_the_same_pod_names_a_container_restart_via_kubectl_pod_get_library_execution_mode", testJobAnAbandonedJobOnTheSamePodNamesAContainerRestartViaKubectlPodGetLibraryExecutionMode},
		{"a_job_that_backgrounds_work_and_exits_reads_as_abandoned_not_success", testJobAJobThatBackgroundsWorkAndExitsReadsAsAbandonedNotSuccess},
		{"a_job_that_ends_while_a_job_it_started_is_still_running_reads_as_gate_incomplete", testJobAJobThatEndsWhileAJobItStartedIsStillRunningReadsAsGateIncomplete},
		{"a_job_that_waits_for_a_job_it_started_reports_the_real_outcome_once_it_finishes", testJobAJobThatWaitsForAJobItStartedReportsTheRealOutcomeOnceItFinishes},
		{"a_job_that_waits_for_a_failed_job_it_started_surfaces_startedJobFailed", testJobAJobThatWaitsForAFailedJobItStartedSurfacesStartedJobFailed},
		{"startedJobFailed_ignores_a_superseded_earlier_attempt_under_the_same_name", testJobStartedJobFailedIgnoresASupersededEarlierAttemptUnderTheSameName},
		{"a_job_that_ends_while_a_background_task_job_it_started_is_still_running_reads_as_gate_incomplete", testJobAJobThatEndsWhileABackgroundTaskJobItStartedIsStillRunningReadsAsGateIncomplete},
		{"a_job_that_waits_for_a_failed_background_task_job_it_started_surfaces_startedJobFailed_and_leaves_its_log_readable", testJobAJobThatWaitsForAFailedBackgroundTaskJobItStartedSurfacesStartedJobFailedAndLeavesItsLogReadable},
		{"a_task_job_started_with_no_parent_stays_parentless", testJobATaskJobStartedWithNoParentStaysParentless},
		{"an_agent_jobs_started_gate_that_failed_gets_one_bounded_resumed_turn_that_fixes_it", testJobAnAgentJobsStartedGateThatFailedGetsOneBoundedResumedTurnThatFixesIt},
		{"an_agent_jobs_started_gate_that_keeps_failing_stops_at_the_reinvocation_bound", testJobAnAgentJobsStartedGateThatKeepsFailingStopsAtTheReinvocationBound},
		{"handoff_excludes_a_job_from_its_parents_finish_check", testJobHandoffExcludesAJobFromItsParentsFinishCheck},
		{"an_agent_job_that_ends_with_an_uncommitted_working_tree_is_checkpointed_pushed_and_not_reported_as_success", testJobAnAgentJobThatEndsWithAnUncommittedWorkingTreeIsCheckpointedPushedAndNotReportedAsSuccess},
		{"an_agent_jobs_clean_pushed_clone_under_the_work_root_is_reclaimed_after_it_finishes", testJobAnAgentJobsCleanPushedCloneUnderTheWorkRootIsReclaimedAfterItFinishes},
		{"an_agent_jobs_dirty_detached_clone_is_kept_and_the_reason_is_named", testJobAnAgentJobsDirtyDetachedCloneIsKeptAndTheReasonIsNamed},
		{"a_job_started_on_a_different_pod_reads_back_as_pod_replaced_not_a_guess", testJobAJobStartedOnADifferentPodReadsBackAsPodReplacedNotAGuess},
		{"attach_tracks_a_pid_without_claiming_an_outcome", testJobAttachTracksAPidWithoutClaimingAnOutcome},
		{"start_refuses_to_reuse_a_running_id", testJobStartRefusesToReuseARunningId},
		{"status_of_an_unknown_id_names_the_environment", testJobStatusOfAnUnknownIdNamesTheEnvironment},
		{"status_without_jobs_says_so", testJobStatusWithoutJobsSaysSo},
		{"status_skips_a_corrupted_record_and_orders_ties_by_id", testJobStatusSkipsACorruptedRecordAndOrdersTiesById},
		{"start_requires_target_and_name", testJobStartRequiresTargetAndName},
		{"await_refuses_an_unbounded_timeout", testJobAwaitRefusesAnUnboundedTimeout},
		{"cancel_dry_run_names_the_target_without_signalling", testJobCancelDryRunNamesTheTargetWithoutSignalling},
		{"output_past_the_cap_is_reported_as_truncated", testJobOutputPastTheCapIsReportedAsTruncated},
		{"agent_progress_keeps_moving_after_the_output_cap", testJobAgentProgressKeepsMovingAfterTheOutputCap},
	} {
		t.Run(scenario.name, scenario.run)
	}
}

func testJobHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"job", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "job/help", normalize.Apply(result.Combined))
}

func testJobStartRefusesADirThatDoesNotExistByName(t *testing.T) {
	// An unusable working directory used to reach exec, where Go reports it
	// as an ENOENT naming the *binary* — so a dir the supervisor could not
	// resolve surfaced as a missing shell and sent the reader after a broken
	// image instead of a bad path.
	setup := env.New(t)
	envVars := jobStubEnv(t, setup, "printf '"+jobStubSignal+"'")
	result := startJob(t, setup, envVars, "baddir", "--dir", "no-such-subdir", "--", "work")
	if result.ExitCode == 0 {
		t.Fatalf("expected a refusal for a missing dir, got exit 0:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "no-such-subdir") {
		t.Fatalf("the refusal must name the dir, got:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "fork/exec") {
		t.Fatalf("a bad dir must not surface as an exec failure, got:\n%s", result.Combined)
	}
}

func testJobStartRunsTheWorkInARelativeDir(t *testing.T) {
	// The supervisor is detached with no inherited working directory, so a
	// relative dir has to be anchored before it is handed over, the same way
	// every other repo-facing surface reads a path.
	setup := env.New(t)
	workdir := filepath.Join(setup.Cwd, "workdir-sub")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("create the work directory: %v", err)
	}
	envVars := jobStubEnv(t, setup, "pwd > pwd.txt")
	result := startJob(t, setup, envVars, "reldir", "--dir", "workdir-sub", "--", "work")
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	got := strings.TrimSpace(waitForFile(t, filepath.Join(workdir, "pwd.txt"), 30*time.Second))
	if filepath.Base(got) != "workdir-sub" {
		t.Fatalf("work ran in %q, want it resolved to %s", got, workdir)
	}
}

func testJobAwaitHelp(t *testing.T) {
	// The await exit codes are the contract an orchestrator branches on, so the
	// help that states them is locked here.
	setup := env.New(t)
	result := erun.Run(t, []string{"job", "await", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "job/await_help", normalize.Apply(result.Combined))
}

func testJobStatusHelp(t *testing.T) {
	// The job states -- including abandoned/gate-incomplete, and that an
	// agent job's own process exiting is not the verdict -- are the
	// contract an orchestrator reads this help for, so it is locked here.
	setup := env.New(t)
	result := erun.Run(t, []string{"job", "status", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "job/status_help", normalize.Apply(result.Combined))
}

func testJobStartDryRunPlansTheDetach(t *testing.T) {
	// The plan must show the supervisor argv, where output will land, and that
	// a lease is taken, without starting anything.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := jobStubEnv(t, setup, "exit 0")
	result := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "suite", "--dry-run", "--", "work", "--all"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "job/start_dry_run_plans_the_detach", normalize.Apply(result.Combined))
	if _, err := os.Stat(jobRecordPath(setup, "team", "dev", "suite")); !os.IsNotExist(err) {
		t.Errorf("a dry-run start must not register a job, stat err: %v", err)
	}
}

func testJobStartEnvDryRunPlansTheSupervisorArgv(t *testing.T) {
	// --env must show up in the supervisor argv the plan traces, sorted
	// so the plan is deterministic regardless of flag order.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := jobStubEnv(t, setup, "exit 0")
	result := erun.Run(t, []string{
		"job", "start", "--tenant", "team", "--environment", "dev", "--name", "suite", "--dry-run",
		"--env", "CLAUDE_CODE_MAX_OUTPUT_TOKENS=64000", "--env", "SOME_FLAG=1",
		"--", "work",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "job/start_env_dry_run_plans_the_supervisor_argv", normalize.Apply(result.Combined))
}

func testJobStartHandoffDryRunPlansTheSupervisorArgv(t *testing.T) {
	// --handoff must reach the supervisor argv, the same way --env does above,
	// so the deliberate-handoff record actually lands: without --handoff on
	// the supervisor's own invocation, the started job would default into its
	// parent's finish check like any other.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := jobStubEnv(t, setup, "exit 0")
	result := erun.Run(t, []string{
		"job", "start", "--tenant", "team", "--environment", "dev", "--name", "release", "--dry-run", "--handoff",
		"--", "work",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "job/start_handoff_dry_run_plans_the_supervisor_argv", normalize.Apply(result.Combined))
}

func testJobStartEnvRefusesANameThatCouldRedirectTheJob(t *testing.T) {
	// PATH/LD_PRELOAD/etc. are refused up front: letting a caller
	// override them would let it redirect what the job's own process executes
	// or loads, which is a hijack rather than a tuning knob.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := jobStubEnv(t, setup, "exit 0")
	result := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "suite", "--dry-run", "--env", "PATH=/tmp/evil", "--", "work"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a refusal for --env PATH=..., got 0:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, `"PATH"`) {
		t.Fatalf("the refusal must name the rejected var, got:\n%s", result.Combined)
	}
	if _, err := os.Stat(jobRecordPath(setup, "team", "dev", "suite")); !os.IsNotExist(err) {
		t.Errorf("a refused start must not register a job, stat err: %v", err)
	}
}

func testJobStartEnvReachesTheJobsOwnProcess(t *testing.T) {
	// The real point of --env: the value is not just traced, it is set
	// on the job's actual process, on top of what it inherits.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := jobStubEnv(t, setup, `printf '%s' "$SOME_JOB_VAR" > seen.txt`)
	result := startJob(t, setup, envVars, "envcheck", "--env", "SOME_JOB_VAR=hello-job", "--", "work")
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	got := waitForFile(t, filepath.Join(setup.Cwd, "seen.txt"), 30*time.Second)
	if got != "hello-job" {
		t.Fatalf("job process saw SOME_JOB_VAR=%q, want %q", got, "hello-job")
	}
}

func testJobStartHelp(t *testing.T) {
	// The agent switch and what it does to the invocation are only discoverable
	// here, so the help that states them is locked.
	setup := env.New(t)
	result := erun.Run(t, []string{"job", "start", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "job/start_help", normalize.Apply(result.Combined))
}

func testJobStartRefusesToReuseARunningId(t *testing.T) {
	// Two supervisors writing one record would make every later answer
	// ambiguous, so the collision is refused rather than resolved.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	release := filepath.Join(setup.Cwd, "release")
	envVars := jobStubEnv(t, setup, "while [ ! -f '"+release+"' ]; do sleep 0.05; done\nexit 0")

	first := startJob(t, setup, envVars, "suite", "--", "work")
	if first.ExitCode != 0 {
		t.Fatalf("first start: exit %d: %s", first.ExitCode, first.Combined)
	}
	second := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "suite", "--", "work"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if second.ExitCode == 0 {
		t.Fatalf("expected the colliding start to fail, got 0:\n%s", second.Combined)
	}
	golden.Equal(t, "job/start_refuses_to_reuse_a_running_id", normalize.Apply(second.Combined))

	if err := os.WriteFile(release, []byte("go\n"), 0o644); err != nil {
		t.Fatalf("release the stub: %v", err)
	}
	done := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "suite", "--timeout", "30s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if done.ExitCode != 0 {
		t.Fatalf("await: exit %d: %s", done.ExitCode, done.Combined)
	}
}

func testJobStartRequiresTargetAndName(t *testing.T) {
	setup := env.New(t)
	missingTarget := erun.Run(t, []string{"job", "start", "--name", "suite", "--", "work"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if missingTarget.ExitCode == 0 {
		t.Fatalf("expected non-zero exit without target flags, got 0:\n%s", missingTarget.Combined)
	}
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	missingName := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--", "work"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if missingName.ExitCode == 0 {
		t.Fatalf("expected non-zero exit without a name, got 0:\n%s", missingName.Combined)
	}
	golden.Equal(t, "job/start_requires_target_and_name", normalize.Apply(missingTarget.Combined+missingName.Combined))
}

func testJobStartExclusiveDryRunPlansTheEnvironmentClaim(t *testing.T) {
	// --exclusive is a decision the plan has to show, not only an effect at
	// run time: the claim it takes is what will refuse every other job start
	// here, and --exclusive must reach the supervisor argv or the supervisor
	// would neither renew the claim nor release it on the way out.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := jobStubEnv(t, setup, "exit 0")
	result := erun.Run(t, []string{
		"job", "start", "--tenant", "team", "--environment", "dev", "--name", "check", "--dry-run", "--exclusive",
		"--", "work",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "job/start_exclusive_dry_run_plans_the_environment_claim", normalize.Apply(result.Combined))
	if _, err := os.Stat(jobRecordPath(setup, "team", "dev", "check")); !os.IsNotExist(err) {
		t.Errorf("a dry-run start must not register a job, stat err: %v", err)
	}
}

func testJobStartExclusiveOffEnvironmentDryRunForwardsTheClaimToTheEdge(t *testing.T) {
	// The scenario above proves the claim once job start reaches
	// StartEnvironmentJob directly, which only happens in-environment.
	// Off-environment -- an operator's own machine, or any other pod,
	// targeting this one through its MCP edge -- job start never calls
	// that function directly: it calls the edge's exec_raw tool instead,
	// and --exclusive never reached that call's arguments at all until
	// this fix, so the edge enforced nothing while the caller was told
	// the job started. No `inEnvironment` here is the point: this is the
	// host-caller path.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{
		"job", "start", "--tenant", "team", "--environment", "dev", "--name", "check", "--dry-run", "--exclusive",
		"--", "work",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "job/start_exclusive_off_environment_dry_run_forwards_the_claim_to_the_edge", normalize.Apply(result.Combined))
}

func testJobAgentStartExclusiveOffEnvironmentDryRunForwardsTheClaimToTheEdge(t *testing.T) {
	// Same gap, the other off-environment dispatch tool: an agent job
	// started off-environment goes through exec_agent rather than
	// exec_raw, and it had the identical missing forward.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{
		"job", "start", "--tenant", "team", "--environment", "dev", "--name", "sweep", "--agent", "claude", "--exclusive", "--dry-run",
		"--", "fix the failing tests",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "job/agent_start_exclusive_off_environment_dry_run_forwards_the_claim_to_the_edge", normalize.Apply(result.Combined))
}

func testJobAnExclusiveJobRefusesEveryOtherJobStartAndNamesTheHolder(t *testing.T) {
	// The whole point of the claim, and the asymmetry that makes it worth
	// more than a mutex between exclusive jobs: while a gate holds the
	// environment, an ordinary job start is refused too. Scheduling probe
	// jobs beside a running gate is exactly what turned a 7-minute green
	// run into a 17-minute one and two reds on tests that pass standalone.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	release := filepath.Join(setup.Cwd, "release")
	envVars := jobStubEnv(t, setup, "while [ ! -f '"+release+"' ]; do sleep 0.05; done\nexit 0")

	start := startJob(t, setup, envVars, "gate", "--exclusive", "--", "work")
	if start.ExitCode != 0 {
		t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
	}
	held := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "gate"},
		erun.RunOptions{Cwd: setup.Cwd, Env: envVars})

	plain := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "probe", "--", "work"},
		erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if plain.ExitCode == 0 {
		t.Fatalf("an ordinary job start must be refused while a gate holds the environment, got 0:\n%s", plain.Combined)
	}
	if _, err := os.Stat(jobRecordPath(setup, "team", "dev", "probe")); !os.IsNotExist(err) {
		t.Errorf("a refused start must register nothing, stat err: %v", err)
	}
	second := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "gate-two", "--exclusive", "--", "work"},
		erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if second.ExitCode == 0 {
		t.Fatalf("a second exclusive job start must be refused, got 0:\n%s", second.Combined)
	}

	if err := os.WriteFile(release, []byte("go\n"), 0o644); err != nil {
		t.Fatalf("release the stub: %v", err)
	}
	await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "gate", "--timeout", "30s"},
		erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if await.ExitCode != 0 {
		t.Fatalf("await: exit %d: %s", await.ExitCode, await.Combined)
	}
	// Released on the way out rather than left to lapse, so the next gate
	// starts immediately instead of waiting out a TTL.
	after := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "probe", "--", "work"},
		erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if after.ExitCode != 0 {
		t.Fatalf("a job start after the exclusive job finished must succeed: exit %d: %s", after.ExitCode, after.Combined)
	}
	t.Cleanup(func() {
		stopJob(t, setup, envVars, "probe")
	})
	golden.Equal(t, "job/an_exclusive_job_refuses_every_other_job_start_and_names_the_holder",
		normalize.Apply(held.Combined+plain.Combined+second.Combined))
}

func testJobAnExpiredExclusiveClaimIsReclaimedRatherThanPinningTheEnvironment(t *testing.T) {
	// The requirement that keeps this from being a lock that can strand an
	// environment: a claim nobody renews stops holding anything. Taken with
	// a TTL that has already elapsed by the time the next read happens, so
	// the reclaim is observable without waiting for one.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := jobStubEnv(t, setup, "exit 0")
	take := erun.Run(t, []string{
		"activity", "lease", "take", "--tenant", "team", "--environment", "dev",
		"--name", "abandoned-gate", "--id", "abandoned-gate", "--exclusive", "--scope", "environment", "--ttl", "1ns",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if take.ExitCode != 0 {
		t.Fatalf("take: exit %d: %s", take.ExitCode, take.Combined)
	}
	result := erun.Run(t, []string{
		"job", "start", "--tenant", "team", "--environment", "dev", "--name", "gate", "--exclusive", "--dry-run", "--", "work",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("a lapsed claim must not refuse a new one: exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "job/an_expired_exclusive_claim_is_reclaimed_rather_than_pinning_the_environment",
		normalize.Apply(result.Combined))
}

func testJobAJobStartedByTheHolderRunsUnderItsClaimInsteadOfBeingRefused(t *testing.T) {
	// Lineage is the one exemption, and it has to exist: a gate job that
	// detaches its own nested work (a gate started from inside an agent job)
	// would otherwise be refused by its own ancestor's claim, which would
	// make the mechanism unusable exactly where it is needed. The nested job
	// takes no second claim -- releasing one on its way out would drop the
	// ancestor's.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	release := filepath.Join(setup.Cwd, "release")
	envVars := jobStubEnv(t, setup, "while [ ! -f '"+release+"' ]; do sleep 0.05; done\nexit 0")

	start := startJob(t, setup, envVars, "gate", "--exclusive", "--", "work")
	if start.ExitCode != 0 {
		t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
	}
	// ERUN_JOB_ID is what the gate's own work inherits, so a start made from
	// inside it names the gate as its parent exactly as a real nested start
	// would.
	nested := erun.Run(t, []string{
		"job", "start", "--tenant", "team", "--environment", "dev", "--name", "nested", "--dry-run", "--", "work",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: append(envVars, "ERUN_JOB_ID=gate")})
	if nested.ExitCode != 0 {
		t.Fatalf("the holder's own nested start must not be refused: exit %d: %s", nested.ExitCode, nested.Combined)
	}
	if err := os.WriteFile(release, []byte("go\n"), 0o644); err != nil {
		t.Fatalf("release the stub: %v", err)
	}
	golden.Equal(t, "job/a_job_started_by_the_holder_runs_under_its_claim_instead_of_being_refused",
		normalize.Apply(nested.Combined))
}
