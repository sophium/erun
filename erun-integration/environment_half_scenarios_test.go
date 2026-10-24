package integration

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// Off-environment scenarios: idle/job/lease act on the environment through its
// edge. Each pins its own port so fake edges never collide.
const (
	idleEdgeLocalPort  = 26200
	jobEdgeLocalPort   = 26300
	leaseEdgeLocalPort = 26400
)

const idleStatusPayload = `{"content":[{"type":"text","text":"idle"}],"structuredContent":{` +
	`"policy":{"timeout":300000000000,"workingHours":"08:00-20:00","idleTrafficBytes":0},` +
	`"managedCloud":true,"stopEligible":false,"stopBlockedReason":"a lease is held",` +
	`"secondsUntilStop":1796,` +
	`"leases":[{"id":"suite","name":"suite","expiresAt":"2099-01-01T00:00:00Z"}],` +
	`"markers":[{"name":"lease","idle":false,"reason":"held by suite","secondsRemaining":1796},` +
	`{"name":"ssh","idle":true,"reason":"last activity exceeded timeout"}]}}`

func TestIdleOffEnvironment(t *testing.T) {
	t.Run("reports_the_environments_own_answer", func(t *testing.T) {
		// A status read from the caller's own store would say "idle" regardless.
		skipIfPortsBusy(t, idleEdgeLocalPort)
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", idleEdgeLocalPort)
		fixture.SeedDesktopIdentity(t, setup)
		edge := &fakeMCPEdge{Results: map[string]string{"tools/call": idleStatusPayload}}
		edge.start(t, idleEdgeLocalPort)

		result := erun.Run(t, []string{"idle", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "idle/off_environment_reports_the_environments_own_answer", normalize.Apply(result.Combined))

		// An idle check must not count as activity, or polling keeps the env awake.
		call := edge.requestFor(t, "tools/call")
		if !call.IdleProbe {
			t.Errorf("expected the idle call to carry the idle-probe header so it does not reset the environment's idle timer")
		}
	})

	t.Run("unreachable_edge_is_not_reported_as_idle", func(t *testing.T) {
		skipIfPortsBusy(t, idleEdgeLocalPort)
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", idleEdgeLocalPort)
		fixture.SeedDesktopIdentity(t, setup)

		result := erun.Run(t, []string{"idle", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for an unreachable edge, got 0:\n%s", result.Combined)
		}
		if strings.Contains(result.Combined, "lease: idle") {
			t.Errorf("an unreachable environment was reported as idle:\n%s", result.Combined)
		}
		golden.Equal(t, "idle/off_environment_unreachable_edge_is_not_reported_as_idle", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_traces_the_environment_call", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"idle", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "idle/off_environment_dry_run_traces_the_environment_call", normalize.Apply(result.Combined))
	})
}

func TestJobOffEnvironment(t *testing.T) {
	t.Run("start_runs_the_work_in_the_environment", testJobOffEnvironmentStartRunsTheWorkInTheEnvironment)
	t.Run("start_forwards_this_process_job_id_as_startedByJobId", testJobOffEnvironmentStartForwardsThisProcessJobIdAsStartedByJobId)
	t.Run("start_dry_run_traces_the_environment_call", testJobOffEnvironmentStartDryRunTracesTheEnvironmentCall)
	t.Run("a_malformed_request_fails_before_reaching_the_edge", testJobOffEnvironmentAMalformedRequestFailsBeforeReachingTheEdge)
	t.Run("await_maps_the_environments_outcome_onto_the_exit_code", testJobOffEnvironmentAwaitMapsTheEnvironmentsOutcomeOntoTheExitCode)
	t.Run("status_reads_the_environments_job_state", testJobOffEnvironmentStatusReadsTheEnvironmentsJobState)
	t.Run("output_reads_the_environments_captured_output", testJobOffEnvironmentOutputReadsTheEnvironmentsCapturedOutput)
	t.Run("attach_tracks_the_environments_process", testJobOffEnvironmentAttachTracksTheEnvironmentsProcess)
	t.Run("attach_a_malformed_request_fails_before_reaching_the_edge", testJobOffEnvironmentAttachAMalformedRequestFailsBeforeReachingTheEdge)
	t.Run("cancel_signals_the_environments_job", testJobOffEnvironmentCancelSignalsTheEnvironmentsJob)
	t.Run("start_agent_runs_the_agent_in_the_environment", testJobOffEnvironmentStartAgentRunsTheAgentInTheEnvironment)
	t.Run("status_lists_every_job_when_no_id_is_given", testJobOffEnvironmentStatusListsEveryJobWhenNoIdIsGiven)

	t.Run("status_reports_the_tool_and_the_remedy_when_the_target_is_unresolved", testJobOffEnvironmentStatusReportsTheToolAndTheRemedyWhenTheTargetIsUnresolved)
}

func testJobOffEnvironmentStartRunsTheWorkInTheEnvironment(t *testing.T) {
	// A job started locally would hold no lease against the environment's idle-stop.
	skipIfPortsBusy(t, jobEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", jobEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	// wait:false only confirms the start, so the CLI follows up with exec_job_status.
	edge := &fakeMCPEdge{ToolResults: map[string]string{
		"exec_raw": `{"content":[{"type":"text","text":"started"}],` +
			`"structuredContent":{"executed":true,"jobId":"suite","state":"running","wait":false}}`,
		"exec_job_status": `{"content":[{"type":"text","text":"status"}],` +
			`"structuredContent":{"tenant":"team","environment":"dev","job":{` +
			`"id":"suite","name":"suite","state":"running","childPid":4242,` +
			`"logPath":"/home/erun/.cache/erun/activity/team/dev/jobs/suite.log",` +
			`"leaseId":"job-suite","outputBytes":0}}}`,
	}}
	edge.start(t, jobEdgeLocalPort)

	result := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "suite", "--", "work"},
		erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Stdout, setup.Home) {
		t.Errorf("the job reported a path on the caller's machine:\n%s", result.Stdout)
	}
	golden.Equal(t, "job/off_environment_start_runs_the_work_in_the_environment", normalize.Apply(result.Combined))

	calls := edge.requestsFor("tools/call")
	if len(calls) != 2 {
		t.Fatalf("expected exec_raw then exec_job_status, got %d tools/call requests: %+v", len(calls), calls)
	}
	if calls[0].Tool != "exec_raw" {
		t.Fatalf("the environment was asked for %q first, want exec_raw", calls[0].Tool)
	}
	if calls[0].IdleProbe {
		t.Errorf("exec_raw must not carry the idle-probe header: it starts real work in the environment")
	}
	if calls[1].Tool != "exec_job_status" {
		t.Fatalf("the environment was asked for %q second, want exec_job_status", calls[1].Tool)
	}
	for _, call := range calls {
		assertToolArgumentsNameTarget(t, call, "team", "dev")
	}
}

func testJobOffEnvironmentStartForwardsThisProcessJobIdAsStartedByJobId(t *testing.T) {
	// The edge process has no ERUN_JOB_ID to inherit, so the parent is passed explicitly.
	skipIfPortsBusy(t, jobEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", jobEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{ToolResults: map[string]string{
		"exec_raw": `{"content":[{"type":"text","text":"started"}],` +
			`"structuredContent":{"executed":true,"jobId":"suite","state":"running","wait":false}}`,
		"exec_job_status": `{"content":[{"type":"text","text":"status"}],` +
			`"structuredContent":{"tenant":"team","environment":"dev","job":{` +
			`"id":"suite","name":"suite","state":"running","childPid":4242,` +
			`"logPath":"/home/erun/.cache/erun/activity/team/dev/jobs/suite.log",` +
			`"leaseId":"job-suite","outputBytes":0}}}`,
	}}
	edge.start(t, jobEdgeLocalPort)

	envVars := append(append([]string{}, setup.Env()...), "ERUN_JOB_ID=upstream-job")
	result := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "suite", "--", "work"},
		erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	calls := edge.requestsFor("tools/call")
	if len(calls) == 0 || calls[0].Tool != "exec_raw" {
		t.Fatalf("expected exec_raw as the first tools/call request, got %+v", calls)
	}
	if got := calls[0].Arguments["startedByJobId"]; got != "upstream-job" {
		t.Fatalf("expected exec_raw to carry startedByJobId %q from this process's own ERUN_JOB_ID, got %v (arguments: %+v)", "upstream-job", got, calls[0].Arguments)
	}
}

