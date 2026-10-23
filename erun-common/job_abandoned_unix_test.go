//go:build !windows

package eruncommon

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
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
	// A leftover that left both the job's process group and its session is
	// found only by the descendant scan, which needs Linux's child subreaper
	// (job_process_descendant_other.go documents the gap elsewhere).
	if runtime.GOOS != "linux" {
		t.Skip("own-session survivor detection needs the Linux child subreaper")
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
	deadline := time.Now().Add(core.EnvironmentJobProcessGroupSurvivorSettleWindow * 10)
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
		time.Sleep(core.EnvironmentJobProcessGroupSurvivorSettlePoll)
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
