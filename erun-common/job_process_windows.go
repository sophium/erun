//go:build windows

package eruncommon

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// createNewProcessGroup detaches a child from the console control events the
// caller's group receives, which is the closest Windows equivalent to a new
// session: closing the console that started a job no longer takes the job with
// it.
const createNewProcessGroup = 0x00000200

func detachEnvironmentJobSupervisor(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup
}

func detachEnvironmentJobChild(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup
}

// signalEnvironmentJobProcessGroup terminates the work by pid. Windows has no
// signals, so every supported signal name lands on the same termination; the
// self-signal guard still applies, because naming this process would take the
// bookkeeping down with the work.
func signalEnvironmentJobProcessGroup(pid int, signal string) error {
	if pid <= 0 {
		return fmt.Errorf("job process id must be positive")
	}
	if pid == os.Getpid() {
		return fmt.Errorf("refusing to signal this process (%d) as a job", pid)
	}
	if _, err := normalizeEnvironmentJobSignal(signal); err != nil {
		return err
	}
	kill := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", pid))
	HideConsoleWindow(kill)
	// A process that already exited yields a harmless non-zero taskkill exit; the
	// record reconciles on the next read either way.
	_ = kill.Run()
	return nil
}

// environmentJobExitSignal is always empty on Windows: a terminated process
// reports an exit code, not a signal.
func environmentJobExitSignal(state *os.ProcessState) string { return "" }