func testJobOffEnvironmentStartDryRunTracesTheEnvironmentCall(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "suite", "--dry-run", "--", "work", "--all"},
		erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "job/off_environment_start_dry_run_traces_the_environment_call", normalize.Apply(result.Combined))
}

func testJobOffEnvironmentAMalformedRequestFailsBeforeReachingTheEdge(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--", "work"},
		erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for a nameless job, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "job/off_environment_a_malformed_request_fails_before_reaching_the_edge", normalize.Apply(result.Combined))
}

func testJobOffEnvironmentAwaitMapsTheEnvironmentsOutcomeOntoTheExitCode(t *testing.T) {
	skipIfPortsBusy(t, jobEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", jobEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{Results: map[string]string{"tools/call": `{"content":[{"type":"text","text":"waited"}],` +
		`"structuredContent":{"timedOut":true,"timeoutSeconds":30,` +
		`"job":{"id":"suite","name":"suite","state":"running","childPid":4242,"outputBytes":0}}}`}}
	edge.start(t, jobEdgeLocalPort)

	result := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "suite", "--timeout", "30s"},
		erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 124 {
		t.Fatalf("exit %d, want 124 for a job still running: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "job/off_environment_await_maps_the_environments_outcome_onto_the_exit_code", normalize.Apply(result.Combined))

	call := edge.requestFor(t, "tools/call")
	if !call.IdleProbe {
		t.Errorf("expected job_await to carry the idle-probe header")
	}
}

func testJobOffEnvironmentStatusReadsTheEnvironmentsJobState(t *testing.T) {
	skipIfPortsBusy(t, jobEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", jobEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{Results: map[string]string{"tools/call": `{"content":[{"type":"text","text":"status"}],` +
		`"structuredContent":{"tenant":"team","environment":"dev",` +
		`"job":{"id":"suite","name":"suite","state":"running","childPid":4242,"outputBytes":0},` +
		`"jobs":[{"id":"suite","name":"suite","state":"running","childPid":4242,"outputBytes":0}]}}`}}
	edge.start(t, jobEdgeLocalPort)

	result := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "suite"},
		erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}

	call := edge.requestFor(t, "tools/call")
	if call.Tool != "exec_job_status" {
		t.Fatalf("the environment was asked for %q, want exec_job_status", call.Tool)
	}
	if !call.IdleProbe {
		t.Errorf("expected job_status to carry the idle-probe header so polling it does not reset the environment's idle timer")
	}
	assertToolArgumentsNameTarget(t, call, "team", "dev")
}

