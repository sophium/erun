package eruncommon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// An agent job that backgrounds its gate and exits reports nothing -- the
// supervisor waits on its immediate child, the immediate child exits 0
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
	marker := filepath.Join(t.TempDir(), "background-marker")

	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Name:        id,
		Command:     []string{"sh", "-c", fmt.Sprintf("%s </dev/null >%s 2>&1 & exit 0", leftoverBackgroundCommand(marker, 5), backgroundLog)},
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}
	// By the time this runs the job's own child has been reaped, so signalling
	// its process group is not available: Getpgid on a reaped pid fails, and
	// the group's remaining member would outlive the test and be reparented
	// onto the next one. The leftover answers to its own marker instead.
	t.Cleanup(func() { killProcessesMatching(marker) })

	job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}

	if job.Succeeded {
		t.Fatalf("job reported success (state=%q, exitCode=%v) even though it left a background process running: %+v", job.State, job.ExitCode, job)
	}
	if job.State != EnvironmentJobStateAbandoned {
		t.Fatalf("State = %q, want %q", job.State, EnvironmentJobStateAbandoned)
	}
}

// The actual reproduction: a background process that escapes into a
// *fresh* process group of its own -- exactly what an agent tool's Bash tool
// does when it backgrounds a command so it survives the turn that started it
// -- rather than staying in the immediate child's own group the way the test
// above's plain `cmd &` does. `set -m` is what forces bash's job control to
// give the backgrounded sleep its own pgid instead of sharing the script's;
// environmentJobProcessGroupSurvivors alone cannot see it once it lands
// there, which is exactly the false `succeeded: true` this issue reported.
func TestEnvironmentJobThatBackgroundsWorkIntoAFreshProcessGroupIsNotReportedAsSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group/session survivor detection is POSIX-only")
	}
	isolateActivityCache(t)

	const tenant = "abandoned-contract"
	const environment = "bg-fresh-pgid-test"
	const id = "job"
	backgroundLog := filepath.Join(t.TempDir(), "background.log")
	marker := filepath.Join(t.TempDir(), "background-marker")

	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Name:        id,
		Command:     []string{"bash", "-c", fmt.Sprintf("set -m; %s </dev/null >%s 2>&1 & exit 0", leftoverBackgroundCommand(marker, 5), backgroundLog)},
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}
	t.Cleanup(func() { killProcessesMatching(marker) })

	job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}

	if job.Succeeded {
		t.Fatalf("job reported success (state=%q, exitCode=%v) even though it left a background process running outside its own process group: %+v", job.State, job.ExitCode, job)
	}
	if job.State != EnvironmentJobStateAbandoned {
		t.Fatalf("State = %q, want %q", job.State, EnvironmentJobStateAbandoned)
	}
}

// The third escape, and the widest: a background process that calls setsid for
// itself, taking a fresh process group *and* a fresh session. The test above
// escapes only the process group, which the session scan still catches; this
// one leaves the session too, so no scan of the job's own group or session can
// name it at all. What it cannot leave is its parentage -- the kernel hands an
// orphan to the nearest ancestor marked a child subreaper, which the supervisor
// is -- so the record still reads as abandoned rather than as the clean success
// its exit code claims.
//
// This is the state the reported failure was in: the job's own pids were gone,
// the leftover was running under a pgid and sid of its own, and the record said
// `succeeded: true`.
func TestEnvironmentJobThatBackgroundsWorkIntoItsOwnSessionIsNotReportedAsSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group/session survivor detection is POSIX-only")
	}
	isolateActivityCache(t)

	const tenant = "abandoned-contract"
	const environment = "bg-setsid-test"
	const id = "job"
	backgroundLog := filepath.Join(t.TempDir(), "background.log")
	marker := filepath.Join(t.TempDir(), "background-marker")

	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Name:        id,
		Command:     []string{"bash", "-c", fmt.Sprintf("setsid %s </dev/null >%s 2>&1 & exit 0", leftoverBackgroundCommand(marker, 5), backgroundLog)},
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}
	t.Cleanup(func() { killProcessesMatching(marker) })

	job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}

	if job.Succeeded {
		t.Fatalf("job reported success (state=%q, exitCode=%v) even though it left a background process running in a session of its own: %+v", job.State, job.ExitCode, job)
	}
	if job.State != EnvironmentJobStateAbandoned {
		t.Fatalf("State = %q, want %q", job.State, EnvironmentJobStateAbandoned)
	}
}

