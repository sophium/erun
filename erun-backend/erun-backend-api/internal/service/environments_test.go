package service

import (
	"context"
	"errors"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/deployexec"
)

type recordingStatusWriter struct {
	transitions []string // "status:error" per call
	err         error
}

func (w *recordingStatusWriter) UpdateProvisioningStatus(_ context.Context, _, status, provisionError string) error {
	w.transitions = append(w.transitions, status+":"+provisionError)
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
	return NewEnvironmentProvisioner(runner, status).Provision(context.Background(), "env-1", deployexec.DeployJobParams{})
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