func testJobOffEnvironmentOutputReadsTheEnvironmentsCapturedOutput(t *testing.T) {
	skipIfPortsBusy(t, jobEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", jobEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{Results: map[string]string{"tools/call": `{"content":[{"type":"text","text":"output"}],` +
		`"structuredContent":{"output":"hello\n","offset":0,"nextOffset":6,"hasMore":false,"complete":true}}`}}
	edge.start(t, jobEdgeLocalPort)

	result := erun.Run(t, []string{"job", "output", "--tenant", "team", "--environment", "dev", "--id", "suite"},
		erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}

	call := edge.requestFor(t, "tools/call")
	if call.Tool != "exec_job_output" {
		t.Fatalf("the environment was asked for %q, want exec_job_output", call.Tool)
	}
	if !call.IdleProbe {
		t.Errorf("expected job_output to carry the idle-probe header so polling it does not reset the environment's idle timer")
	}
	assertToolArgumentsNameTarget(t, call, "team", "dev")
}

func testJobOffEnvironmentAttachTracksTheEnvironmentsProcess(t *testing.T) {
	skipIfPortsBusy(t, jobEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", jobEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{Results: map[string]string{"tools/call": `{"content":[{"type":"text","text":"attached"}],` +
		`"structuredContent":{"tenant":"team","environment":"dev",` +
		`"job":{"id":"overnight-index","name":"overnight-index","state":"running","pid":4242,"leaseId":"job-overnight-index","outputBytes":0},"executed":true}}`}}
	edge.start(t, jobEdgeLocalPort)

	result := erun.Run(t, []string{"job", "attach", "--tenant", "team", "--environment", "dev", "--name", "overnight-index", "--pid", "4242"},
		erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Stdout, "job attached: overnight-index") {
		t.Errorf("expected the environment's attached job to be reported, got:\n%s", result.Stdout)
	}
	call := edge.requestFor(t, "tools/call")
	if call.Tool != "exec_job_attach" {
		t.Fatalf("the environment was asked for %q, want exec_job_attach", call.Tool)
	}
	if call.IdleProbe {
		t.Errorf("exec_job_attach must not carry the idle-probe header: it takes a lease on the environment")
	}
	assertToolArgumentsNameTarget(t, call, "team", "dev")
}

func testJobOffEnvironmentAttachAMalformedRequestFailsBeforeReachingTheEdge(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"job", "attach", "--tenant", "team", "--environment", "dev", "--pid", "4242"},
		erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for a nameless attach, got 0:\n%s", result.Combined)
	}
}

