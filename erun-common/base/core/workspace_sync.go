package core

import (
	"errors"
	"os/exec"
	"strings"
)

// StderrFromExitError trims the stderr exec.Cmd.Output() captures onto a
// failed *exec.ExitError, so a caller that wrapped only "%w" (which renders
// as the content-free "exit status N") can include the process's own
// diagnostic instead. Returns "" for a non-ExitError (e.g. context
// cancellation) or an ExitError with nothing on stderr.
func StderrFromExitError(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return strings.TrimSpace(string(exitErr.Stderr))
	}
	return ""
}
