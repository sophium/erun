//go:build linux

package podruntime

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/sophium/erun/erun-common/base/core"
)

var runtimeInitSignals = []os.Signal{syscall.SIGCHLD, syscall.SIGUSR1, syscall.SIGUSR2}

func isRuntimeReapSignal(sig os.Signal) bool {
	return sig == syscall.SIGCHLD
}

func enableRuntimeSubreaper() {
	if os.Getpid() != 1 {
		core.EnableEnvironmentJobSubreaper()
	}
}

// reapOrphans holds the supervisor's lock so a starting process is either owned or not
// yet started.
func (s *runtimeSupervisor) reapOrphans() {
	s.mu.Lock()
	defer s.mu.Unlock()
	procs, ok := core.EnvironmentJobSessionProcessesFunc()
	if !ok {
		return
	}
	self := os.Getpid()
	for _, proc := range procs {
		if _, owned := s.owned[proc.Pid]; owned || proc.Pid <= 0 || !proc.Zombie || proc.Parent != self {
			continue
		}
		core.ReapEnvironmentJobChild(proc.Pid)
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

func syncRuntimeFilesystems() {
	syscall.Sync()
}
