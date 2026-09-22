package eruncommon

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A job's supervisor is the one process a cancel must never reach: it is what
// has to survive the signal to record what happened to the work. A started
// job's record names that work in ChildPID, but the supervisor publishes its
// running record before it spawns the work, so a reader can observe a running
// job whose ChildPID is not there yet. Cancelling in that window must not fall
// back to the one pid the record does hold, because that pid is the
// supervisor's — and the report this reproduces is exactly that: the
// supervisor gone without recording an exit status, so the cancelled job read
// back as unknown instead of as an exited job carrying the signal.
//
// The stand-in below is detached with the same helper the real supervisor is
// (detachEnvironmentJobSupervisor), so it sits in its own process group the way
// a real one does and the "this is this call's own group" guard in
// signalEnvironmentJobProcessGroup cannot quietly make the test pass.
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

	dir, err := environmentJobDir(tenant, environment)
	if err != nil {
		t.Fatalf("environmentJobDir: %v", err)
	}
	record := EnvironmentJob{
		ID:          id,
		Name:        id,
		State:       EnvironmentJobStateRunning,
		Kind:        EnvironmentJobKindCommand,
		PID:         supervisor.Pid,
		StartedAt:   time.Now(),
		LastAliveAt: time.Now(),
		Hostname:    currentJobHostname(),
		LogPath:     filepath.Join(dir, id+".log"),
	}
	if err := writeEnvironmentJob(dir, record); err != nil {
		t.Fatalf("write job record: %v", err)
	}

	result, err := CancelEnvironmentJob(Context{}, CancelEnvironmentJobParams{Tenant: tenant, Environment: environment, ID: id})
	if err == nil {
		t.Fatalf("cancel signalled job %q with no recorded work pid and reported success (target %d); the only pid that record holds is the supervisor's, so this is how a cancelled job's outcome gets lost", id, result.TargetPID)
	}
	if !strings.Contains(err.Error(), "work's process") {
		t.Fatalf("cancel error = %q, want one naming the missing work pid so a caller knows to retry", err)
	}
	if !processAlive(supervisor.Pid) {
		t.Fatalf("cancel killed the job's supervisor (pid %d): nothing is left to record the outcome, which is the unknown-outcome failure this guards", supervisor.Pid)
	}
}

// startCancelTestStandInSupervisor launches a long-lived process detached
// exactly as a job supervisor is, so it is in its own process group and can be
// observed for having been signalled.
func startCancelTestStandInSupervisor(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := Command("sleep", "30")
	detachEnvironmentJobSupervisor(cmd)
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
