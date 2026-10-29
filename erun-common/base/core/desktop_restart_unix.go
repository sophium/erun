//go:build !windows

package core

import (
	"errors"
	"os"
	"syscall"
)

// DesktopProcessAlive probes pid with signal 0; nil or EPERM means alive.
func DesktopProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil || process == nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
