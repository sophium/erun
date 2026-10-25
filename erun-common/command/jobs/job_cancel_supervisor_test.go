package jobs

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/build/jobhost"
	"github.com/sophium/erun/erun-common/primitive/shared/jobexclusive"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
)

// The supervisor publishes its running record before spawning the work, so a
// cancel in that window must refuse rather than fall back to the supervisor's
// pid. The stand-in is detached like the real one, so the own-group guard cannot mask it.
func TestCancelRefusesToSignalTheSupervisorWhenTheWorkIsNotYetNamed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group signalling is POSIX-only")
	}
	isolateActivityCache(t)

	const tenant = "cancel-supervisor-contract"
	const environment = "cancel-test"
	const id = "runaway"

	supervisorCmd := startCancelTestStandInSupervisor(t)
	supervisor := supervisorCmd.Process

	dir, err := jobexclusive.EnvironmentJobDir(tenant, environment)
	if err != nil {
		t.Fatalf("environmentJobDir: %v", err)
	}
	record := jobstate.EnvironmentJob{
		ID:          id,
		Name:        id,
		State:       EnvironmentJobStateRunning,
		Kind:        EnvironmentJobKindCommand,
		PID:         supervisor.Pid,
		StartedAt:   time.Now(),
		LastAliveAt: time.Now(),
		Hostname:    jobhost.CurrentJobHostname(),
		LogPath:     filepath.Join(dir, id+".log"),
	}
	if err := writeEnvironmentJob(dir, record); err != nil {
		t.Fatalf("write job record: %v", err)
	}

	result, err := CancelEnvironmentJob(core.Context{}, model.CancelEnvironmentJobParams{Tenant: tenant, Environment: environment, ID: id})
	if err == nil {
		t.Fatalf("cancel signalled job %q with no recorded work pid and reported success (target %d); the only pid that record holds is the supervisor's, so this is how a cancelled job's outcome gets lost", id, result.TargetPID)
	}
	if !strings.Contains(err.Error(), "work's process") {
		t.Fatalf("cancel error = %q, want one naming the missing work pid so a caller knows to retry", err)
	}
	if !activity.ProcessAlive(supervisor.Pid) {
		t.Fatalf("cancel killed the job's supervisor (pid %d): nothing is left to record the outcome, which is the unknown-outcome failure this guards", supervisor.Pid)
	}
}

func startCancelTestStandInSupervisor(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := core.Command("sleep", "30")
	core.DetachEnvironmentJobSupervisor(cmd)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		t.Fatalf("start stand-in supervisor: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}
