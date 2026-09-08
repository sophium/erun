package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// portForwardStartMaxAttempts bounds a retry around the OS-level resource
// allocation a port-forward start needs (opening its log file, forking the
// kubectl child), and portForwardStartRetryBase is its linear backoff step.
// A momentary fd/process-table squeeze on a loaded host -- many concurrent
// erun operations, a busy CI/agent pod -- can fail one of these allocations
// even though the identical call succeeds moments later; a real port-forward
// is worth a few fast retries rather than falling back to a fully degraded
// session over a hiccup that already cleared.
const (
	portForwardStartMaxAttempts = 3
	portForwardStartRetryBase   = 200 * time.Millisecond
)

// retryTransientPortForwardStart retries attempt a bounded number of times
// when it fails with a transient OS resource-allocation error, and returns
// immediately otherwise. attempt must be safe to call again after a prior
// call failed -- each call performs its own log-file open and subprocess
// fork from scratch.
func retryTransientPortForwardStart(attempt func() error) error {
	var lastErr error
	for i := 1; i <= portForwardStartMaxAttempts; i++ {
		err := injectedTransientPortForwardStartFailure(i)
		if err == nil {
			err = attempt()
		}
		if err == nil {
			return nil
		}
		lastErr = err
		if i == portForwardStartMaxAttempts || !isTransientPortForwardStartError(err) {
			break
		}
		time.Sleep(portForwardStartRetryBase * time.Duration(i))
	}
	return lastErr
}

// erunPortForwardForceTransientFailuresEnv is a test-only seam, not a
// production knob: it makes the first N attempts of a port-forward start
// fail with a synthetic transient error, so the integration suite can lock
// the retry behavior above without actually exhausting file descriptors or
// the process table on the host running the test.
const erunPortForwardForceTransientFailuresEnv = "ERUN_PORT_FORWARD_FORCE_TRANSIENT_FAILURES"

func injectedTransientPortForwardStartFailure(attempt int) error {
	raw := strings.TrimSpace(os.Getenv(erunPortForwardForceTransientFailuresEnv))
	if raw == "" {
		return nil
	}
	forceCount, err := strconv.Atoi(raw)
	if err != nil || attempt > forceCount {
		return nil
	}
	return fmt.Errorf("injected for testing (attempt %d of %s): resource temporarily unavailable", attempt, erunPortForwardForceTransientFailuresEnv)
}

// isTransientPortForwardStartError reports whether err is the shape of an OS
// resource-allocation failure (fd exhaustion, process-table exhaustion, or
// memory exhaustion) rather than a genuine, persistent failure to launch the
// port-forward -- e.g. the "kubectl" binary itself missing, which retrying
// can never fix.
func isTransientPortForwardStartError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"too many open files",
		"resource temporarily unavailable",
		"cannot allocate memory",
		"out of memory",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
