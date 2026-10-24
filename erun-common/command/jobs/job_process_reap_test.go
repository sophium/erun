//go:build !windows

package jobs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// reapContractCommand runs a job whose orphaned grandchild exits seconds before
// the job's own command, so the supervisor must reap while still waiting.
func reapContractCommand(t *testing.T, exitCode int) ([]string, string) {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "orphan.pid")
	script := fmt.Sprintf(`sh -c "sleep 2 </dev/null >/dev/null 2>&1 & echo \$! >%s"
sleep 4
exit %d
`, pidFile, exitCode)
	return []string{"sh", "-c", script}, pidFile
}

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

// processTableEntry returns pid's process-table row; a reaped process has none.
func processTableEntry(t *testing.T, pid int) (core.SessionProcess, bool) {
	t.Helper()
	procs, ok := core.EnvironmentJobSessionProcessesFunc()
	if !ok {
		t.Fatalf("this host's process table could not be read, so the reap cannot be observed")
	}
	for _, proc := range procs {
		if proc.Pid == pid {
			return proc, true
		}
	}
	return core.SessionProcess{}, false
}

// A supervisor is a child subreaper; an unreaped orphan stays a zombie, and
// signal 0 reads a zombie as alive.
func TestEnvironmentJobSupervisorReapsAReparentedDescendantThatExitsWhileTheJobRuns(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only Linux hands an orphaned descendant to a child subreaper")
	}
	isolateActivityCache(t)

	const tenant = "reap-contract"
	const environment = "reap-test"
	const id = "job"
	command, pidFile := reapContractCommand(t, 0)

	if err := RunEnvironmentJobSupervisor(model.EnvironmentJobSupervisorParams{
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
		t.Fatalf("the job's orphaned descendant (pid %d) is still in the process table %s after it exited, as a child of %d (zombie=%v): "+
			"the supervisor did not reap it, so signal 0 reads an exited process as alive",
			orphanPID, 4*time.Second, proc.Parent, proc.Zombie)
	}
}

// Reaping must not consume the exit status of the job's own command.
func TestEnvironmentJobSupervisorKeepsItsOwnChildsExitStatusWhileReaping(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only Linux hands an orphaned descendant to a child subreaper")
	}
	isolateActivityCache(t)

	const tenant = "reap-contract"
	const environment = "reap-exit-code-test"
	const id = "job"
	command, pidFile := reapContractCommand(t, 42)

	if err := RunEnvironmentJobSupervisor(model.EnvironmentJobSupervisorParams{
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
		t.Fatalf("the job's orphaned descendant (pid %d) was not reaped (child of %d, zombie=%v)", orphanPID, proc.Parent, proc.Zombie)
	}

	job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if job.ExitCode == nil || *job.ExitCode != 42 {
		t.Fatalf("exit code = %v, want 42: reaping a descendant must not consume the exit status of the job's own command (%+v)", job.ExitCode, job)
	}
}
