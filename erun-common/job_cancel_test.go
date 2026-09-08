package eruncommon

import (
	"runtime"
	"testing"
	"time"
)

// The mirror image of a false clean exit: a job whose only started work is one it
// deliberately cancelled must not read as having failed. The inner job runs
// concurrently with the test (a goroutine, not a real supervisor subprocess
// -- this package has no built erun binary to re-exec for a real nested
// `job start`) and records itself as started by the outer job the same way
// a nested `job start` run from inside the outer's own work would (ERUN_JOB_ID
// inheritance), set directly here since a goroutine shares this process's
// env with the test itself.
func TestEnvironmentJobDeliberatelyCancelledByItsParentIsNotReportedAsAFailedStartedJob(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group signalling is POSIX-only")
	}
	isolateActivityCache(t)

	const tenant = "cancel-provenance-contract"
	const environment = "cancel-test"
	const parentID = "outer"
	const innerID = "inner"

	done := startCancelTestInnerJob(t, tenant, environment, parentID, innerID)
	cancelCancelTestInnerJob(t, tenant, environment, innerID, done)

	outer := runCancelTestOuterJob(t, tenant, environment, parentID)
	if outer.StartedJobFailed != "" {
		t.Fatalf("StartedJobFailed = %q, want empty -- the parent cancelled this job itself, which is not a failure of work it waited for", outer.StartedJobFailed)
	}
	if !outer.Succeeded {
		t.Fatalf("job reported failure even though the only job it started was one it deliberately cancelled: %+v", outer)
	}
}

// startCancelTestInnerJob starts the "inner" job in a goroutine (long enough
// to still be alive when the test cancels it) and returns the channel its
// eventual RunEnvironmentJobSupervisor error lands on.
func startCancelTestInnerJob(t *testing.T, tenant, environment, parentID, innerID string) <-chan error {
	t.Helper()
	t.Setenv(environmentJobIDEnvVar, parentID)
	done := make(chan error, 1)
	go func() {
		done <- RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
			Tenant:      tenant,
			Environment: environment,
			ID:          innerID,
			Name:        innerID,
			Command:     []string{"sleep", "30"},
		})
	}()

	inner, err := pollUntilEnvironmentJob(tenant, environment, innerID, 5*time.Second, func(job EnvironmentJob) bool {
		return job.ChildPID > 0
	})
	if err != nil {
		t.Fatalf("inner job never registered: %v", err)
	}
	if inner.StartedByJobID != parentID {
		t.Fatalf("inner job StartedByJobID = %q, want %q", inner.StartedByJobID, parentID)
	}
	return done
}

// cancelCancelTestInnerJob signals the inner job, waits for its supervisor to
// finish, and asserts the finished record carries the cancel's provenance.
func cancelCancelTestInnerJob(t *testing.T, tenant, environment, innerID string, done <-chan error) {
	t.Helper()
	result, err := CancelEnvironmentJob(Context{}, CancelEnvironmentJobParams{Tenant: tenant, Environment: environment, ID: innerID})
	if err != nil {
		t.Fatalf("CancelEnvironmentJob: %v", err)
	}
	if !result.Signalled {
		t.Fatalf("cancel did not signal a live job: %+v", result)
	}
	if err := <-done; err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor (inner): %v", err)
	}

	cancelled, err := LoadEnvironmentJob(tenant, environment, innerID, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob (inner): %v", err)
	}
	if cancelled.Succeeded {
		t.Fatalf("a job killed by a signal reported success: %+v", cancelled)
	}
	if cancelled.CancelledByJobID != result.Job.StartedByJobID {
		t.Fatalf("CancelledByJobID = %q, want %q (the inner job's own recorded parent)", cancelled.CancelledByJobID, result.Job.StartedByJobID)
	}
}

// runCancelTestOuterJob runs the outer job (a plain, already-finished
// command) and returns its own loaded record.
func runCancelTestOuterJob(t *testing.T, tenant, environment, parentID string) EnvironmentJob {
	t.Helper()
	// The outer job itself was never started by anyone -- clear the env
	// before running it so its own record does not claim a parent of its own.
	t.Setenv(environmentJobIDEnvVar, "")
	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          parentID,
		Name:        parentID,
		Command:     []string{"sh", "-c", "exit 0"},
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor (outer): %v", err)
	}
	outer, err := LoadEnvironmentJob(tenant, environment, parentID, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob (outer): %v", err)
	}
	return outer
}
