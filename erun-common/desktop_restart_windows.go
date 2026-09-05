//go:build windows

package eruncommon

import "os"

// DesktopProcessAlive probes pid by opening it. Unlike Unix, os.FindProcess on
// Windows calls OpenProcess itself, so success here already confirms a live
// process at that pid; a not-found or access-denied error reads as gone rather
// than as "alive but unverifiable", since the caller's only use for this is
// deciding whether it is safe to ask that pid's owner to restart.
func DesktopProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	return err == nil && process != nil
}
