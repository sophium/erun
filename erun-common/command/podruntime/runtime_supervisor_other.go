//go:build !linux

package podruntime

import (
	"os"
	"os/exec"
)

var runtimeInitSignals []os.Signal

func isRuntimeReapSignal(os.Signal) bool { return false }

func enableRuntimeSubreaper() {}

func (s *runtimeSupervisor) reapOrphans() {}

func runtimeExitCode(cmd *exec.Cmd, err error) int {
	if cmd.ProcessState == nil {
		if err != nil {
			return 1
		}
		return 0
	}
	return cmd.ProcessState.ExitCode()
}

func syncRuntimeFilesystems() {}