func testJobOffEnvironmentCancelSignalsTheEnvironmentsJob(t *testing.T) {
	skipIfPortsBusy(t, jobEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", jobEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{Results: map[string]string{"tools/call": `{"content":[{"type":"text","text":"cancelled"}],` +
		`"structuredContent":{"tenant":"team","environment":"dev",` +
		`"cancel":{"job":{"id":"suite","name":"suite","state":"running"},"signalled":true,"signal":"KILL","targetPid":4242}}}`}}
	edge.start(t, jobEdgeLocalPort)

	result := erun.Run(t, []string{"job", "cancel", "--tenant", "team", "--environment", "dev", "--id", "suite", "--signal", "KILL"},
		erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Stdout, "job cancelled: suite, SIGKILL to process group 4242") {
		t.Errorf("expected the environment's cancel outcome to be reported, got:\n%s", result.Stdout)
	}
	call := edge.requestFor(t, "tools/call")
	if call.Tool != "exec_job_cancel" {
		t.Fatalf("the environment was asked for %q, want exec_job_cancel", call.Tool)
	}
	if call.IdleProbe {
		t.Errorf("exec_job_cancel must not carry the idle-probe header: it signals a process in the environment")
	}
	assertToolArgumentsNameTarget(t, call, "team", "dev")
}

func testJobOffEnvironmentStartAgentRunsTheAgentInTheEnvironment(t *testing.T) {
	skipIfPortsBusy(t, jobEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", jobEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{Results: map[string]string{"tools/call": `{"content":[{"type":"text","text":"started"}],` +
		`"structuredContent":{"tenant":"team","environment":"dev",` +
		`"job":{"id":"sweep","name":"sweep","state":"running","childPid":4242,"leaseId":"job-sweep","outputBytes":0},"executed":true}}`}}
	edge.start(t, jobEdgeLocalPort)

	result := erun.Run(t, []string{
		"job", "start", "--tenant", "team", "--environment", "dev", "--name", "sweep", "--agent", "claude",
		"--", "fix the failing tests",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	call := edge.requestFor(t, "tools/call")
	if call.Tool != "exec_agent" {
		t.Fatalf("the environment was asked for %q, want exec_agent", call.Tool)
	}
	if call.IdleProbe {
		t.Errorf("exec_agent must not carry the idle-probe header: it starts real work in the environment")
	}
	if got := call.Arguments["prompt"]; got != "fix the failing tests" {
		t.Fatalf("expected exec_agent to carry the joined prompt, got %v (arguments: %+v)", got, call.Arguments)
	}
	assertToolArgumentsNameTarget(t, call, "team", "dev")
}

func testJobOffEnvironmentStatusListsEveryJobWhenNoIdIsGiven(t *testing.T) {
	// The list reply carries no "job" field.
	skipIfPortsBusy(t, jobEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", jobEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{Results: map[string]string{"tools/call": `{"content":[{"type":"text","text":"status"}],` +
		`"structuredContent":{"tenant":"team","environment":"dev"}}`}}
	edge.start(t, jobEdgeLocalPort)

	result := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev"},
		erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Stdout, "no jobs") {
		t.Errorf("expected an empty environment job list to say so, got:\n%s", result.Stdout)
	}
	call := edge.requestFor(t, "tools/call")
	if call.Tool != "exec_job_status" {
		t.Fatalf("the environment was asked for %q, want exec_job_status", call.Tool)
	}
	if !call.IdleProbe {
		t.Errorf("expected job_status to carry the idle-probe header so polling it does not reset the environment's idle timer")
	}
}

