//go:build !windows

package attach

import (
	"os/exec"
	"syscall"
)

// killAttachProcessGroup signals the whole group: the script runs `dtach -A` as
// a child of `sh`, so signalling `sh` alone would orphan it.
func killAttachProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
