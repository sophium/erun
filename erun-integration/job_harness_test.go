package integration

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
)

// The job scenarios drive the real detach-and-observe path rather than a
// dry-run trace: the whole contract is what a supervisor running in the
// environment observes about work it waited on, which no plan can show. The
// work itself is a declared stub binary, so what a job reports is fixed by the
// scenario and not by whatever the host happens to have installed.

// jobStubEnv declares the stub the job scenarios run as their work and returns
// the scenario environment routed at it. The stub is reached through the
// ERUN_<NAME>_BIN seam every erun-spawned binary honors, so the scrubbed PATH
// stays scrubbed.
func jobStubEnv(t *testing.T, setup env.Setup, script string) []string {
	t.Helper()
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryWithScript(t, stubs, "work", script)
	return inEnvironment(append(setup.Env(), fixture.StubEnv(stubs, "work")...))
}

// jobStubSignal is the line a job stub writes to say it reached a point in its
// run, so a scenario synchronizes on an observable condition rather than on a
// wall-clock sleep that a loaded machine would make flaky. It carries content
// because waitForFile waits for a non-empty file.
const jobStubSignal = "reached\\n"

func jobRecordPath(setup env.Setup, tenant, environment, id string) string {
	return filepath.Join(setup.CacheHome, "erun", "activity", tenant, environment, "jobs", id+".json")
}

// startJob starts a job and registers a cleanup that stops it. The whole point
// of a job is that it outlives the call that started it, so a scenario that
// fails before releasing its work would otherwise strand a process past the test
// run; stopping an already-finished job is a successful no-op, so the cleanup
// is unconditional.
func startJob(t *testing.T, setup env.Setup, envVars []string, name string, args ...string) erun.Result {
	t.Helper()
	start := append([]string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", name}, args...)
	result := erun.Run(t, start, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	t.Cleanup(func() {
		stopJob(t, setup, envVars, name)
	})
	return result
}

// stopJob signals a job to stop and then waits for its supervisor process to
// actually exit before returning -- see awaitJobSupervisorExit for why the
// wait matters. Every scenario that starts a real job registers this (rather
// than a bare "job cancel") as its cleanup, so a still-alive supervisor can
// never race t.TempDir()'s own removal, which runs only after every
// registered t.Cleanup callback has returned.
func stopJob(t *testing.T, setup env.Setup, envVars []string, id string) {
	t.Helper()
	erun.Run(t, []string{"job", "cancel", "--tenant", "team", "--environment", "dev", "--id", id, "--signal", "KILL"},
		erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	awaitJobSupervisorExit(t, setup, envVars, id, 30*time.Second)
}

// stopOffEnvironmentJob is stopJob for a job a host caller started through
// erun's exec namespace (job_off_environment_*_test.go). It exists because
// the race stopJob guards against is a property of the job supervisor, not of
// which namespace started it: CancelEnvironmentJob signals only the job's
// work (job_supervisor.go), so an off-environment job's supervisor outlives
// its own cancel exactly as an in-environment one does, keeps renewing its
// activity lease under the scenario's TempDir, and races t.TempDir()'s
// RemoveAll the same way. The two differ only in how the record is read back:
// an off-environment job's record lives on the far side of the MCP edge, so
// its status is read with "exec job status" rather than "job status".
func stopOffEnvironmentJob(t *testing.T, setup env.Setup, envVars []string, id string) {
	t.Helper()
	erun.Run(t, []string{"exec", "job", "cancel", "--tenant", "team", "--environment", "dev", "--id", id, "--signal", "KILL"},
		erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	awaitJobSupervisorExitVia(t, setup, envVars, id, 30*time.Second, offEnvironmentJobStatus)
}

// inEnvironmentJobStatus and offEnvironmentJobStatus are the argv prefixes
// that read a job record back through the namespace it was started in.
func inEnvironmentJobStatus(id string) []string {
	return []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", id, "--output", "json"}
}

func offEnvironmentJobStatus(id string) []string {
	return []string{"exec", "job", "status", "--tenant", "team", "--environment", "dev", "--id", id, "--output", "json"}
}

// waitForJobActivity blocks until the supervisor has folded enough of an agent's
// event stream to name an activity. The fold runs on the supervisor's own poll,
// so a scenario that read straight after the stub flushed would race it; waiting
// on the observable condition is what keeps the read deterministic instead of
// timing-dependent.
func waitForJobActivity(t *testing.T, setup env.Setup, envVars []string, id string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		result := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", id, "--output", "json"},
			erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		var payload struct {
			Progress struct {
				Activity string `json:"activity"`
			} `json:"progress"`
		}
		if json.Unmarshal([]byte(result.Stdout), &payload) == nil && payload.Progress.Activity != "" {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("job %q reported no agent activity within %s:\n%s", id, timeout, result.Combined)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitForJobToLeaveRunning polls the job's status until it reports anything
// other than running, failing after thirty seconds.
func waitForJobToLeaveRunning(t *testing.T, setup env.Setup, envVars []string, id string) erun.Result {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		status := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", id}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if !strings.HasPrefix(status.Combined, "running:") {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("job never left running: %s", status.Combined)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitForJobActivityLeaseRelease polls `activity lease list` until the job's
// claim is gone, and returns that listing.
//
// A job's terminal record and its lease release are not ordered against each
// other: finishEnvironmentJob writes the record, and the lease is dropped by
// the supervisor's own deferred cleanup, which runs after it. `job await`
// returns on the record, so reading the lease straight afterwards races that
// cleanup -- under a loaded full-suite run the listing still showed the claim
// and the scenario failed on a happens-before the production code never
// promised. Waiting for the condition the scenario is actually about (the
// claim being released) is what makes it deterministic; the deadline keeps it
// a real assertion rather than an assumption.
func waitForJobActivityLeaseRelease(t *testing.T, cwd string, envVars []string) erun.Result {
	t.Helper()
	const timeout = 30 * time.Second
	deadline := time.Now().Add(timeout)
	for {
		result := erun.Run(t, []string{"activity", "lease", "list", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: cwd, Env: envVars})
		if result.ExitCode == 0 && strings.Contains(result.Combined, "no leases held") {
			return result
		}
		if time.Now().After(deadline) {
			t.Fatalf("a finished job still held its activity lease %s after it reported a terminal state; finishing work must release the claim:\n%s", timeout, result.Combined)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
