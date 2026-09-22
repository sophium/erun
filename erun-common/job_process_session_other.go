//go:build !windows && !linux && !darwin

package eruncommon

// platformSessionProcesses reports that this platform has no session-capable
// source of its own, leaving the session check on its ps fallback.
func platformSessionProcesses() ([]sessionProcess, bool) {
	return nil, false
}
