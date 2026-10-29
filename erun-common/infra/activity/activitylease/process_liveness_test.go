package activitylease

import (
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

// Signal 0 succeeds for an unreaped zombie, which must not read as alive. The raw probe is
// asserted too so the test cannot become vacuous.
func TestProcessAliveRejectsAnExitedProcessThatWasNeverReaped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no zombie state to distinguish, and no signal probe either")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("sh is not on PATH: %v", err)
	}
	// No os/exec Cmd, so nothing reaps it.
	proc, err := os.StartProcess(shell, []string{"sh", "-c", "exit 0"}, &os.ProcAttr{})
	if err != nil {
		t.Fatalf("start a child to leave unreaped: %v", err)
	}
	t.Cleanup(func() { _, _ = proc.Wait() })

	deadline := time.Now().Add(30 * time.Second)
	for {
		if zombie, ok := core.PlatformProcessZombie(proc.Pid); ok && zombie {
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
