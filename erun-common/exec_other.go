//go:build !windows

package eruncommon

import "os/exec"

// HideConsoleWindow is a no-op off Windows, where there is no stray console
// window to suppress. See exec_windows.go for the real implementation.
func HideConsoleWindow(cmd *exec.Cmd) {}
