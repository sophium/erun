package eruncommon

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

// fakeKubectlPodRunner stubs the kubectl call jobSupervisorContainerRestart
// makes, so a test can supply a canned `kubectl get pod -o json` response (or
// a failure) without shelling out to a real kubectl or cluster.
func fakeKubectlPodRunner(t *testing.T, stdout string, err error) openKubectlRunnerFunc {
	t.Helper()
	return func(args []string, out, errOut io.Writer) error {
		if err != nil {
			return err
		}
		_, writeErr := io.Copy(out, bytes.NewBufferString(stdout))
		return writeErr
	}
}

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

// TestReconcileEnvironmentJobChecksContainerRestartOnSamePod covers the case
// TestReconcileEnvironmentJobDistinguishesPodReplacedFromSupervisorGone does
// not: hostname match rules out a pod replacement, and the same-pod branch
// then asks Kubernetes whether the runtime container itself restarted rather
// than guessing.
func TestReconcileEnvironmentJobChecksContainerRestartOnSamePod(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(0, 0)

	t.Run("restart check unavailable is the supervisor gone case, not a guessed replacement", func(t *testing.T) {
		job := EnvironmentJob{PID: 123, State: EnvironmentJobStateRunning, Hostname: "same-pod-abc123"}
		resolved := reconcileEnvironmentJobWithRestartCheck(dir, job, now, neverAlive, "same-pod-abc123", nil)
		if resolved.UnknownReasonKind != UnknownReasonSupervisorGone {
			t.Fatalf("UnknownReasonKind = %q, want %q", resolved.UnknownReasonKind, UnknownReasonSupervisorGone)
		}
		if strings.Contains(resolved.Reason, "most likely replaced") {
			t.Errorf("Reason = %q, must not guess a replacement when the pod is known to be the same one", resolved.Reason)
		}
	})

	t.Run("kubectl finds no restart is also supervisor gone", func(t *testing.T) {
		job := EnvironmentJob{PID: 123, State: EnvironmentJobStateRunning, Hostname: "same-pod-abc123"}
		runner := fakeKubectlPodRunner(t, `{"status":{"containerStatuses":[{"name":"erun-devops","restartCount":0}]}}`, nil)
		resolved := reconcileEnvironmentJobWithRestartCheck(dir, job, now, neverAlive, "same-pod-abc123", runner)
		if resolved.UnknownReasonKind != UnknownReasonSupervisorGone {
			t.Fatalf("UnknownReasonKind = %q, want %q", resolved.UnknownReasonKind, UnknownReasonSupervisorGone)
		}
	})

	t.Run("kubectl shows an attributable OOM kill names it", func(t *testing.T) {
		job := EnvironmentJob{
			PID:         123,
			State:       EnvironmentJobStateRunning,
			Hostname:    "same-pod-abc123",
			LastAliveAt: time.Unix(0, 0),
		}
		runner := fakeKubectlPodRunner(t, `{"status":{"containerStatuses":[{"name":"erun-devops","restartCount":1,"lastState":{"terminated":{"exitCode":137,"reason":"OOMKilled","finishedAt":"1970-01-01T00:00:05Z"}}}]}}`, nil)
		resolved := reconcileEnvironmentJobWithRestartCheck(dir, job, now, neverAlive, "same-pod-abc123", runner)
		if resolved.UnknownReasonKind != UnknownReasonContainerRestarted {
			t.Fatalf("UnknownReasonKind = %q, want %q", resolved.UnknownReasonKind, UnknownReasonContainerRestarted)
		}
		if !strings.Contains(resolved.Reason, "OOMKilled") || !strings.Contains(resolved.Reason, "137") {
			t.Errorf("Reason = %q, want it to name the OOMKilled reason and exit code 137", resolved.Reason)
		}
	})

	t.Run("kubectl shows a restart from before this job started is not attributed to it", func(t *testing.T) {
		job := EnvironmentJob{
			PID:         123,
			State:       EnvironmentJobStateRunning,
			Hostname:    "same-pod-abc123",
			LastAliveAt: time.Unix(100, 0),
		}
		runner := fakeKubectlPodRunner(t, `{"status":{"containerStatuses":[{"name":"erun-devops","restartCount":1,"lastState":{"terminated":{"exitCode":137,"reason":"OOMKilled","finishedAt":"1970-01-01T00:00:05Z"}}}]}}`, nil)
		resolved := reconcileEnvironmentJobWithRestartCheck(dir, job, now, neverAlive, "same-pod-abc123", runner)
		if resolved.UnknownReasonKind != UnknownReasonSupervisorGone {
			t.Fatalf("UnknownReasonKind = %q, want %q (the restart predates this job's own last known alive beat)", resolved.UnknownReasonKind, UnknownReasonSupervisorGone)
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
