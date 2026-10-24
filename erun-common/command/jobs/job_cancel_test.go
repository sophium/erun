package jobs

import (
	"runtime"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// The inner job runs in a goroutine (no erun binary to re-exec) and sets
// ERUN_JOB_ID directly to mark the outer job as its parent.
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

func startCancelTestInnerJob(t *testing.T, tenant, environment, parentID, innerID string) <-chan error {
	t.Helper()
	t.Setenv(shared.EnvironmentJobIDEnvVar, parentID)
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

	inner, err := pollUntilEnvironmentJob(tenant, environment, innerID, 5*time.Second, func(job shared.EnvironmentJob) bool {
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

func cancelCancelTestInnerJob(t *testing.T, tenant, environment, innerID string, done <-chan error) {
	t.Helper()
	result, err := CancelEnvironmentJob(core.Context{}, CancelEnvironmentJobParams{Tenant: tenant, Environment: environment, ID: innerID})
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

func runCancelTestOuterJob(t *testing.T, tenant, environment, parentID string) shared.EnvironmentJob {
	t.Helper()
	// Clear the env so the outer record claims no parent.
	t.Setenv(shared.EnvironmentJobIDEnvVar, "")
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
