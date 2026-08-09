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
		edge := &fakeMCPEdge{Results: map[string]string{"tools/call": `{"content":[{"type":"text","text":"started"}],` +
			`"structuredContent":{"tenant":"team","environment":"dev","executed":true,"job":{` +
			`"id":"suite","name":"suite","state":"running","childPid":4242,` +
			`"logPath":"/home/erun/.cache/erun/activity/team/dev/jobs/suite.log",` +
			`"leaseId":"job-suite","outputBytes":0}}}`}}
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

		call := edge.requestFor(t, "tools/call")
		if call.Tool != "job_start" {
			t.Fatalf("the environment was asked for %q, want job_start", call.Tool)
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
	})
}
