//go:build !windows && !linux && !darwin

package core

// platformSessionProcesses reports that this platform has no session-capable
// source of its own, leaving the session check on its ps fallback.
func platformSessionProcesses() ([]SessionProcess, bool) {
	return nil, false
}
