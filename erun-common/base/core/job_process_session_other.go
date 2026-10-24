//go:build !windows && !linux && !darwin

package core

// platformSessionProcesses has no native source here; the ps fallback is used.
func platformSessionProcesses() ([]SessionProcess, bool) {
	return nil, false
}
