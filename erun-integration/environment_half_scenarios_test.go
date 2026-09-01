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

// These scenarios cover the off-environment half: run from the operator's
// machine, idle/job/lease must answer about, and act on, the environment rather
// than the machine they were typed on. Each pins its own local port so the fake
// edge never collides with a live erun session or with another scenario's edge.
const (
	idleEdgeLocalPort  = 26200
	jobEdgeLocalPort   = 26300
	leaseEdgeLocalPort = 26400
)

// idleStatusPayload is an environment reporting itself busy: a lease is held, so
// nothing about this status can be produced by reading the caller's own store.
const idleStatusPayload = `{"content":[{"type":"text","text":"idle"}],"structuredContent":{` +
	`"policy":{"timeout":300000000000,"workingHours":"08:00-20:00","idleTrafficBytes":0},` +
	`"managedCloud":true,"stopEligible":false,"stopBlockedReason":"a lease is held",` +
	`"secondsUntilStop":1796,` +
	`"leases":[{"id":"suite","name":"suite","expiresAt":"2099-01-01T00:00:00Z"}],` +
	`"markers":[{"name":"lease","idle":false,"reason":"held by suite","secondsRemaining":1796},` +
	`{"name":"ssh","idle":true,"reason":"last activity exceeded timeout"}]}}`

func TestIdleOffEnvironment(t *testing.T) {
	t.Run("reports_the_environments_own_answer", func(t *testing.T) {
		// The environment says a lease is holding it busy. Read from the caller's
		// own store the same command answers "idle" for every marker, which is the
		// defect: a status that does not depend on the environment is not its status.
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

		// #1227: asking whether the environment is idle must not itself count as
		// activity, or polling this command holds the environment awake forever.
		call := edge.requestFor(t, "tools/call")
		if !call.IdleProbe {
			t.Errorf("expected the idle call to carry the idle-probe header so it does not reset the environment's idle timer")
		}
	})

	t.Run("unreachable_edge_is_not_reported_as_idle", func(t *testing.T) {
		// The whole point: a check that cannot see the environment must not answer
		// for it. Nothing is listening on the env's port, which is the same thing an
		// operator sees before `erun open`.
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
	t.Run("start_runs_the_work_in_the_environment", func(t *testing.T) {
		// The handle comes back naming the environment's own log path and the pid of
		// a process there. Started on the caller's machine instead, the job would
		// report a local path and a local pid, and the environment would never see
		// the lease that keeps idle-stop off the work.
		skipIfPortsBusy(t, jobEdgeLocalPort)
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", jobEdgeLocalPort)
		fixture.SeedDesktopIdentity(t, setup)
		// job_start is gone (#1246): starting a plain command remotely now goes
		// through exec_raw's wait:false mode, whose own response only confirms
		// the background start, so the CLI follows up with one exec_job_status
		// call to learn the full record -- both need their own canned answer.
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
		// exec_raw does real work in the environment, so it must count as
		// activity like any other action — never carry the idle-probe header.
		if calls[0].IdleProbe {
			t.Errorf("exec_raw must not carry the idle-probe header: it starts real work in the environment")
		}
		if calls[1].Tool != "exec_job_status" {
			t.Fatalf("the environment was asked for %q second, want exec_job_status", calls[1].Tool)
		}
		// erun#1709: this host already resolved team/dev to reach this edge, and
		// must restate it in the call itself rather than leaving the tool to
		// infer it from the server's own bound context -- the plumbing gap that
		// let a bare "tenant and environment are required" reach an operator
		// even though the flags were parsed correctly.
		for _, call := range calls {
			assertToolArgumentsNameTarget(t, call, "team", "dev")
		}
	})

	t.Run("start_forwards_this_processs_own_job_id_as_startedByJobId", func(t *testing.T) {
		// The MCP edge's own server process has no ERUN_JOB_ID of its own to
		// inherit -- it was never itself started as anyone's job -- so a start
		// forwarded off-environment would otherwise never record its real
		// parent no matter how deep the logical nesting is on the calling
		// side. This is what an off-environment start (this job's own worker
		// running elsewhere, calling out to start work here) threads
		// explicitly instead of relying on that inheritance.
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
	})

	t.Run("start_dry_run_traces_the_environment_call", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "suite", "--dry-run", "--", "work", "--all"},
			erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "job/off_environment_start_dry_run_traces_the_environment_call", normalize.Apply(result.Combined))
	})

	t.Run("a_malformed_request_fails_before_reaching_the_edge", func(t *testing.T) {
		// A request that is wrong on its face is refused for that, not for whatever
		// the edge happened to answer — so the error does not change with connectivity.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--", "work"},
			erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for a nameless job, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "job/off_environment_a_malformed_request_fails_before_reaching_the_edge", normalize.Apply(result.Combined))
	})

	t.Run("await_maps_the_environments_outcome_onto_the_exit_code", func(t *testing.T) {
		// The exit-code contract is what a shell caller branches on, so it has to
		// survive the hop: a still-running job is 124 whether the wait happened here
		// or in the environment.
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

		// #1227: waiting on a job is a read of its state, not new activity in the
		// environment.
		call := edge.requestFor(t, "tools/call")
		if !call.IdleProbe {
			t.Errorf("expected job_await to carry the idle-probe header")
		}
	})

	t.Run("status_reads_the_environments_job_state", func(t *testing.T) {
		// job_status is a pure read, same reasoning as idle (#1227): polling an
		// environment's job status must not itself keep it awake.
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
	})

	t.Run("output_reads_the_environments_captured_output", func(t *testing.T) {
		// job_output is a pure read too: it serves a page of already-captured
		// output, so reading it must not reset the environment's idle timer.
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
	})

	// A genuinely unresolved target (the edge's own runtime image predates the
	// tenant/environment plumbing fix, or the pod was started with no bound
	// context of its own) must still error -- and the message an operator
	// actually sees has to name which tool refused and what would satisfy it,
	// not the bare "tenant and environment are required" dead end erun#1709
	// reported (that phrasing named no tool, no caller, and no recovery).
	t.Run("status_reports_the_tool_and_the_remedy_when_the_target_is_unresolved", func(t *testing.T) {
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
	})
}

func TestActivityLeaseOffEnvironment(t *testing.T) {
	t.Run("take_holds_the_lease_in_the_environment", func(t *testing.T) {
		// A lease taken on the caller's machine defers nothing: it is the
		// environment's idle-stop the lease exists to hold off.
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
		// Taking a lease is a deliberate claim on the environment's busy state, so
		// it must count as activity, not be exempted via the idle-probe header.
		if call.IdleProbe {
			t.Errorf("activity_lease_take must not carry the idle-probe header")
		}
	})

	t.Run("take_exclusive_forwards_the_exclusive_claim_to_the_environment", func(t *testing.T) {
		// erun#1245's exclusive mode has to reach the environment the same way
		// plain presence already does: through activity_lease_take, counting
		// as activity, never as an idle probe.
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
	})

	t.Run("list_reads_the_environments_held_leases", func(t *testing.T) {
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
		// #1227: listing leases only observes them, so it must not itself count as
		// activity.
		if !call.IdleProbe {
			t.Errorf("expected activity_lease_list to carry the idle-probe header")
		}
	})
}
