package eruncommon

import (
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// #1374: an agent job that backgrounds its gate and exits reports nothing --
// the supervisor waits on its immediate child, the immediate child exits 0
// having left a process it spawned still running, and the record reads as a
// clean success. This is the reproduction: a command that redirects a
// long-lived child's output to a file and backgrounds it before exiting, the
// same shape as an agent job that redirects a gate to a background shell and
// returns. (Backgrounding a child that inherits the supervisor's own stdout
// pipe blocks cmd.Wait() until that child exits too, which would mask the
// bug; redirecting its output away, the way a real background gate does, is
// what reproduces the silent-success shape.)

func TestEnvironmentJobThatBackgroundsWorkAndExitsIsNotReportedAsSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group survivor detection is POSIX-only")
	}
	isolateActivityCache(t)

	const tenant = "abandoned-contract"
	const environment = "bg-test"
	const id = "job"
	backgroundLog := filepath.Join(t.TempDir(), "background.log")

	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Name:        id,
		Command:     []string{"sh", "-c", fmt.Sprintf("sleep 5 </dev/null >%s 2>&1 & exit 0", backgroundLog)},
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}

	job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	t.Cleanup(func() {
		if job.ChildPID > 0 {
			_ = signalEnvironmentJobProcessGroup(job.ChildPID, "KILL")
		}
	})

	if job.Succeeded() {
		t.Fatalf("job reported success (state=%q, exitCode=%v) even though it left a background process running: %+v", job.State, job.ExitCode, job)
	}
	if job.State != EnvironmentJobStateAbandoned {
		t.Fatalf("State = %q, want %q", job.State, EnvironmentJobStateAbandoned)
	}
}

func TestEnvironmentJobThatExitsCleanlyStillSucceeds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group survivor detection is POSIX-only")
	}
	isolateActivityCache(t)

	const tenant = "abandoned-contract"
	const environment = "clean-test"
	const id = "job"

	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Name:        id,
		Command:     []string{"sh", "-c", "exit 0"},
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}

	job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if !job.Succeeded() {
		t.Fatalf("job with no abandoned work did not report success: %+v", job)
	}
	if job.State != EnvironmentJobStateExited {
		t.Fatalf("State = %q, want %q", job.State, EnvironmentJobStateExited)
	}
}
