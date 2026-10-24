package shared

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Bounded linear-backoff retry for transient fd/process-table exhaustion when starting a port-forward.
const (
	portForwardStartMaxAttempts = 3
	portForwardStartRetryBase   = 200 * time.Millisecond
)

// retryTransientPortForwardStart retries attempt on transient OS allocation errors only;
// attempt must be safe to repeat.
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

// Test-only seam: fail the first N start attempts with a synthetic transient error.
const erunPortForwardForceTransientFailuresEnv = "ERUN_PORT_FORWARD_FORCE_TRANSIENT_FAILURES"

func injectedTransientPortForwardStartFailure(attempt int) error {
	forceCount, ok := forcedTransientPortForwardStartFailures()
	if !ok || attempt > forceCount {
		return nil
	}
	return fmt.Errorf("injected for testing (attempt %d of %s): resource temporarily unavailable", attempt, erunPortForwardForceTransientFailuresEnv)
}

func forcedTransientPortForwardStartFailures() (int, bool) {
	raw := strings.TrimSpace(os.Getenv(erunPortForwardForceTransientFailuresEnv))
	if raw == "" {
		return 0, false
	}
	forceCount, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return forceCount, true
}

// isTransientPortForwardStartError reports fd, process-table, or memory exhaustion, which retrying can fix.
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
