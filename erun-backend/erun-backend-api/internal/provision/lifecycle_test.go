package provision

import (
	"context"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/deployexec"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

type recordingUsageRecorder struct {
	events []model.UsageEvent
}

func (r *recordingUsageRecorder) Record(_ context.Context, event model.UsageEvent) error {
	r.events = append(r.events, event)
	return nil
}

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
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig(), nil, nil, nil)

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
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig(), nil, nil, nil)

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
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig(), nil, nil, nil)

	err := lifecycle.Stop(context.Background(), EnvLifecycleInput{Tenant: "acme", Environment: "prod", EnvironmentID: "env-1", RunningVersion: "1.2.3"})
	if err == nil {
		t.Fatal("expected an error when the stop job fails")
	}
}

// TestEnvLifecycleStopFallsBackToCanonicalImage and
// TestEnvLifecycleDeleteFallsBackToCanonicalImage lock the stop/delete image
// fallback: an environment that was bootstrapped onto the canonical
// erun-devops image at deploy time (because the tenant never published its
// own <tenant>-devops image) is still running that image, so its stop/delete
// Jobs must name it too — not the tenant's own image, which the registry
// already confirmed does not exist and which would only ImagePullBackOff.
func TestEnvLifecycleStopFallsBackToCanonicalImage(t *testing.T) {
	runner := &stubLifecycleRunner{stopResult: deployexec.Result{Outcome: deployexec.OutcomeSucceeded}}
	checker := &stubImageChecker{missing: true}
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig(), nil, checker, nil)

	err := lifecycle.Stop(context.Background(), EnvLifecycleInput{
		Tenant: "operations", Environment: "probe7", EnvironmentID: "env-1", RunningVersion: "1.0.185",
	})
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	got := runner.stopCalls[0]
	if got.Image != "ghcr.io/sophium/erun-devops:1.0.185" {
		t.Fatalf("stop job image = %q, want the canonical ghcr.io/sophium/erun-devops:1.0.185", got.Image)
	}
}

func TestEnvLifecycleDeleteFallsBackToCanonicalImage(t *testing.T) {
	runner := &stubLifecycleRunner{deleteResult: deployexec.Result{Outcome: deployexec.OutcomeSucceeded}}
	checker := &stubImageChecker{missing: true}
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig(), nil, checker, nil)

	err := lifecycle.Delete(context.Background(), EnvLifecycleInput{
		Tenant: "operations", Environment: "probe7", EnvironmentID: "env-1", RunningVersion: "1.0.185",
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	got := runner.deleteCalls[0]
	if got.Image != "ghcr.io/sophium/erun-devops:1.0.185" {
		t.Fatalf("delete job image = %q, want the canonical ghcr.io/sophium/erun-devops:1.0.185", got.Image)
	}
}

func TestEnvLifecycleDeleteRunsJobThenDeletesRow(t *testing.T) {
	runner := &stubLifecycleRunner{deleteResult: deployexec.Result{Outcome: deployexec.OutcomeSucceeded}}
	rows := &stubRowDeleter{}
	lifecycle := NewEnvLifecycle(runner, rows, testLifecycleConfig(), nil, nil, nil)

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
	lifecycle := NewEnvLifecycle(runner, rows, testLifecycleConfig(), nil, nil, nil)

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
	lifecycle := NewEnvLifecycle(runner, rows, testLifecycleConfig(), nil, nil, nil)

	err := lifecycle.Delete(context.Background(), EnvLifecycleInput{Tenant: "acme", Environment: "prod", EnvironmentID: "env-1", RunningVersion: "1.2.3"})
	if err == nil {
		t.Fatal("expected an error when the delete job fails")
	}
	if len(rows.deleted) != 0 {
		t.Fatal("the row must survive a failed teardown so the environment is not silently forgotten")
	}
}

// TestEnvLifecycleStopRecordsUsageEvent and TestEnvLifecycleDeleteRecordsUsageEvent
// lock the metering hook for #605: a successful stop/delete records the event,
// a failed one does not.
func TestEnvLifecycleStopRecordsUsageEvent(t *testing.T) {
	runner := &stubLifecycleRunner{stopResult: deployexec.Result{Outcome: deployexec.OutcomeSucceeded}}
	usage := &recordingUsageRecorder{}
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig(), usage, nil, nil)

	if err := lifecycle.Stop(context.Background(), EnvLifecycleInput{Tenant: "acme", Environment: "prod", EnvironmentID: "env-1", RunningVersion: "1.2.3"}); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if len(usage.events) != 1 || usage.events[0].EnvironmentID != "env-1" || usage.events[0].EventType != string(model.UsageEventEnvironmentStopped) {
		t.Fatalf("usage events = %+v", usage.events)
	}
}

