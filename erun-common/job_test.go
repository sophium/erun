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

func TestReconcileEnvironmentJobComputesAliveAgeMsOnEveryRead(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 1, 1, 0, 0, 10, 0, time.UTC)

	t.Run("never beaten reports nil, not zero", func(t *testing.T) {
		job := EnvironmentJob{PID: 123, State: EnvironmentJobStateRunning}
		resolved := reconcileEnvironmentJob(dir, job, now, alwaysAlive, "")
		if resolved.AliveAgeMs != nil {
			t.Fatalf("AliveAgeMs = %v, want nil for a job that never beat", resolved.AliveAgeMs)
		}
	})

	t.Run("a healthy running job reports the elapsed time since its last beat", func(t *testing.T) {
		lastBeat := now.Add(-3 * time.Second)
		job := EnvironmentJob{PID: 123, State: EnvironmentJobStateRunning, LastAliveAt: lastBeat}
		resolved := reconcileEnvironmentJob(dir, job, now, alwaysAlive, "")
		if resolved.AliveAgeMs == nil || *resolved.AliveAgeMs != 3000 {
			t.Fatalf("AliveAgeMs = %v, want 3000", resolved.AliveAgeMs)
		}
	})

	t.Run("a finished job still reports its frozen alive age rather than dropping it", func(t *testing.T) {
		lastBeat := now.Add(-1 * time.Second)
		code := 0
		job := EnvironmentJob{PID: 123, State: EnvironmentJobStateExited, LastAliveAt: lastBeat, ExitCode: &code}
		resolved := reconcileEnvironmentJob(dir, job, now, alwaysAlive, "")
		if resolved.AliveAgeMs == nil || *resolved.AliveAgeMs != 1000 {
			t.Fatalf("AliveAgeMs = %v, want 1000", resolved.AliveAgeMs)
		}
	})

	t.Run("a stale beat on an otherwise-alive pid still surfaces as a large age for the caller to act on", func(t *testing.T) {
		lastBeat := now.Add(-30 * time.Second)
		job := EnvironmentJob{PID: 123, State: EnvironmentJobStateRunning, LastAliveAt: lastBeat}
		resolved := reconcileEnvironmentJob(dir, job, now, alwaysAlive, "")
		if resolved.State != EnvironmentJobStateRunning {
			t.Fatalf("State = %q, want running (reconcile does not act on staleness itself)", resolved.State)
		}
		if resolved.AliveAgeMs == nil || *resolved.AliveAgeMs != 30000 {
			t.Fatalf("AliveAgeMs = %v, want 30000", resolved.AliveAgeMs)
		}
		if *resolved.AliveAgeMs <= EnvironmentJobAliveStaleMs {
			t.Fatalf("AliveAgeMs = %d, want > EnvironmentJobAliveStaleMs (%d)", *resolved.AliveAgeMs, EnvironmentJobAliveStaleMs)
		}
	})
}