// killProcessesMatching kills every process whose command line names path,
// for a background process that escaped the job's own process group (so
// signalEnvironmentJobProcessGroup on the job's ChildPID cannot reach it), and
// then waits for them to go.
//
// The wait is what keeps one test's leftover out of the next test's: a
// survivor of a job is deliberately detached, and the supervisor is a child
// subreaper, so an escaped leftover is reparented onto the test process and
// stays visible to every descendant scan the tests below run until it actually
// dies. Signalling it is not the same as it being gone.
//
// A leftover only answers to this if its own command line carries path:
// `sleep 5` redirected to a log names neither, which is why the background
// commands below give their process a marker to be found by.
func killProcessesMatching(path string) {
	deadline := time.Now().Add(environmentJobProcessGroupSurvivorSettleWindow * 10)
	for {
		pids := pidsMatching(path)
		if len(pids) == 0 {
			return
		}
		for _, pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		if !time.Now().Before(deadline) {
			return
		}
		time.Sleep(environmentJobProcessGroupSurvivorSettlePoll)
	}
}

// pidsMatching returns the pids whose command line names path, ignoring the
// ones pgrep cannot report.
func pidsMatching(path string) []int {
	out, err := exec.Command("pgrep", "-f", path).Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, field := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(field); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// leftoverBackgroundCommand returns a command that runs a long-lived process
// under marker as its command line, so killProcessesMatching can find it after
// it has escaped its job. It is deliberately one process carrying the marker:
// `exec -a` replaces the shell with the sleep, so killing it does not orphan a
// child that would then have to be reaped separately.
func leftoverBackgroundCommand(marker string, seconds int) string {
	return fmt.Sprintf("bash -c 'exec -a %s sleep %d'", marker, seconds)
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
	if !job.Succeeded {
		t.Fatalf("job with no abandoned work did not report success: %+v", job)
	}
	if job.State != EnvironmentJobStateExited {
		t.Fatalf("State = %q, want %q", job.State, EnvironmentJobStateExited)
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

// An agent job that runs a gate through its own `job start` (agent-gate.sh's
// detach-and-await, or an agent driving the same primitive directly) and then
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

	dir, err := environmentJobDir(tenant, environment)
	if err != nil {
		t.Fatalf("environmentJobDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	// A pid of this test process itself reads as alive for as long as the test
	// runs, which is what keeps the seeded child's state at "running" through
	// the parent's own finish check below without needing a real process.
	child := EnvironmentJob{
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
	if parent.State != EnvironmentJobStateGateIncomplete {
		t.Fatalf("State = %q, want %q", parent.State, EnvironmentJobStateGateIncomplete)
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

	dir, err := environmentJobDir(tenant, environment)
	if err != nil {
		t.Fatalf("environmentJobDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	// Alive for the test's whole life, same as the seeded child above -- the
	// point under test is that Handoff excludes it regardless of how long it
	// runs, not that it happens to finish quickly.
	child := EnvironmentJob{
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
	if parent.State != EnvironmentJobStateExited {
		t.Fatalf("State = %q, want %q", parent.State, EnvironmentJobStateExited)
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

	dir, err := environmentJobDir(tenant, environment)
	if err != nil {
		t.Fatalf("environmentJobDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	failedCode := 1
	child := EnvironmentJob{
		ID:             childID,
		Name:           childID,
		State:          EnvironmentJobStateExited,
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
	if parent.State != EnvironmentJobStateExited {
		t.Fatalf("State = %q, want %q -- this job's own process still exited cleanly, only the started job it waited for failed", parent.State, EnvironmentJobStateExited)
	}
	if !strings.Contains(parent.StartedJobFailed, childID) {
		t.Fatalf("StartedJobFailed %q does not name the failed job %q", parent.StartedJobFailed, childID)
	}
}

// The gate-incomplete detection above depends on a nested `job start` reading
// back the parent job's id from its own environment, exactly the way
// agent-gate.sh's `erun exec job start` (run from inside the agent's own Bash
// tool) reads it back today. This locks that half of the wiring directly:
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
