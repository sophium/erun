//go:build !windows

package eruncommon

import "os/exec"

// HideConsoleWindow is a no-op off Windows, where there is no stray console
// window to suppress. See exec_windows.go for the real implementation.
func HideConsoleWindow(cmd *exec.Cmd) {}

// terminateProcessTree force-kills cmd's process. Off Windows a direct kill is
// what erun's abort paths have always used; the orphaned-grandchild problem that
// forces a tree walk is Windows-specific (see exec_windows.go).
func terminateProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
