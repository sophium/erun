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
		// Can't determine liveness. On Windows os.FindProcess calls OpenProcess,
		// which an endpoint-security agent denies — returning "dead" here would
		// make a LIVE concurrent deploy look finished and silently defeat the
		// single-flight guard, so rapid/duplicate deploys stop being deduped.
		// Assume alive instead; a genuinely-crashed marker is still reclaimed by
		// the max-age fallback in reconcileExistingInflightMarker. (Unix
		// FindProcess never errors, so this path is Windows-only in practice.)
		return true
	}
	signalErr := proc.Signal(syscall.Signal(0))
	if signalErr == nil {
		return true
	}
	// On darwin (Go 1.23+) `(*os.Process).Signal` returns os.ErrProcessDone
	// for a dead PID instead of surfacing the underlying ESRCH, so a check
	// against ESRCH alone leaves the marker stuck until the max-age fallback.
	if errors.Is(signalErr, syscall.ESRCH) || errors.Is(signalErr, os.ErrProcessDone) {
		return false
	}
	// EPERM and other errors imply the process exists but we can't signal it.
	return true
}
