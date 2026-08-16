package service

import (
	"context"
	"errors"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/deployexec"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

type recordingStatusWriter struct {
	transitions []string // "status:error" per call
	updates     []repository.EnvironmentStatusUpdate
	err         error
	// failFirst makes the first n calls fail, exercising the bounded retry.
	failFirst int
	calls     int
}

func (w *recordingStatusWriter) UpdateProvisioningStatus(_ context.Context, _ string, update repository.EnvironmentStatusUpdate) error {
	w.calls++
	if w.calls <= w.failFirst {
		return errors.New("database unavailable")
	}
	w.transitions = append(w.transitions, update.Status+":"+update.ProvisionError)
	w.updates = append(w.updates, update)
	return w.err
}

type fakeRunner struct {
	outcome deployexec.Outcome
	err     error
}

func (r fakeRunner) Run(context.Context, deployexec.DeployJobParams) (deployexec.Outcome, error) {
	return r.outcome, r.err
}

func provision(t *testing.T, runner DeployRunner, status *recordingStatusWriter) error {
	t.Helper()
	return provisionVersion(t, runner, status, "")
}

// provisionVersion drives one deploy with the retry backoff removed, so the
// bounded-retry path costs no wall-clock in tests.
func provisionVersion(t *testing.T, runner DeployRunner, status *recordingStatusWriter, version string) error {
	t.Helper()
	provisioner := NewEnvironmentProvisioner(runner, status)
	provisioner.backoff = 0
	return provisioner.Provision(context.Background(), "env-1", deployexec.DeployJobParams{Version: version})
}

func TestProvisionMarksRunningOnSuccess(t *testing.T) {
	status := &recordingStatusWriter{}
	if err := provision(t, fakeRunner{outcome: deployexec.OutcomeSucceeded}, status); err != nil {
		t.Fatalf("provision: %v", err)
	}
	want := []string{"provisioning:", "running:"}
	assertTransitions(t, status.transitions, want)
}

func TestProvisionMarksFailedOnJobFailure(t *testing.T) {
	status := &recordingStatusWriter{}
	err := provision(t, fakeRunner{outcome: deployexec.OutcomeFailed}, status)
	if err == nil {
		t.Fatal("expected an error when the deploy job fails")
	}
	if len(status.transitions) != 2 || status.transitions[0] != "provisioning:" || status.transitions[1] == "running:" {
		t.Fatalf("transitions = %v, want provisioning then a failed state", status.transitions)
	}
	if status.transitions[1][:7] != "failed:" {
		t.Fatalf("second transition = %q, want failed:*", status.transitions[1])
	}
}

func TestProvisionMarksFailedOnRunError(t *testing.T) {
	status := &recordingStatusWriter{}
	err := provision(t, fakeRunner{err: errors.New("cluster unreachable")}, status)
	if err == nil {
		t.Fatal("expected the run error to propagate")
	}
	if status.transitions[len(status.transitions)-1] != "failed:cluster unreachable" {
		t.Fatalf("last transition = %q, want failed:cluster unreachable", status.transitions[len(status.transitions)-1])
	}
}

// TestProvisionRecordsDeployedVersionOnSuccess: reaching running is what makes
// the version live, so that is where the env records what it is running.
func TestProvisionRecordsDeployedVersionOnSuccess(t *testing.T) {
	status := &recordingStatusWriter{}
	if err := provisionVersion(t, fakeRunner{outcome: deployexec.OutcomeSucceeded}, status, "1.2.3"); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if got := status.updates[0].DeployedVersion; got != "" {
		t.Fatalf("deployed version recorded while still provisioning: %q", got)
	}
	if got := status.updates[1].DeployedVersion; got != "1.2.3" {
		t.Fatalf("running update deployedVersion = %q, want 1.2.3", got)
	}
}

// TestProvisionLeavesDeployedVersionOnFailure: a failed deploy does not change
// what the cluster is running, so it must not claim a new deployed version.
func TestProvisionLeavesDeployedVersionOnFailure(t *testing.T) {
	status := &recordingStatusWriter{}
	if err := provisionVersion(t, fakeRunner{outcome: deployexec.OutcomeFailed}, status, "1.2.3"); err == nil {
		t.Fatal("expected an error when the deploy job fails")
	}
	for i, update := range status.updates {
		if update.DeployedVersion != "" {
			t.Fatalf("update[%d] recorded deployedVersion %q on a failed deploy", i, update.DeployedVersion)
		}
	}
}

// TestProvisionRetriesTransientStatusWrite: a lost lifecycle write strands the
// env in provisioning under an already-terminal workflow, so the write retries.
func TestProvisionRetriesTransientStatusWrite(t *testing.T) {
	status := &recordingStatusWriter{failFirst: 2}
	if err := provisionVersion(t, fakeRunner{outcome: deployexec.OutcomeSucceeded}, status, "1.2.3"); err != nil {
		t.Fatalf("provision should survive a transient status-write failure: %v", err)
	}
	assertTransitions(t, status.transitions, []string{"provisioning:", "running:"})
}

// TestProvisionFailsWhenStatusWriteNeverSucceeds: retries are bounded, so a
// database that stays down surfaces the error rather than looping.
func TestProvisionFailsWhenStatusWriteNeverSucceeds(t *testing.T) {
	status := &recordingStatusWriter{failFirst: statusWriteAttempts}
	if err := provisionVersion(t, fakeRunner{outcome: deployexec.OutcomeSucceeded}, status, "1.2.3"); err == nil {
		t.Fatal("expected the exhausted status write to surface an error")
	}
	if status.calls != statusWriteAttempts {
		t.Fatalf("status write attempted %d times, want %d", status.calls, statusWriteAttempts)
	}
}

func assertTransitions(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("transitions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("transition[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
