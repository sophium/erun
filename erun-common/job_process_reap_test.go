package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// reapContractCommand returns the command the two tests below run as a job, and
// the path its orphaned descendant writes its own pid to.
//
// The shape is the point: the job's own command runs for seconds *after* the
// process it leaves behind has already exited, so a reap has to happen while
// the supervisor is still waiting on that command, not as a side effect of it
// finishing. The leftover is a grandchild whose own parent exits immediately,
// which is what orphans it onto the supervisor -- the nearest ancestor marked a
// child subreaper -- rather than onto the command that started it.
func reapContractCommand(t *testing.T, exitCode int) ([]string, string) {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "orphan.pid")
	script := fmt.Sprintf(`sh -c "sleep 2 </dev/null >/dev/null 2>&1 & echo \$! >%s"
sleep 4
exit %d
`, pidFile, exitCode)
	return []string{"sh", "-c", script}, pidFile
}

// readOrphanPID reads the pid the orphaned descendant recorded for itself.
func readOrphanPID(t *testing.T, pidFile string) int {
	t.Helper()
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read the orphan's pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("parse the orphan's pid %q: %v", strings.TrimSpace(string(raw)), err)
	}
	return pid
}

// processTableEntry returns the session-table row for pid, if the platform's
// process table still holds one. A reaped process leaves no row at all.
func processTableEntry(t *testing.T, pid int) (sessionProcess, bool) {
	t.Helper()
	procs, ok := environmentJobSessionProcessesFunc()
	if !ok {
		t.Fatalf("this host's process table could not be read, so the reap cannot be observed")
	}
	for _, proc := range procs {
		if proc.pid == pid {
			return proc, true
		}
	}
	return sessionProcess{}, false
}

// A supervisor is a child subreaper, so anything a job's work orphans is
// reparented onto it -- and nothing else will ever wait on it. Without a reaper
// in the supervisor itself, such a process stays a zombie under the
// supervisor's pid for as long as the supervisor lives, and signal 0, the
// "does this pid exist" probe this codebase uses for liveness, answers true for
// a zombie.
//
// That is the reproduction: a process that has genuinely exited reading as
// alive. It is what made an integration suite's 30s supervisor-liveness waits
// expire against supervisors that were already dead, burning the package's
// whole deadline as a timeout that read like a hang in the change under test.
//
// This is the case that fails before the fix for exactly that reason: the
// orphan has exited a full two seconds before the supervisor stops waiting on
// the job's command, so an un-reaped supervisor still holds it as a zombie when
// this reads the table.
func TestEnvironmentJobSupervisorReapsAReparentedDescendantThatExitsWhileTheJobRuns(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only Linux hands an orphaned descendant to a child subreaper")
	}
	isolateActivityCache(t)

	const tenant = "reap-contract"
	const environment = "reap-test"
	const id = "job"
	command, pidFile := reapContractCommand(t, 0)

	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Name:        id,
		Command:     command,
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}

	orphanPID := readOrphanPID(t, pidFile)
	// The orphan reparented onto this process: it is the nearest ancestor
	// marked a child subreaper, which RunEnvironmentJobSupervisor declared
	// before it started the command. Nothing else can have waited on it, so a
	// row that is still here is the supervisor's to reap and it did not.
	if proc, present := processTableEntry(t, orphanPID); present {
		t.Fatalf("the job's orphaned descendant (pid %d) is still in the process table %s after it exited, as a child of %d (zombie=%v): "+
			"the supervisor did not reap it, so signal 0 reads an exited process as alive",
			orphanPID, 4*time.Second, proc.parent, proc.zombie)
	}
}

// Reaping is scoped to the pid of the job's own command, because os/exec's Wait
// is the only thing that can report that child's exit status. This crosses the
// two: a reap happening alongside the job's command must not consume the exit
// status the record is built from.
func TestEnvironmentJobSupervisorKeepsItsOwnChildsExitStatusWhileReaping(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only Linux hands an orphaned descendant to a child subreaper")
	}
	isolateActivityCache(t)

	const tenant = "reap-contract"
	const environment = "reap-exit-code-test"
	const id = "job"
	command, pidFile := reapContractCommand(t, 42)

	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Name:        id,
		Command:     command,
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}

	orphanPID := readOrphanPID(t, pidFile)
	if proc, present := processTableEntry(t, orphanPID); present {
		t.Fatalf("the job's orphaned descendant (pid %d) was not reaped (child of %d, zombie=%v)", orphanPID, proc.parent, proc.zombie)
	}

	job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if job.ExitCode == nil || *job.ExitCode != 42 {
		t.Fatalf("exit code = %v, want 42: reaping a descendant must not consume the exit status of the job's own command (%+v)", job.ExitCode, job)
	}
}
