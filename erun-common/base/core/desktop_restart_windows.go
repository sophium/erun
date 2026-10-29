//go:build windows

package core

import (
	"os"
)

// DesktopProcessAlive probes pid by opening it; any error reads as gone, since
// restarting an unverifiable pid is unsafe.
func DesktopProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	return err == nil && process != nil
}
