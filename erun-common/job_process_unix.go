//go:build !windows

package eruncommon

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// detachEnvironmentJobSupervisor puts the supervisor in its own session, so it
// has no controlling terminal to be hung up with and is not in the caller's
// process group. That is what makes a started job survive the call returning and
// the transport dropping, without the caller wrapping anything in setsid.
func detachEnvironmentJobSupervisor(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}

// detachEnvironmentJobChild gives the work its own process group, so cancelling
// a job reaches everything the work spawned and nothing else — in particular not
// the supervisor, which has to outlive the cancel to record its outcome.
func detachEnvironmentJobChild(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// signalEnvironmentJobProcessGroup signals a recorded pid's whole process group.
// The two guards are the point: a caller can only ever name a pid a job record
// holds, and this refuses to signal the group the caller is itself in — which is
// how a pattern-matched kill once took out the sequence that issued it.
func signalEnvironmentJobProcessGroup(pid int, signal string) error {
	if pid <= 0 {
		return fmt.Errorf("job process id must be positive")
	}
	if pid == os.Getpid() {
		return fmt.Errorf("refusing to signal this process (%d) as a job", pid)
	}
	group, err := syscall.Getpgid(pid)
	if err != nil {
		// The process is already gone; the record reconciles on the next read.
		return nil
	}
	if own, err := syscall.Getpgid(os.Getpid()); err == nil && own == group {
		return fmt.Errorf("refusing to signal process group %d: it is this process's own group", group)
	}
	number, err := environmentJobSignalNumber(signal)
	if err != nil {
		return err
	}
	if err := syscall.Kill(-group, number); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("signal job process group %d: %w", group, err)
	}
	return nil
}

func environmentJobSignalNumber(signal string) (syscall.Signal, error) {
	switch signal {
	case "TERM":
		return syscall.SIGTERM, nil
	case "INT":
		return syscall.SIGINT, nil
	case "HUP":
		return syscall.SIGHUP, nil
	case "KILL":
		return syscall.SIGKILL, nil
	default:
		return 0, fmt.Errorf("unsupported signal %q", signal)
	}
}

// environmentJobExitSignal names the signal that ended the work, so a cancelled
// job reads as cancelled rather than as an exit code nobody chose.
func environmentJobExitSignal(state *os.ProcessState) string {
	if state == nil {
		return ""
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return ""
	}
	return status.Signal().String()
}
