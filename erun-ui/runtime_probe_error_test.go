package main

import (
	"context"
	"errors"
	"os/exec"
	goruntime "runtime"
	"strings"
	"testing"
	"time"
)

// TestRuntimeProbeFailureMessageNamesOwnDeadlineAsTimeout is the reported
// defect itself: when the ctx that bounded the probe is the one that expired,
// the message must name that timeout and its bound rather than repeating
// whatever kill-signal text os/exec used to enforce it.
func TestRuntimeProbeFailureMessageNamesOwnDeadlineAsTimeout(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	defer cancel()

	msg := runtimeProbeFailureMessage(ctx, 10*time.Second, errors.New("signal: killed"), func(e error) string { return e.Error() })

	if !strings.Contains(msg, "timed out") || !strings.Contains(msg, "10s") {
		t.Fatalf("expected the message to name a timeout and its bound, got %q", msg)
	}
	if strings.Contains(msg, "signal:") {
		t.Fatalf("a self-inflicted deadline must never surface the raw kill signal, got %q", msg)
	}
}

// TestRuntimeProbeFailureMessageNamesExternalKillDistinctFromTimeout covers a
// kill this probe did not cause: ctx has not expired, but the process still
// terminated by signal. That is a different situation (an OOM kill, an
// evicted pod) with a different next action, so it must read differently from
// the timeout branch above, and must not name a bare signal either.
func TestRuntimeProbeFailureMessageNamesExternalKillDistinctFromTimeout(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("signal-terminated exec is a POSIX concept")
	}
	cmd := exec.Command("/bin/sh", "-c", "kill -9 $$")
	killErr := cmd.Run()
	if killErr == nil {
		t.Fatalf("expected the self-kill to produce a signal-terminated exec error")
	}

	msg := runtimeProbeFailureMessage(context.Background(), 10*time.Second, killErr, func(e error) string { return e.Error() })

	if strings.Contains(msg, "timed out") {
		t.Fatalf("a kill this probe did not cause must not be read as its own timeout, got %q", msg)
	}
	if strings.Contains(msg, "signal:") {
		t.Fatalf("an external kill must not surface the raw kill signal either, got %q", msg)
	}
	if !strings.Contains(msg, "killed") || !strings.Contains(msg, "other than this probe") {
		t.Fatalf("expected the message to name an external kill distinct from this probe's own timeout, got %q", msg)
	}
}

// TestRuntimeProbeFailureMessageKeepsRawCauseForUnclassifiedFailure covers
// everything that is neither this probe's own deadline nor a signal-terminated
// process: the raw cause is kept (via the caller's own fallback), never dressed
// up as either specific case.
func TestRuntimeProbeFailureMessageKeepsRawCauseForUnclassifiedFailure(t *testing.T) {
	err := errors.New("kubectl: unable to connect to the server")

	msg := runtimeProbeFailureMessage(context.Background(), 10*time.Second, err, func(e error) string {
		return "fallback: " + e.Error()
	})

	if msg != "fallback: kubectl: unable to connect to the server" {
		t.Fatalf("expected the fallback's own text to survive unchanged, got %q", msg)
	}
}
