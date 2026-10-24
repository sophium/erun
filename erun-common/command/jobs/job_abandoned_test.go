package jobs

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/primitive/shared"
)

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
	if !job.Succeeded {
		t.Fatalf("job with no abandoned work did not report success: %+v", job)
	}
	if job.State != shared.EnvironmentJobStateExited {
		t.Fatalf("State = %q, want %q", job.State, shared.EnvironmentJobStateExited)
	}
}

// shrinkGateIncompleteWait shrinks the gate wait cap and poll for one test.
func shrinkGateIncompleteWait(t *testing.T, waitCap, poll time.Duration) {
	t.Helper()
	originalCap, originalPoll := environmentJobGateIncompleteWaitCap, environmentJobGateIncompletePoll
	environmentJobGateIncompleteWaitCap, environmentJobGateIncompletePoll = waitCap, poll
	t.Cleanup(func() {
		environmentJobGateIncompleteWaitCap, environmentJobGateIncompletePoll = originalCap, originalPoll
	})
}

// A started gate job runs in its own session, outside the parent's process
// group, so only the StartedByJobID record makes it visible to the parent's finish check.
func TestEnvironmentJobThatEndsWhileAJobItStartedIsStillRunningIsNotReportedAsSuccess(t *testing.T) {
	isolateActivityCache(t)
	shrinkGateIncompleteWait(t, 50*time.Millisecond, 5*time.Millisecond)

	const tenant = "gate-incomplete-contract"
	const environment = "seeded-child-test"
	const parentID = "outer"
	const childID = "gate"

	dir, err := shared.EnvironmentJobDir(tenant, environment)
	if err != nil {
		t.Fatalf("environmentJobDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	// This test's own pid stays alive, keeping the seeded child "running".
	child := shared.EnvironmentJob{
		ID:             childID,
		Name:           childID,
		State:          EnvironmentJobStateRunning,
		PID:            os.Getpid(),
		StartedAt:      time.Now(),
		StartedByJobID: parentID,
		LeaseID:        environmentJobLeaseID(childID),
	}
	if err := writeEnvironmentJob(dir, child); err != nil {
		t.Fatalf("seed child job record: %v", err)
	}

	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          parentID,
		Name:        parentID,
		Command:     []string{"sh", "-c", "exit 0"},
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}

	parent, err := LoadEnvironmentJob(tenant, environment, parentID, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if parent.Succeeded {
		t.Fatalf("job reported success (state=%q, exitCode=%v) even though a job it started was still running: %+v", parent.State, parent.ExitCode, parent)
	}
	if parent.State != shared.EnvironmentJobStateGateIncomplete {
		t.Fatalf("State = %q, want %q", parent.State, shared.EnvironmentJobStateGateIncomplete)
	}
	if !strings.Contains(parent.Reason, childID) {
		t.Fatalf("Reason %q does not name the still-running job %q", parent.Reason, childID)
	}
}

// A handed-off supervisor is reparented onto its parent once its starter exits,
// so the descendant scan needs the exclusion set to skip it.
func TestAHandedOffJobIsExcludedFromItsParentsLeftoverScan(t *testing.T) {
	isolateActivityCache(t)

	const tenant = "handoff-leftover-contract"
	const environment = "handoff-test"
	const parent = "parent-job"
	dir, err := shared.EnvironmentJobDir(tenant, environment)
	if err != nil {
		t.Fatalf("environmentJobDir: %v", err)
	}
	seeded := []shared.EnvironmentJob{
		{ID: "held", Name: "held", State: EnvironmentJobStateRunning, PID: 4242, StartedByJobID: parent},
		{ID: "released", Name: "released", State: EnvironmentJobStateRunning, PID: 4243, StartedByJobID: parent, Handoff: true},
		{ID: "elsewhere", Name: "elsewhere", State: EnvironmentJobStateRunning, PID: 4244, StartedByJobID: "someone-else", Handoff: true},
	}
	for _, job := range seeded {
		if err := writeEnvironmentJob(dir, job); err != nil {
			t.Fatalf("seed job %s: %v", job.ID, err)
		}
	}

	excluded := environmentJobLeftoverExclusions(dir, parent, nil)
	if _, ok := excluded[4243]; !ok {
		t.Fatalf("the handed-off job's supervisor must be excluded from its parent's leftover scan, got %v", excluded)
	}
	if _, ok := excluded[4242]; ok {
		t.Fatalf("a job this parent did not hand off must stay reportable, got %v", excluded)
	}
	if _, ok := excluded[4244]; ok {
		t.Fatalf("a handoff job this parent did not start is not its to exclude, got %v", excluded)
	}
}
