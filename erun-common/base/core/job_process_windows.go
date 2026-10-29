//go:build windows

package core

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// createNewProcessGroup detaches the child from the console's control events,
// so closing that console does not kill the job.
const createNewProcessGroup = 0x00000200

func DetachEnvironmentJobSupervisor(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup
}

func DetachEnvironmentJobChild(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup
}

// SignalEnvironmentJobProcessGroup terminates pid; Windows has no signals, so
// every signal name terminates. Self-signalling is still refused.
func SignalEnvironmentJobProcessGroup(pid int, signal string) error {
	if pid <= 0 {
		return fmt.Errorf("job process id must be positive")
	}
	if pid == os.Getpid() {
		return fmt.Errorf("refusing to signal this process (%d) as a job", pid)
	}
	if _, err := NormalizeEnvironmentJobSignal(signal); err != nil {
		return err
	}
	kill := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", pid))
	HideConsoleWindow(kill)
	// An already-exited process makes taskkill exit non-zero harmlessly.
	_ = kill.Run()
	return nil
}

// EnvironmentJobExitSignal is always empty on Windows.
func EnvironmentJobExitSignal(state *os.ProcessState) string { return "" }

// EnvironmentJobProcessGroupSurvivors always reports false: Windows has no
// signal-0 probe for a process group.
func EnvironmentJobProcessGroupSurvivors(pgid int) bool { return false }

// EnvironmentJobSessionSurvivors always reports false on Windows.
func EnvironmentJobSessionSurvivors(sid int) bool { return false }
