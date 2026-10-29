//go:build !windows

package core

import (
	"os/exec"
)

func HideConsoleWindow(cmd *exec.Cmd) {}

// TerminateProcessTree kills cmd's process; orphaned grandchildren are a Windows-only problem.
func TerminateProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
