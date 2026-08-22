package eruncommon

import (
	"testing"
	"time"
)

func TestReconcileEnvironmentJobDistinguishesPodReplacedFromSupervisorGone(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(0, 0)

	t.Run("hostname mismatch is a definite pod replacement", func(t *testing.T) {
		job := EnvironmentJob{PID: 123, State: EnvironmentJobStateRunning, Hostname: "old-pod-abc123"}
		resolved := reconcileEnvironmentJob(dir, job, now, neverAlive, "new-pod-def456")
		if resolved.State != EnvironmentJobStateUnknown {
			t.Fatalf("State = %q, want unknown", resolved.State)
		}
		if resolved.UnknownReasonKind != UnknownReasonPodReplaced {
			t.Fatalf("UnknownReasonKind = %q, want %q", resolved.UnknownReasonKind, UnknownReasonPodReplaced)
		}
		if resolved.Reason == "" {
			t.Error("expected a non-empty Reason")
		}
	})

	t.Run("same hostname, dead process is the supervisor gone case", func(t *testing.T) {
		job := EnvironmentJob{PID: 123, State: EnvironmentJobStateRunning, Hostname: "same-pod-abc123"}
		resolved := reconcileEnvironmentJob(dir, job, now, neverAlive, "same-pod-abc123")
		if resolved.UnknownReasonKind != UnknownReasonSupervisorGone {
			t.Fatalf("UnknownReasonKind = %q, want %q", resolved.UnknownReasonKind, UnknownReasonSupervisorGone)
		}
	})

	t.Run("no recorded hostname (pre-#1051 job) falls back to supervisor-gone, not a guessed replacement", func(t *testing.T) {
		job := EnvironmentJob{PID: 123, State: EnvironmentJobStateRunning}
		resolved := reconcileEnvironmentJob(dir, job, now, neverAlive, "whatever-current-pod")
		if resolved.UnknownReasonKind != UnknownReasonSupervisorGone {
			t.Fatalf("UnknownReasonKind = %q, want %q", resolved.UnknownReasonKind, UnknownReasonSupervisorGone)
		}
	})

	t.Run("attached job reports attached-process-gone regardless of hostname", func(t *testing.T) {
		job := EnvironmentJob{PID: 123, State: EnvironmentJobStateRunning, Attached: true, Hostname: "irrelevant"}
		resolved := reconcileEnvironmentJob(dir, job, now, neverAlive, "different-pod")
		if resolved.UnknownReasonKind != UnknownReasonAttachedProcessGone {
			t.Fatalf("UnknownReasonKind = %q, want %q", resolved.UnknownReasonKind, UnknownReasonAttachedProcessGone)
		}
	})

	t.Run("a live process is not reconciled at all, so UnknownReasonKind stays unset", func(t *testing.T) {
		job := EnvironmentJob{PID: 123, State: EnvironmentJobStateRunning, Hostname: "pod-a"}
		resolved := reconcileEnvironmentJob(dir, job, now, func(int) bool { return true }, "pod-b")
		if resolved.State != EnvironmentJobStateRunning {
			t.Fatalf("State = %q, want running", resolved.State)
		}
		if resolved.UnknownReasonKind != "" {
			t.Errorf("UnknownReasonKind = %q, want empty for a still-running job", resolved.UnknownReasonKind)
		}
	})
}
