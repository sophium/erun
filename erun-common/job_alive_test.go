package eruncommon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// TestEnvironmentJobAliveAgeSurvivesAReconcileReadSlowerThanThePollBudget
// covers the same contract as the kill test above, under the one condition
// that used to make that test red on a loaded machine while the behaviour it
// asserts was never wrong.
//
// LoadEnvironmentJob reconciles what it reads, and once the supervisor is gone
// that reconcile can shell out to kubectl to ask whether the container
// restarted -- a read that is disk-, process- and load-bound, not fixed-cost.
// The poll helper used to give the job a `now` before that read and then
// decide the deadline from a second, fresh clock reading afterwards, so one
// read that outlasted the remaining budget ended the loop reporting
// "condition not met" about an instant that had already met it. The stub below
// makes that read slow on purpose -- deliberately, and by a margin just past
// the 6s window -- so the failure reproduces on a quiet machine instead of only
// on a busy one.
func TestEnvironmentJobAliveAgeSurvivesAReconcileReadSlowerThanThePollBudget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGKILL semantics are POSIX-only")
	}
	isolateActivityCache(t)

	// A stand-in kubectl that records that it ran and then stalls, so the
	// reconcile read is slow for a known reason rather than because the
	// machine happens to be loaded. It writes its marker beside itself, which
	// keeps the script free of any interpolated path.
	stubDir := t.TempDir()
	stub := filepath.Join(stubDir, "kubectl")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n: > \"$(dirname \"$0\")/called\"\nsleep 7\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	const tenant = "alive-contract-slow-read"
	const environment = "kill-test"
	const id = "beat"

	helper := startJobAliveSupervisorHelper(t, tenant, environment, id)
	childPID := awaitFirstAliveBeat(t, tenant, environment, id)
	killJobAliveSupervisorHelper(t, helper, childPID)
	awaitAliveAgeExceedsStaleThreshold(t, tenant, environment, id)

	// Checked only once the contract held, so an earlier failure reports its
	// own cause. Without this the stub silently stopping being consulted --
	// the reconcile moving to the in-process Kubernetes client, say -- would
	// leave this test green while covering nothing.
	if _, err := os.Stat(filepath.Join(stubDir, "called")); err != nil {
		t.Errorf("the slow kubectl stub was never consulted, so this test no longer exercises a slow reconcile read: %v", err)
	}
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
//
// One instant per attempt judges both: the read is given a now, want() judges
// the record against that same now, and the deadline is compared against it
// too rather than a fresh time.Now() taken once the read has returned. A read
// is not free -- LoadEnvironmentJob reconciles the record, which can shell out
// to kubectl for a same-pod supervisor loss -- so a slow one used to end the
// loop reporting "condition not met" about an instant that had already met it,
// which failed this helper's own tests on a loaded machine even though the
// product behaviour they assert was never wrong. Its bound is now one read
// past the deadline rather than exactly the deadline, which is the price of
// never discarding an observation the condition would have accepted.
func pollUntilEnvironmentJob(tenant, environment, id string, timeout time.Duration, want func(EnvironmentJob) bool) (EnvironmentJob, error) {
	deadline := time.Now().Add(timeout)
	var last EnvironmentJob
	for {
		now := time.Now()
		job, err := LoadEnvironmentJob(tenant, environment, id, now)
		if err == nil {
			last = job
			if want(job) {
				return job, nil
			}
		}
		if !now.Before(deadline) {
			return last, fmt.Errorf("condition not met within %s", timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// A job's own record must never read as running without a heartbeat. The
// supervisor publishes that record before it opens the log, takes the activity
// lease and starts its beat, so a reader landing in that window saw a running
// job with no last-beat field at all -- which is the field every liveness
// decision, and the exclusive-claim status line, read. Registering the job is
// itself proof the supervisor is alive, so it stamps the beat it is already
// evidence of rather than leaving a window a reader has to be lucky about.
func TestRegisteredEnvironmentJobIsPublishedWithABeat(t *testing.T) {
	isolateActivityCache(t)

	const tenant = "job-beat-contract"
	const environment = "beat-test"
	const id = "gate"

	if _, err := registerEnvironmentJob(EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Name:        id,
		Command:     []string{"sleep", "30"},
	}); err != nil {
		t.Fatalf("registerEnvironmentJob: %v", err)
	}

	dir, err := environmentJobDir(tenant, environment)
	if err != nil {
		t.Fatalf("environmentJobDir: %v", err)
	}
	// Read back through the store rather than the recorder, so this asserts
	// what a reader of the record actually sees the moment it appears.
	published, err := readEnvironmentJob(filepath.Join(dir, id+".json"))
	if err != nil {
		t.Fatalf("read published job record: %v", err)
	}
	if published.State != EnvironmentJobStateRunning {
		t.Fatalf("published state = %q, want %q", published.State, EnvironmentJobStateRunning)
	}
	resolved := reconcileEnvironmentJob(dir, published, time.Now(), alwaysAlive, published.Hostname)
	if resolved.AliveAgeMs == nil {
		t.Fatalf("the first observable record of a running job reports no heartbeat at all: %+v", resolved)
	}
}
