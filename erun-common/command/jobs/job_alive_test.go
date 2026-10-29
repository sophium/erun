package jobs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared/jobexclusive"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
)

// The supervisor runs in a real re-entered process and is killed for real,
// since an in-process simulation would only prove a stopped ticker is noticed.

const (
	jobAliveSupervisorHelperEnv        = "ERUN_COMMON_TEST_JOB_ALIVE_SUPERVISOR_HELPER"
	jobAliveSupervisorHelperTenantEnv  = "ERUN_COMMON_TEST_JOB_ALIVE_TENANT"
	jobAliveSupervisorHelperEnvNameEnv = "ERUN_COMMON_TEST_JOB_ALIVE_ENVIRONMENT"
	jobAliveSupervisorHelperIDEnv      = "ERUN_COMMON_TEST_JOB_ALIVE_ID"
)

func init() {
	reentryHelpers = append(reentryHelpers, func() (int, bool) {
		if os.Getenv(jobAliveSupervisorHelperEnv) == "" {
			return 0, false
		}
		return runJobAliveSupervisorHelper(), true
	})
}

func runJobAliveSupervisorHelper() int {
	id := os.Getenv(jobAliveSupervisorHelperIDEnv)
	err := RunEnvironmentJobSupervisor(model.EnvironmentJobSupervisorParams{
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

// The stub kubectl stalls just past the 6s window so a slow reconcile read
// reproduces on a quiet machine.
func TestEnvironmentJobAliveAgeSurvivesAReconcileReadSlowerThanThePollBudget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGKILL semantics are POSIX-only")
	}
	isolateActivityCache(t)

	// The stub writes its marker beside itself, so the script needs no interpolated path.
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

	// Without this, the stub no longer being consulted would leave the test vacuous.
	if _, err := os.Stat(filepath.Join(stubDir, "called")); err != nil {
		t.Errorf("the slow kubectl stub was never consulted, so this test no longer exercises a slow reconcile read: %v", err)
	}
}

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

func awaitFirstAliveBeat(t *testing.T, tenant, environment, id string) int {
	t.Helper()
	job, err := pollUntilEnvironmentJob(tenant, environment, id, 5*time.Second, func(job jobstate.EnvironmentJob) bool {
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

// killJobAliveSupervisorHelper also reaps the sleep child, which has its own process group.
func killJobAliveSupervisorHelper(t *testing.T, helper *exec.Cmd, childPID int) {
	t.Helper()
	if err := helper.Process.Kill(); err != nil {
		t.Fatalf("kill supervisor helper: %v", err)
	}
	_ = helper.Wait()
	if childPID > 0 {
		_ = core.SignalEnvironmentJobProcessGroup(childPID, "KILL")
	}
}

func awaitAliveAgeExceedsStaleThreshold(t *testing.T, tenant, environment, id string) {
	t.Helper()
	stale, err := pollUntilEnvironmentJob(tenant, environment, id, 6*time.Second, func(job jobstate.EnvironmentJob) bool {
		return job.AliveAgeMs != nil && *job.AliveAgeMs > EnvironmentJobAliveStaleMs
	})
	if err != nil {
		last, _ := LoadEnvironmentJob(tenant, environment, id, time.Now())
		t.Fatalf("aliveAgeMs never exceeded %dms within 6s of SIGKILL: %v (last seen job: %+v)", EnvironmentJobAliveStaleMs, err, last)
	}
	if stale.AliveAgeMs == nil || *stale.AliveAgeMs <= EnvironmentJobAliveStaleMs {
		t.Fatalf("aliveAgeMs = %v, want > %d", stale.AliveAgeMs, EnvironmentJobAliveStaleMs)
	}
	// State must agree with aliveAgeMs, not lag it.
	if stale.State != jobstate.EnvironmentJobStateUnknown {
		t.Fatalf("State = %q, want unknown once the supervisor pid is confirmed gone", stale.State)
	}
}

// pollUntilEnvironmentJob judges the record and the deadline by the same
// instant, since a reconcile read can be slow; it may overrun by one read.
func pollUntilEnvironmentJob(tenant, environment, id string, timeout time.Duration, want func(jobstate.EnvironmentJob) bool) (jobstate.EnvironmentJob, error) {
	deadline := time.Now().Add(timeout)
	var last jobstate.EnvironmentJob
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

// Registering the job proves the supervisor is alive, so the record is
// published with a beat and never reads running without one.
func TestRegisteredEnvironmentJobIsPublishedWithABeat(t *testing.T) {
	isolateActivityCache(t)

	const tenant = "job-beat-contract"
	const environment = "beat-test"
	const id = "gate"

	if _, err := registerEnvironmentJob(model.EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Name:        id,
		Command:     []string{"sleep", "30"},
	}); err != nil {
		t.Fatalf("registerEnvironmentJob: %v", err)
	}

	dir, err := jobexclusive.EnvironmentJobDir(tenant, environment)
	if err != nil {
		t.Fatalf("environmentJobDir: %v", err)
	}
	// Read through the store to see what a reader sees.
	published, err := jobstate.ReadEnvironmentJob(filepath.Join(dir, id+".json"))
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
