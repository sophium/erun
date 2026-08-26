//go:build !windows

package eruncommon

import (
	"errors"
	"os"
	"syscall"
)

// DesktopProcessAlive probes pid with signal 0, the standard POSIX liveness
// check: os.FindProcess never fails on Unix (it does not open anything), so
// the real answer comes from whether the process accepts a signal. A nil
// error or EPERM (it exists, but this caller may not signal it) both mean
// something is still there at that pid; any other error — ESRCH, or Go's own
// "process already finished" once this process has reaped that pid itself —
// means gone.
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
