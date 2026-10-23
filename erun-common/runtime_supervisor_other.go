//go:build !linux

package eruncommon

import (
	"os"
	"os/exec"
)

// RunRuntimeEntrypoint refuses to run off Linux before it reaches these: only a
// Linux init has orphans to reap and wait statuses to decode.

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
