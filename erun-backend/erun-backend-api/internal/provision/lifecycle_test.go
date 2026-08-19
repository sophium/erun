package provision

import (
	"context"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/deployexec"
)

type stubLifecycleRunner struct {
	stopCalls    []deployexec.StopJobParams
	deleteCalls  []deployexec.DeleteJobParams
	stopResult   deployexec.Result
	deleteResult deployexec.Result
	err          error
}

func (s *stubLifecycleRunner) RunStop(_ context.Context, params deployexec.StopJobParams) (deployexec.Result, error) {
	s.stopCalls = append(s.stopCalls, params)
	if s.err != nil {
		return deployexec.Result{}, s.err
	}
	return s.stopResult, nil
}

func (s *stubLifecycleRunner) RunDelete(_ context.Context, params deployexec.DeleteJobParams) (deployexec.Result, error) {
	s.deleteCalls = append(s.deleteCalls, params)
	if s.err != nil {
		return deployexec.Result{}, s.err
	}
	return s.deleteResult, nil
}

type stubRowDeleter struct {
	deleted []string
	err     error
}

func (s *stubRowDeleter) Delete(_ context.Context, environmentID string) error {
	s.deleted = append(s.deleted, environmentID)
	return s.err
}

func testLifecycleConfig() EnvDeployConfig {
	return EnvDeployConfig{Registry: "ghcr.io/sophium", PlatformNamespace: "acme-platform", DeployerServiceAccount: "acme-api-deployer"}
}

func TestEnvLifecycleStopRunsJobWithRunningVersion(t *testing.T) {
	runner := &stubLifecycleRunner{stopResult: deployexec.Result{Outcome: deployexec.OutcomeSucceeded}}
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig())

	err := lifecycle.Stop(context.Background(), EnvLifecycleInput{
		Tenant: "acme", Environment: "prod", EnvironmentID: "env-1", RunningVersion: "1.2.3",
	})
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if len(runner.stopCalls) != 1 {
		t.Fatalf("RunStop called %d times, want 1", len(runner.stopCalls))
	}
	got := runner.stopCalls[0]
	if got.Image != "ghcr.io/sophium/acme-devops:1.2.3" || got.Namespace != "acme-platform" || got.ServiceAccount != "acme-api-deployer" {
		t.Fatalf("stop job params = %+v", got)
	}
}

func TestEnvLifecycleStopRejectsNeverDeployed(t *testing.T) {
	runner := &stubLifecycleRunner{}
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig())

	err := lifecycle.Stop(context.Background(), EnvLifecycleInput{Tenant: "acme", Environment: "prod", EnvironmentID: "env-1"})
	if err == nil {
		t.Fatal("expected an error for an environment with no running version")
	}
	if len(runner.stopCalls) != 0 {
		t.Fatal("a never-deployed environment must not launch a stop job")
	}
}

func TestEnvLifecycleStopSurfacesJobFailure(t *testing.T) {
	runner := &stubLifecycleRunner{stopResult: deployexec.Result{Outcome: deployexec.OutcomeFailed, Failure: "namespace not found"}}
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig())

	err := lifecycle.Stop(context.Background(), EnvLifecycleInput{Tenant: "acme", Environment: "prod", EnvironmentID: "env-1", RunningVersion: "1.2.3"})
	if err == nil {
		t.Fatal("expected an error when the stop job fails")
	}
}

func TestEnvLifecycleDeleteRunsJobThenDeletesRow(t *testing.T) {
	runner := &stubLifecycleRunner{deleteResult: deployexec.Result{Outcome: deployexec.OutcomeSucceeded}}
	rows := &stubRowDeleter{}
	lifecycle := NewEnvLifecycle(runner, rows, testLifecycleConfig())

	err := lifecycle.Delete(context.Background(), EnvLifecycleInput{
		Tenant: "acme", Environment: "prod", EnvironmentID: "env-1", RunningVersion: "1.2.3",
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(runner.deleteCalls) != 1 {
		t.Fatalf("RunDelete called %d times, want 1", len(runner.deleteCalls))
	}
	if len(rows.deleted) != 1 || rows.deleted[0] != "env-1" {
		t.Fatalf("row deletions = %v, want [env-1]", rows.deleted)
	}
}

// TestEnvLifecycleDeleteSkipsJobWhenNeverDeployed: a remote-agent/local-agent
// row (or a runtime row that never successfully deployed) has no namespace to
// tear down, so delete is a plain row removal.
func TestEnvLifecycleDeleteSkipsJobWhenNeverDeployed(t *testing.T) {
	runner := &stubLifecycleRunner{}
	rows := &stubRowDeleter{}
	lifecycle := NewEnvLifecycle(runner, rows, testLifecycleConfig())

	err := lifecycle.Delete(context.Background(), EnvLifecycleInput{Tenant: "acme", Environment: "agents", EnvironmentID: "env-2"})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(runner.deleteCalls) != 0 {
		t.Fatal("a never-deployed environment must not launch a delete job")
	}
	if len(rows.deleted) != 1 || rows.deleted[0] != "env-2" {
		t.Fatalf("row deletions = %v, want [env-2]", rows.deleted)
	}
}

// TestEnvLifecycleDeleteDoesNotDropRowOnJobFailure: a failed teardown must not
// silently forget an environment whose namespace may still exist.
func TestEnvLifecycleDeleteDoesNotDropRowOnJobFailure(t *testing.T) {
	runner := &stubLifecycleRunner{deleteResult: deployexec.Result{Outcome: deployexec.OutcomeFailed, Failure: "namespace stuck terminating"}}
	rows := &stubRowDeleter{}
	lifecycle := NewEnvLifecycle(runner, rows, testLifecycleConfig())

	err := lifecycle.Delete(context.Background(), EnvLifecycleInput{Tenant: "acme", Environment: "prod", EnvironmentID: "env-1", RunningVersion: "1.2.3"})
	if err == nil {
		t.Fatal("expected an error when the delete job fails")
	}
	if len(rows.deleted) != 0 {
		t.Fatal("the row must survive a failed teardown so the environment is not silently forgotten")
	}
}
