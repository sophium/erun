package core

import (
	"errors"
	"os"
	"strings"
	"syscall"
)

func SanitizeForFilename(s string) string {
	if s == "" {
		return "_"
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		// Unknown liveness: assume alive. Windows endpoint security can deny OpenProcess,
		// and "dead" would defeat single-flight; stale markers age out.
		return true
	}
	signalErr := proc.Signal(syscall.Signal(0))
	if signalErr == nil {
		return true
	}
	// Darwin returns os.ErrProcessDone rather than ESRCH for a dead PID.
	if errors.Is(signalErr, syscall.ESRCH) || errors.Is(signalErr, os.ErrProcessDone) {
		return false
	}
	// EPERM: the process exists but cannot be signalled.
	return true
}
