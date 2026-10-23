//go:build linux

package eruncommon

import (
	"os"
	"os/exec"
	"syscall"
)

// runtimeInitSignals are the signals the init handles besides the terminating
// ones: SIGCHLD wakes the reaper, and the user signals are forwarded.
var runtimeInitSignals = []os.Signal{syscall.SIGCHLD, syscall.SIGUSR1, syscall.SIGUSR2}

func isRuntimeReapSignal(sig os.Signal) bool {
	return sig == syscall.SIGCHLD
}

// enableRuntimeSubreaper makes orphans reparent here when this process is not
// already PID 1, so a run outside a container behaves like one inside.
func enableRuntimeSubreaper() {
	if os.Getpid() != 1 {
		enableEnvironmentJobSubreaper()
	}
}

// reapOrphans waits every zombie child this process did not start itself. It
// holds the supervisor's lock for the sweep, so a process being started is
// either recorded as owned before the sweep reads it or not started yet.
func (s *runtimeSupervisor) reapOrphans() {
	s.mu.Lock()
	defer s.mu.Unlock()
	procs, ok := environmentJobSessionProcessesFunc()
	if !ok {
		return
	}
	self := os.Getpid()
	for _, proc := range procs {
		if _, owned := s.owned[proc.pid]; owned || proc.pid <= 0 || !proc.zombie || proc.parent != self {
			continue
		}
		reapEnvironmentJobChild(proc.pid)
	}
}

func runtimeExitCode(cmd *exec.Cmd, err error) int {
	state := cmd.ProcessState
	if state == nil {
		if err != nil {
			return 1
		}
		return 0
	}
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return state.ExitCode()
}

// syncRuntimeFilesystems flushes filesystem buffers before the host powers off.
func syncRuntimeFilesystems() {
	syscall.Sync()
}
