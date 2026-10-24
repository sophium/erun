package core

import (
	"errors"
	"os/exec"
	"strings"
)

// StderrFromExitError returns the trimmed stderr of an *exec.ExitError, or "".
func StderrFromExitError(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return strings.TrimSpace(string(exitErr.Stderr))
	}
	return ""
}
