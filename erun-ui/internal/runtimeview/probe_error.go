package runtimeview

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// runtimeProbeFailureMessage translates a failed Runtime-tab probe. When the
// probe's own ctx expired it names that timeout rather than os/exec's "signal:
// killed"; another signal kill is reported as external; anything else uses fallback.
func runtimeProbeFailureMessage(ctx context.Context, timeout time.Duration, err error, fallback func(error) string) string {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Sprintf(
			"timed out after %s waiting on the runtime pod. This probe enforces its own bound rather than hanging the tab; if it keeps happening, the environment may be overloaded or wedged -- open a shell to check what it is running, or try Reclaim.",
			timeout)
	case ctx.Err() != nil:
		return fmt.Sprintf("canceled before it finished: %s", ctx.Err())
	case isRuntimeProbeExternalKill(err):
		return "the runtime pod's process was killed for a reason other than this probe's own timeout -- most likely an out-of-memory kill or the pod being evicted. Check the environment's recent events."
	default:
		return fallback(err)
	}
}

// isRuntimeProbeExternalKill uses ProcessState.Exited() rather than matching error text.
func isRuntimeProbeExternalKill(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ProcessState == nil {
		return false
	}
	return !exitErr.Exited()
}
