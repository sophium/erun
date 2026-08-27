package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// runtimeProbeFailureMessage is the one translator all three Runtime-tab
// probes (usage, activity, sizing) route a failed read through. Each probe
// binds its own kubectl exec to a context it created and cancels on a fixed
// deadline, so when that call fails, ctx.Err() tells the cause with certainty
// -- there is nothing to infer, because erun is the one that set the deadline
// and enforced it. Reporting the "signal: killed" os/exec used to carry out
// that enforcement, instead of the deadline itself, discards a cause erun
// already knows and reads (in a panel about memory) as an OOM kill that never
// happened. A kill this probe did not cause -- the kernel OOM-killing the
// exec'd process, the pod getting evicted -- is a different situation with a
// different next action, so it is named as an external kill rather than
// folded into the timeout message. Anything else falls through to fallback,
// which lets a caller keep its own domain-specific extraction (e.g. the
// resize command's own refusal text) for errors that were never about the
// deadline at all.
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

// isRuntimeProbeExternalKill reports whether err is a process that terminated
// by signal rather than by exiting. It uses os/exec's own distinction
// (ProcessState.Exited()) rather than matching "signal: killed" in the error
// text, so it cannot mistake a real message that happens to contain that
// phrase for a kill, and it never needs to reproduce the phrase itself.
func isRuntimeProbeExternalKill(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ProcessState == nil {
		return false
	}
	return !exitErr.Exited()
}
