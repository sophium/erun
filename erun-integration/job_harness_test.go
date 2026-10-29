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

// jobStubEnv routes the "work" stub via ERUN_WORK_BIN, keeping PATH scrubbed.
func jobStubEnv(t *testing.T, setup env.Setup, script string) []string {
	t.Helper()
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryWithScript(t, stubs, "work", script)
	return inEnvironment(append(setup.Env(), fixture.StubEnv(stubs, "work")...))
}

// jobStubSignal is what a stub writes to a file to signal a point in its run;
// waitForFile needs it non-empty.
const jobStubSignal = "reached\\n"

func jobRecordPath(setup env.Setup, tenant, environment, id string) string {
	return filepath.Join(setup.CacheHome, "erun", "activity", tenant, environment, "jobs", id+".json")
}

// startJob starts a job and registers an unconditional stop as cleanup, since a
// job outlives its caller.
func startJob(t *testing.T, setup env.Setup, envVars []string, name string, args ...string) erun.Result {
	t.Helper()
	start := append([]string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", name}, args...)
	result := erun.Run(t, start, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	t.Cleanup(func() {
		stopJob(t, setup, envVars, name)
	})
	return result
}

// stopJob kills a job and waits for its supervisor to exit, so it cannot race
// t.TempDir removal.
func stopJob(t *testing.T, setup env.Setup, envVars []string, id string) {
	t.Helper()
	erun.Run(t, []string{"job", "cancel", "--tenant", "team", "--environment", "dev", "--id", id, "--signal", "KILL"},
		erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	awaitJobSupervisorExit(t, setup, envVars, id, 30*time.Second)
}

// stopOffEnvironmentJob is stopJob for a job started via `erun exec`: cancel
// signals only the work, so its supervisor still needs awaiting.
func stopOffEnvironmentJob(t *testing.T, setup env.Setup, envVars []string, id string) {
	t.Helper()
	erun.Run(t, []string{"exec", "job", "cancel", "--tenant", "team", "--environment", "dev", "--id", id, "--signal", "KILL"},
		erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	awaitJobSupervisorExitVia(t, setup, envVars, id, 30*time.Second, offEnvironmentJobStatus)
}

func inEnvironmentJobStatus(id string) []string {
	return []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", id, "--output", "json"}
}

func offEnvironmentJobStatus(id string) []string {
	return []string{"exec", "job", "status", "--tenant", "team", "--environment", "dev", "--id", id, "--output", "json"}
}

// waitForJobActivity waits until the supervisor's poll has folded an activity.
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

// waitForJobToLeaveRunning polls status until it is not running (30s cap).
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

// waitForJobActivityLeaseRelease polls until the job's lease is released. The
// release happens in deferred cleanup after the terminal record, so `job await`
// returning does not order it.
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
