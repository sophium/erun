package jobs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common"
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
	if job.State != eruncommon.EnvironmentJobStateExited {
		t.Fatalf("State = %q, want %q", job.State, eruncommon.EnvironmentJobStateExited)
	}
}

// shrinkGateIncompleteWait overrides the wait cap/poll a job's own finish
// check uses when waiting for a job it started (see
// awaitEnvironmentJobRunningChildren), so a test whose seeded child never
// actually finishes hits the cap in milliseconds rather than genuinely
// waiting out the production value. Restored on cleanup so tests do not leak
// state into each other.
func shrinkGateIncompleteWait(t *testing.T, waitCap, poll time.Duration) {
	t.Helper()
	originalCap, originalPoll := environmentJobGateIncompleteWaitCap, environmentJobGateIncompletePoll
	environmentJobGateIncompleteWaitCap, environmentJobGateIncompletePoll = waitCap, poll
	t.Cleanup(func() {
		environmentJobGateIncompleteWaitCap, environmentJobGateIncompletePoll = originalCap, originalPoll
	})
}

// An agent job that runs a gate through its own `job start` (detach and
// await) and then
// ends -- however that end comes about: the agent backgrounding the run
// itself, exhausting its turns, or an outer `timeout` killing the foreground
// wait -- must not read as a clean success while the gate it started has not
// reached a verdict. That gate runs as its own job, detached into its own
// session on purpose so it survives the caller; it is never a member of the
// caller's process group, so the process-group survivor check above cannot
// see it. The record relationship (StartedByJobID) is what makes it visible
// instead. The gate job is seeded directly rather than started for real,
// because the property under test is what the *parent's* finish check does
// when a sibling record says still running, not how the sibling got there.
//
// The parent's finish check actually waits out the gate up to a cap before
// declaring it incomplete (see resolveEnvironmentJobOutcome); the seeded
// child here never finishes, so the wait is shrunk to milliseconds rather
// than genuinely waiting out the production cap.
func TestEnvironmentJobThatEndsWhileAJobItStartedIsStillRunningIsNotReportedAsSuccess(t *testing.T) {
	isolateActivityCache(t)
	shrinkGateIncompleteWait(t, 50*time.Millisecond, 5*time.Millisecond)

	const tenant = "gate-incomplete-contract"
	const environment = "seeded-child-test"
	const parentID = "outer"
	const childID = "gate"

	dir, err := eruncommon.EnvironmentJobDir(tenant, environment)
	if err != nil {
		t.Fatalf("environmentJobDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	// A pid of this test process itself reads as alive for as long as the test
	// runs, which is what keeps the seeded child's state at "running" through
	// the parent's own finish check below without needing a real process.
	child := eruncommon.EnvironmentJob{
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
	if parent.State != eruncommon.EnvironmentJobStateGateIncomplete {
		t.Fatalf("State = %q, want %q", parent.State, eruncommon.EnvironmentJobStateGateIncomplete)
	}
	if !strings.Contains(parent.Reason, childID) {
		t.Fatalf("Reason %q does not name the still-running job %q", parent.Reason, childID)
	}
}

// A deliberate handoff (`job start --handoff`) is excluded from its parent's
// own finish check entirely -- unlike the gate above, a handed-off job that
// is still running (and never finishes) must not stop its parent from
// reporting its own, real success. This is the "some jobs are meant to
// outlive the run that started them" case: a release or a long render an
// agent kicks off on purpose before ending its own turn.
func TestEnvironmentJobHandoffDoesNotBlockItsParentsSuccess(t *testing.T) {
	isolateActivityCache(t)

	const tenant = "gate-incomplete-contract"
	const environment = "handoff-test"
	const parentID = "outer"
	const childID = "released-work"

	dir, err := eruncommon.EnvironmentJobDir(tenant, environment)
	if err != nil {
		t.Fatalf("environmentJobDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	// Alive for the test's whole life, same as the seeded child above -- the
	// point under test is that Handoff excludes it regardless of how long it
	// runs, not that it happens to finish quickly.
	child := eruncommon.EnvironmentJob{
		ID:             childID,
		Name:           childID,
		State:          EnvironmentJobStateRunning,
		PID:            os.Getpid(),
		StartedAt:      time.Now(),
		StartedByJobID: parentID,
		LeaseID:        environmentJobLeaseID(childID),
		Handoff:        true,
	}
	if err := writeEnvironmentJob(dir, child); err != nil {
		t.Fatalf("seed handed-off child job record: %v", err)
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
	if !parent.Succeeded {
		t.Fatalf("a handed-off job that is still running blocked its parent's success: %+v", parent)
	}
	if parent.State != eruncommon.EnvironmentJobStateExited {
		t.Fatalf("State = %q, want %q", parent.State, eruncommon.EnvironmentJobStateExited)
	}
}

// A job this job started, and waited for (see the gate-incomplete test
// above), that finishes without succeeding must not vanish behind this job's
// own clean exit code once that wait concludes because the started job
// finished (rather than by hitting the wait cap) -- StartedJobFailed is what
// surfaces it without a caller separately chasing down every job this one
// ever started. The child here is seeded already finished and failed, so no
// waiting happens at all.
func TestEnvironmentJobThatStartedAFinishedButFailedJobIsNotReportedAsSuccess(t *testing.T) {
	isolateActivityCache(t)

	const tenant = "gate-incomplete-contract"
	const environment = "failed-child-test"
	const parentID = "outer"
	const childID = "gate"

	dir, err := eruncommon.EnvironmentJobDir(tenant, environment)
	if err != nil {
		t.Fatalf("environmentJobDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	failedCode := 1
	child := eruncommon.EnvironmentJob{
		ID:             childID,
		Name:           childID,
		State:          eruncommon.EnvironmentJobStateExited,
		StartedAt:      time.Now(),
		EndedAt:        time.Now(),
		ExitCode:       &failedCode,
		StartedByJobID: parentID,
		LeaseID:        environmentJobLeaseID(childID),
	}
	if err := writeEnvironmentJob(dir, child); err != nil {
		t.Fatalf("seed failed child job record: %v", err)
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
		t.Fatalf("job reported success even though a job it started (and waited for) failed: %+v", parent)
	}
	if parent.State != eruncommon.EnvironmentJobStateExited {
		t.Fatalf("State = %q, want %q -- this job's own process still exited cleanly, only the started job it waited for failed", parent.State, eruncommon.EnvironmentJobStateExited)
	}
	if !strings.Contains(parent.StartedJobFailed, childID) {
		t.Fatalf("StartedJobFailed %q does not name the failed job %q", parent.StartedJobFailed, childID)
	}
}

// The gate-incomplete detection above depends on a nested `job start` reading
// back the parent job's id from its own environment, the way an
// `erun exec job start` run from inside the agent's own Bash tool reads it
// back. This locks that half of the wiring directly:
// the job's own child process must see ERUN_JOB_ID naming this job.
func TestEnvironmentJobSupervisorPropagatesItsOwnIDToTheWorksProcess(t *testing.T) {
	isolateActivityCache(t)

	const tenant = "gate-incomplete-contract"
	const environment = "env-propagation-test"
	const id = "outer"

	idFile := filepath.Join(t.TempDir(), "observed-job-id.txt")
	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Name:        id,
		Command:     []string{"sh", "-c", fmt.Sprintf("printf '%%s' \"$ERUN_JOB_ID\" > %s", idFile)},
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}

	observed, err := os.ReadFile(idFile)
	if err != nil {
		t.Fatalf("read observed job id: %v", err)
	}
	if string(observed) != id {
		t.Fatalf("the work's process observed ERUN_JOB_ID=%q, want %q", observed, id)
	}
}

// A handed-off job is work its parent deliberately does not wait for, so the
// parent's finish check must not count it however it is found. The record-based
// reads already honor that (environmentJobRunningChildren), but the descendant
// scan reads a process table and cannot: a handed-off job's supervisor detaches
// into its own session, so once the work that started it exits the kernel
// reparents it onto the parent -- exactly the shape the widest scan reports as
// abandoned work. The exclusion set is what carries the handoff relationship
// across to that scan, and this locks its two sides: a child this job handed
// off is excluded, and a job it did not start is not.
func TestAHandedOffJobIsExcludedFromItsParentsLeftoverScan(t *testing.T) {
	isolateActivityCache(t)

	const tenant = "handoff-leftover-contract"
	const environment = "handoff-test"
	const parent = "parent-job"
	dir, err := eruncommon.EnvironmentJobDir(tenant, environment)
	if err != nil {
		t.Fatalf("environmentJobDir: %v", err)
	}
	seeded := []eruncommon.EnvironmentJob{
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
