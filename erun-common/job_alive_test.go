package eruncommon

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// The alive contract exists because a job's supervisor can die outright (a
// SIGTERM'd container, an OOM kill) and leave nothing to say so from the
// inside. Simulating that in-process would only prove the reconcile logic
// reacts correctly to a stopped ticker, not that the whole thing survives a
// real process actually going away — so this spawns a genuine supervisor in
// its own OS process and kills it for real, the same way TestMain already
// re-enters this binary as a stub ssh for the workspace-sync tests.

const (
	jobAliveSupervisorHelperEnv        = "ERUN_COMMON_TEST_JOB_ALIVE_SUPERVISOR_HELPER"
	jobAliveSupervisorHelperTenantEnv  = "ERUN_COMMON_TEST_JOB_ALIVE_TENANT"
	jobAliveSupervisorHelperEnvNameEnv = "ERUN_COMMON_TEST_JOB_ALIVE_ENVIRONMENT"
	jobAliveSupervisorHelperIDEnv      = "ERUN_COMMON_TEST_JOB_ALIVE_ID"
)

// runJobAliveSupervisorHelper is the re-entered process body: it runs the real
// supervisor against a long-lived child, so killing this process is killing
// exactly what a runtime pod's own supervisor process is.
func runJobAliveSupervisorHelper() int {
	id := os.Getenv(jobAliveSupervisorHelperIDEnv)
	err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant:      os.Getenv(jobAliveSupervisorHelperTenantEnv),
		Environment: os.Getenv(jobAliveSupervisorHelperEnvNameEnv),
		ID:          id,
		Name:        id,
		Command:     []string{"sleep", "60"},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func TestEnvironmentJobAliveAgeMsExceedsFiveSecondsWithinSixSecondsOfSupervisorSIGKILL(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGKILL semantics are POSIX-only")
	}
	isolateActivityCache(t)

	const tenant = "alive-contract"
	const environment = "kill-test"
	const id = "beat"

	helper := startJobAliveSupervisorHelper(t, tenant, environment, id)
	childPID := awaitFirstAliveBeat(t, tenant, environment, id)
	killJobAliveSupervisorHelper(t, helper, childPID)
	awaitAliveAgeExceedsStaleThreshold(t, tenant, environment, id)
}

// startJobAliveSupervisorHelper launches the re-entered process and registers
// a cleanup that kills it, so a failing assertion never leaves it running past
// the test.
func startJobAliveSupervisorHelper(t *testing.T, tenant, environment, id string) *exec.Cmd {
	t.Helper()
	helper := exec.Command(os.Args[0])
	helper.Env = append(os.Environ(),
		jobAliveSupervisorHelperEnv+"=1",
		jobAliveSupervisorHelperTenantEnv+"="+tenant,
		jobAliveSupervisorHelperEnvNameEnv+"="+environment,
		jobAliveSupervisorHelperIDEnv+"="+id,
	)
	if err := helper.Start(); err != nil {
		t.Fatalf("start supervisor helper: %v", err)
	}
	t.Cleanup(func() { _ = helper.Process.Kill(); _ = helper.Wait() })
	return helper
}

// awaitFirstAliveBeat waits for the real supervisor to register the job and
// land its first beat, mirroring what a caller starting a job would poll for,
// and returns the child pid the cleanup after the kill needs.
func awaitFirstAliveBeat(t *testing.T, tenant, environment, id string) int {
	t.Helper()
	job, err := pollUntilEnvironmentJob(tenant, environment, id, 5*time.Second, func(job EnvironmentJob) bool {
		return job.AliveSeq > 0 && job.AliveAgeMs != nil
	})
	if err != nil {
		t.Fatalf("job never landed its first alive beat: %v", err)
	}
	if job.State != EnvironmentJobStateRunning {
		t.Fatalf("job state = %q before kill, want running", job.State)
	}
	return job.ChildPID
}

// killJobAliveSupervisorHelper kills the supervisor for real and reaps its
// sleep child, which sat in its own process group precisely so this can reach
// it without touching the (now dead) supervisor.
func killJobAliveSupervisorHelper(t *testing.T, helper *exec.Cmd, childPID int) {
	t.Helper()
	if err := helper.Process.Kill(); err != nil {
		t.Fatalf("kill supervisor helper: %v", err)
	}
	_ = helper.Wait()
	if childPID > 0 {
		_ = signalEnvironmentJobProcessGroup(childPID, "KILL")
	}
}

// awaitAliveAgeExceedsStaleThreshold is the assertion the whole test exists
// for: a dead supervisor's silence must read as stale within the documented
// ~6s bound, not linger as an ambiguous "still running".
func awaitAliveAgeExceedsStaleThreshold(t *testing.T, tenant, environment, id string) {
	t.Helper()
	stale, err := pollUntilEnvironmentJob(tenant, environment, id, 6*time.Second, func(job EnvironmentJob) bool {
		return job.AliveAgeMs != nil && *job.AliveAgeMs > EnvironmentJobAliveStaleMs
	})
	if err != nil {
		last, _ := LoadEnvironmentJob(tenant, environment, id, time.Now())
		t.Fatalf("aliveAgeMs never exceeded %dms within 6s of SIGKILL: %v (last seen job: %+v)", EnvironmentJobAliveStaleMs, err, last)
	}
	if stale.AliveAgeMs == nil || *stale.AliveAgeMs <= EnvironmentJobAliveStaleMs {
		t.Fatalf("aliveAgeMs = %v, want > %d", stale.AliveAgeMs, EnvironmentJobAliveStaleMs)
	}
	// The pid-liveness reconcile already catches up by this point too — the
	// documented caller rule's "unknown, never success, never a tool error"
	// is backed by both signals agreeing, not aliveAgeMs alone.
	if stale.State != EnvironmentJobStateUnknown {
		t.Fatalf("State = %q, want unknown once the supervisor pid is confirmed gone", stale.State)
	}
}

// pollUntilEnvironmentJob re-reads a job until it satisfies want or the
// deadline passes, so the test reacts to the real beat cadence instead of
// sleeping a fixed guess.
func pollUntilEnvironmentJob(tenant, environment, id string, timeout time.Duration, want func(EnvironmentJob) bool) (EnvironmentJob, error) {
	deadline := time.Now().Add(timeout)
	var last EnvironmentJob
	for {
		job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
		if err == nil {
			last = job
			if want(job) {
				return job, nil
			}
		}
		if !time.Now().Before(deadline) {
			return last, fmt.Errorf("condition not met within %s", timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