func TestEnvLifecycleStopRecordsNoUsageEventOnFailure(t *testing.T) {
	runner := &stubLifecycleRunner{stopResult: deployexec.Result{Outcome: deployexec.OutcomeFailed}}
	usage := &recordingUsageRecorder{}
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig(), usage, nil, nil)

	_ = lifecycle.Stop(context.Background(), EnvLifecycleInput{Tenant: "acme", Environment: "prod", EnvironmentID: "env-1", RunningVersion: "1.2.3"})
	if len(usage.events) != 0 {
		t.Fatalf("usage events = %+v, want none on a failed stop", usage.events)
	}
}

func TestEnvLifecycleDeleteRecordsUsageEvent(t *testing.T) {
	runner := &stubLifecycleRunner{deleteResult: deployexec.Result{Outcome: deployexec.OutcomeSucceeded}}
	usage := &recordingUsageRecorder{}
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig(), usage, nil, nil)

	if err := lifecycle.Delete(context.Background(), EnvLifecycleInput{Tenant: "acme", Environment: "prod", EnvironmentID: "env-1", RunningVersion: "1.2.3"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(usage.events) != 1 || usage.events[0].EnvironmentID != "env-1" || usage.events[0].EventType != string(model.UsageEventEnvironmentDeleted) {
		t.Fatalf("usage events = %+v", usage.events)
	}
}

// TestEnvLifecycleStopRefusesWhenPlacementCredentialUnavailable: an
// environment placed on a context but no resolver configured must fail
// clearly rather than running the stop Job unauthenticated (#1112).
func TestEnvLifecycleStopRefusesWhenPlacementCredentialUnavailable(t *testing.T) {
	runner := &stubLifecycleRunner{stopResult: deployexec.Result{Outcome: deployexec.OutcomeSucceeded}}
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig(), nil, nil, nil)

	err := lifecycle.Stop(context.Background(), EnvLifecycleInput{
		Tenant: "acme", Environment: "prod", EnvironmentID: "env-1", RunningVersion: "1.2.3", ContextID: "ctx-1",
	})
	if err == nil {
		t.Fatal("expected an error when no placement credential resolver is configured")
	}
	if len(runner.stopCalls) != 0 {
		t.Fatal("a stop with no resolvable credential must not launch a job")
	}
}

// TestEnvLifecycleStopThreadsThePlacementCredential: the stop Job's placement
// carries the live-resolved token, the target cluster's kubernetes context
// name, and its server URL.
func TestEnvLifecycleStopThreadsThePlacementCredential(t *testing.T) {
	runner := &stubLifecycleRunner{stopResult: deployexec.Result{Outcome: deployexec.OutcomeSucceeded}}
	credentials := stubLifecycleCredentials{token: "live-token"}
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig(), nil, nil, credentials)

	err := lifecycle.Stop(context.Background(), EnvLifecycleInput{
		Tenant: "acme", Environment: "prod", EnvironmentID: "env-1", RunningVersion: "1.2.3",
		ContextID: "ctx-1", PlacementKubernetesContext: "prod-cluster", PlacementServerURL: "https://203.0.113.10:6443",
	})
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	got := runner.stopCalls[0].Placement
	if got.AdminToken != "live-token" || got.KubernetesContext != "prod-cluster" || got.ServerURL != "https://203.0.113.10:6443" {
		t.Fatalf("stop job placement = %+v", got)
	}
}

// TestEnvLifecycleDeleteRefusesWhenPlacementCredentialUnavailable mirrors the
// stop case for delete.
func TestEnvLifecycleDeleteRefusesWhenPlacementCredentialUnavailable(t *testing.T) {
	runner := &stubLifecycleRunner{deleteResult: deployexec.Result{Outcome: deployexec.OutcomeSucceeded}}
	rows := &stubRowDeleter{}
	lifecycle := NewEnvLifecycle(runner, rows, testLifecycleConfig(), nil, nil, nil)

	err := lifecycle.Delete(context.Background(), EnvLifecycleInput{
		Tenant: "acme", Environment: "prod", EnvironmentID: "env-1", RunningVersion: "1.2.3", ContextID: "ctx-1",
	})
	if err == nil {
		t.Fatal("expected an error when no placement credential resolver is configured")
	}
	if len(runner.deleteCalls) != 0 {
		t.Fatal("a delete with no resolvable credential must not launch a job")
	}
	if len(rows.deleted) != 0 {
		t.Fatal("a delete that never ran its job must not remove the row")
	}
}

type stubLifecycleCredentials struct {
	token string
	err   error
}

func (s stubLifecycleCredentials) Get(context.Context, string) (string, error) {
	return s.token, s.err
}
