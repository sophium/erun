//go:build !windows

package jobs

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
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// `set -m` gives the backgrounded sleep its own pgid, which the process-group
// scan alone cannot see.
func TestEnvironmentJobThatBackgroundsWorkIntoAFreshProcessGroupIsNotReportedAsSuccess(t *testing.T) {
	isolateActivityCache(t)

	const tenant = "abandoned-contract"
	const environment = "bg-fresh-pgid-test"
	const id = "job"
	backgroundLog := filepath.Join(t.TempDir(), "background.log")
	marker := filepath.Join(t.TempDir(), "background-marker")

	if err := RunEnvironmentJobSupervisor(model.EnvironmentJobSupervisorParams{
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
	if job.State != shared.EnvironmentJobStateAbandoned {
		t.Fatalf("State = %q, want %q", job.State, shared.EnvironmentJobStateAbandoned)
	}
}

// setsid leaves both the group and the session; only parentage via the child
// subreaper still finds it.
func TestEnvironmentJobThatBackgroundsWorkIntoItsOwnSessionIsNotReportedAsSuccess(t *testing.T) {
	// Only the descendant scan finds this, and it needs Linux's child subreaper.
	if runtime.GOOS != "linux" {
		t.Skip("own-session survivor detection needs the Linux child subreaper")
	}
	isolateActivityCache(t)

	const tenant = "abandoned-contract"
	const environment = "bg-setsid-test"
	const id = "job"
	backgroundLog := filepath.Join(t.TempDir(), "background.log")
	marker := filepath.Join(t.TempDir(), "background-marker")

	if err := RunEnvironmentJobSupervisor(model.EnvironmentJobSupervisorParams{
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
	if job.State != shared.EnvironmentJobStateAbandoned {
		t.Fatalf("State = %q, want %q", job.State, shared.EnvironmentJobStateAbandoned)
	}
}

// killProcessesMatching kills escaped leftovers by command line and waits for
// them to die, since the subreaper keeps them visible to later tests' scans.
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

// leftoverBackgroundCommand uses `exec -a` so a single process carries the
// marker and killing it orphans no child.
func leftoverBackgroundCommand(marker string, seconds int) string {
	return fmt.Sprintf("bash -c 'exec -a %s sleep %d'", marker, seconds)
}