func testJobOffEnvironmentStatusReportsTheToolAndTheRemedyWhenTheTargetIsUnresolved(t *testing.T) {
	skipIfPortsBusy(t, jobEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", jobEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{ToolResults: map[string]string{
		"exec_job_status": `{"content":[{"type":"text","text":"tenant/environment not resolved: this MCP server was not started bound to a tenant/environment, and the call did not supply tenant/environment either -- pass tenant and environment explicitly in the call, or run this edge for an environment that has it configured"}],"isError":true}`,
	}}
	edge.start(t, jobEdgeLocalPort)

	result := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "suite"},
		erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for an unresolved target, got 0:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "exec_job_status") {
		t.Errorf("error must name the refusing tool (exec_job_status), got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "pass tenant and environment explicitly") {
		t.Errorf("error must name the remedy, got:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "tenant and environment are required") {
		t.Errorf("error regressed to the bare required-input dead end, got:\n%s", result.Combined)
	}
}

func TestActivityLeaseOffEnvironment(t *testing.T) {
	t.Run("take_holds_the_lease_in_the_environment", testActivityLeaseOffEnvironmentTakeHoldsTheLeaseInTheEnvironment)
	t.Run("take_exclusive_forwards_the_exclusive_claim_to_the_environment", testActivityLeaseOffEnvironmentTakeExclusiveForwardsTheExclusiveClaimToTheEnvironment)
	t.Run("list_reads_the_environments_held_leases", testActivityLeaseOffEnvironmentListReadsTheEnvironmentsHeldLeases)
}

func testActivityLeaseOffEnvironmentTakeHoldsTheLeaseInTheEnvironment(t *testing.T) {
	skipIfPortsBusy(t, leaseEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", leaseEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{Results: map[string]string{"tools/call": `{"content":[{"type":"text","text":"held"}],` +
		`"structuredContent":{"tenant":"team","environment":"dev",` +
		`"lease":{"id":"gradle-build","name":"gradle-build","expiresAt":"2099-01-01T00:00:00Z"},` +
		`"held":[{"id":"gradle-build","name":"gradle-build","expiresAt":"2099-01-01T00:00:00Z"}]}}`}}
	edge.start(t, leaseEdgeLocalPort)

	result := erun.Run(t, []string{"activity", "lease", "take", "--tenant", "team", "--environment", "dev", "--name", "gradle-build"},
		erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Stdout, "lease held: gradle-build") {
		t.Errorf("expected the environment's lease to be reported, got:\n%s", result.Stdout)
	}
	call := edge.requestFor(t, "tools/call")
	if call.Tool != "activity_lease_take" {
		t.Fatalf("the environment was asked for %q, want activity_lease_take", call.Tool)
	}
	if call.IdleProbe {
		t.Errorf("activity_lease_take must not carry the idle-probe header")
	}
}

func testActivityLeaseOffEnvironmentTakeExclusiveForwardsTheExclusiveClaimToTheEnvironment(t *testing.T) {
	skipIfPortsBusy(t, leaseEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", leaseEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{Results: map[string]string{"tools/call": `{"content":[{"type":"text","text":"held"}],` +
		`"structuredContent":{"tenant":"team","environment":"dev",` +
		`"lease":{"id":"job-fix-1245","name":"job-fix-1245","exclusive":true,"scope":"worktree","expiresAt":"2099-01-01T00:00:00Z"},` +
		`"held":[{"id":"job-fix-1245","name":"job-fix-1245","exclusive":true,"scope":"worktree","expiresAt":"2099-01-01T00:00:00Z"}]}}`}}
	edge.start(t, leaseEdgeLocalPort)

	result := erun.Run(t, []string{
		"activity", "lease", "take", "--tenant", "team", "--environment", "dev",
		"--name", "job-fix-1245", "--exclusive", "--orchestrator", "petios",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	call := edge.requestFor(t, "tools/call")
	if call.Tool != "activity_lease_take" {
		t.Fatalf("the environment was asked for %q, want activity_lease_take", call.Tool)
	}
	if call.IdleProbe {
		t.Errorf("an exclusive activity_lease_take must not carry the idle-probe header")
	}
}

func testActivityLeaseOffEnvironmentListReadsTheEnvironmentsHeldLeases(t *testing.T) {
	skipIfPortsBusy(t, leaseEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", leaseEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{Results: map[string]string{"tools/call": `{"content":[{"type":"text","text":"held"}],` +
		`"structuredContent":{"tenant":"team","environment":"dev",` +
		`"held":[{"id":"suite","name":"suite","pid":4242,"expiresAt":"2099-01-01T00:00:00Z"}]}}`}}
	edge.start(t, leaseEdgeLocalPort)

	result := erun.Run(t, []string{"activity", "lease", "list", "--tenant", "team", "--environment", "dev"},
		erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	for _, want := range []string{"suite", "pid 4242"} {
		if !strings.Contains(result.Stdout, want) {
			t.Errorf("expected the environment's leases to contain %q, got:\n%s", want, result.Stdout)
		}
	}
	call := edge.requestFor(t, "tools/call")
	if call.Tool != "activity_lease_list" {
		t.Fatalf("the environment was asked for %q, want activity_lease_list", call.Tool)
	}
	if !call.IdleProbe {
		t.Errorf("expected activity_lease_list to carry the idle-probe header")
	}
}
