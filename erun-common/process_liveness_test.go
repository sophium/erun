package eruncommon

import (
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// ProcessAlive is what a lease and a job record are reconciled against, so it
// has to answer "is this pid still working", not "does this pid exist". The two
// differ for exactly one state, and it is the state this test constructs: a
// process that has exited and is only waiting for a parent to reap it. Signal 0
// still succeeds for that corpse, which is the whole blindness — an exited
// supervisor, or an exited lease holder, reads as alive for as long as
// something holds it.
//
// The crossing is asserted deliberately: the raw signal probe is checked to
// still succeed, so a future change that made the process go away instead of
// leaving it a zombie could not silently make this test vacuous.
func TestProcessAliveRejectsAnExitedProcessThatWasNeverReaped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no zombie state to distinguish, and no signal probe either")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("sh is not on PATH: %v", err)
	}
	// Started without an os/exec Cmd on purpose, so nothing is waiting on it:
	// this is the child whose exit status nobody collects.
	proc, err := os.StartProcess(shell, []string{"sh", "-c", "exit 0"}, &os.ProcAttr{})
	if err != nil {
		t.Fatalf("start a child to leave unreaped: %v", err)
	}
	t.Cleanup(func() { _, _ = proc.Wait() })

	deadline := time.Now().Add(30 * time.Second)
	for {
		if zombie, ok := platformProcessZombie(proc.Pid); ok && zombie {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("the child (pid %d) never reached the exited-but-unreaped state this test needs", proc.Pid)
		}
		time.Sleep(5 * time.Millisecond)
	}

	if err := proc.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("signal 0 on the unreaped child (pid %d) = %v, want success: without that, this test no longer covers the state that reads as alive", proc.Pid, err)
	}
	if ProcessAlive(proc.Pid) {
		t.Fatalf("ProcessAlive(pid %d) = true for a process that has already exited and is only waiting to be reaped", proc.Pid)
	}
}
