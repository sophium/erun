package provision

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

type blockedDeleteCall struct {
	environmentID string
	reason        string
}

type stubRowDeleter struct {
	deleted []string
	blocked []blockedDeleteCall
	err     error
}

func (s *stubRowDeleter) Delete(_ context.Context, _ string, environmentID string) error {
	s.deleted = append(s.deleted, environmentID)
	return s.err
}

func (s *stubRowDeleter) MarkDeleteBlocked(_ context.Context, _ string, environmentID, reason string) error {
	s.blocked = append(s.blocked, blockedDeleteCall{environmentID: environmentID, reason: reason})
	return nil
}

func testLifecycleConfig() EnvDeployConfig {
	return EnvDeployConfig{Registry: "ghcr.io/sophium", PlatformNamespace: "acme-platform", DeployerServiceAccount: "acme-api-deployer"}
}

func TestEnvLifecycleStopRunsJobWithRunningVersion(t *testing.T) {
	runner := &stubLifecycleRunner{stopResult: deployexec.Result{Outcome: deployexec.OutcomeSucceeded}}
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig(), nil, nil, nil, nil)

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
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig(), nil, nil, nil, nil)

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
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig(), nil, nil, nil, nil)

	err := lifecycle.Stop(context.Background(), EnvLifecycleInput{Tenant: "acme", Environment: "prod", EnvironmentID: "env-1", RunningVersion: "1.2.3"})
	if err == nil {
		t.Fatal("expected an error when the stop job fails")
	}
}

// A bootstrapped environment's stop/delete Jobs must run the canonical image.
func TestEnvLifecycleStopFallsBackToCanonicalImage(t *testing.T) {
	runner := &stubLifecycleRunner{stopResult: deployexec.Result{Outcome: deployexec.OutcomeSucceeded}}
	checker := &stubImageChecker{missing: true}
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig(), nil, checker, nil, nil)

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
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig(), nil, checker, nil, nil)

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
	lifecycle := NewEnvLifecycle(runner, rows, testLifecycleConfig(), nil, nil, nil, nil)

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

type stubMachineIdentityRevoker struct {
	revoked []string
	err     error
}

func (s *stubMachineIdentityRevoker) Revoke(_ context.Context, environmentID string) error {
	s.revoked = append(s.revoked, environmentID)
	return s.err
}

// Revocation happens after namespace teardown and before row removal.
func TestEnvLifecycleDeleteRevokesTheEnvironmentPlatformIdentity(t *testing.T) {
	runner := &stubLifecycleRunner{deleteResult: deployexec.Result{Outcome: deployexec.OutcomeSucceeded}}
	rows := &stubRowDeleter{}
	identities := &stubMachineIdentityRevoker{}
	lifecycle := NewEnvLifecycle(runner, rows, testLifecycleConfig(), nil, nil, nil, identities)

	err := lifecycle.Delete(context.Background(), EnvLifecycleInput{
		Tenant: "acme", Environment: "prod", EnvironmentID: "env-1", RunningVersion: "1.2.3",
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(identities.revoked) != 1 || identities.revoked[0] != "env-1" {
		t.Fatalf("revocations = %v, want [env-1]", identities.revoked)
	}
	if len(rows.deleted) != 1 {
		t.Fatalf("row deletions = %v, want the row removed after the revocation", rows.deleted)
	}
}

func TestEnvLifecycleDeleteBlocksWhenRevokingThePlatformIdentityFails(t *testing.T) {
	runner := &stubLifecycleRunner{deleteResult: deployexec.Result{Outcome: deployexec.OutcomeSucceeded}}
	rows := &stubRowDeleter{}
	identities := &stubMachineIdentityRevoker{err: errors.New("identity provider unreachable")}
	lifecycle := NewEnvLifecycle(runner, rows, testLifecycleConfig(), nil, nil, nil, identities)

	err := lifecycle.Delete(context.Background(), EnvLifecycleInput{
		Tenant: "acme", Environment: "prod", EnvironmentID: "env-1", RunningVersion: "1.2.3",
	})
	if err == nil {
		t.Fatal("expected an error when the identity could not be revoked")
	}
	if len(rows.deleted) != 0 {
		t.Fatal("an environment whose identity was not revoked must not have its row removed")
	}
	if len(rows.blocked) != 1 || rows.blocked[0].environmentID != "env-1" {
		t.Fatalf("blocked deletions = %+v, want one naming env-1", rows.blocked)
	}
	if reason := rows.blocked[0].reason; !strings.Contains(reason, "identity") {
		t.Fatalf("deletion-blocked reason = %q, want it to name the identity revocation", reason)
	}
}

func TestEnvLifecycleDeleteSkipsJobWhenNeverDeployed(t *testing.T) {
	runner := &stubLifecycleRunner{}
	rows := &stubRowDeleter{}
	lifecycle := NewEnvLifecycle(runner, rows, testLifecycleConfig(), nil, nil, nil, nil)

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

func TestEnvLifecycleDeleteMarksBlockedOnJobFailure(t *testing.T) {
	runner := &stubLifecycleRunner{deleteResult: deployexec.Result{Outcome: deployexec.OutcomeFailed, Failure: "namespace stuck terminating"}}
	rows := &stubRowDeleter{}
	lifecycle := NewEnvLifecycle(runner, rows, testLifecycleConfig(), nil, nil, nil, nil)

	err := lifecycle.Delete(context.Background(), EnvLifecycleInput{Tenant: "acme", Environment: "prod", EnvironmentID: "env-1", RunningVersion: "1.2.3"})
	if err == nil {
		t.Fatal("expected an error when the delete job fails")
	}
	if len(rows.deleted) != 0 {
		t.Fatal("the row must survive a failed teardown so the environment is not silently forgotten")
	}
	if len(rows.blocked) != 1 || rows.blocked[0].environmentID != "env-1" || rows.blocked[0].reason == "" {
		t.Fatalf("blocked calls = %+v, want one naming env-1 with a non-empty reason", rows.blocked)
	}
}

// `erun delete` exits 0 even when namespace teardown failed.
func TestEnvLifecycleDeleteMarksBlockedOnNamespaceStuckDespiteJobSuccess(t *testing.T) {
	blocker := `namespace "acme-prod" did not finish terminating within 20m0s:` + "\n" +
		"NamespaceContentRemaining=True     challenges.acme.cert-manager.io has 1 resource instances"
	output := `{"tenant":"acme","environment":"prod","namespaceDeleteError":` + jsonQuote(blocker) + `}`
	runner := &stubLifecycleRunner{deleteResult: deployexec.Result{Outcome: deployexec.OutcomeSucceeded, Output: output}}
	rows := &stubRowDeleter{}
	lifecycle := NewEnvLifecycle(runner, rows, testLifecycleConfig(), nil, nil, nil, nil)

	err := lifecycle.Delete(context.Background(), EnvLifecycleInput{Tenant: "acme", Environment: "prod", EnvironmentID: "env-1", RunningVersion: "1.2.3"})
	if err == nil {
		t.Fatal("expected an error when the namespace is stuck despite the job succeeding")
	}
	if len(rows.deleted) != 0 {
		t.Fatal("a stuck namespace must not hard-delete the row even though the job succeeded")
	}
	if len(rows.blocked) != 1 || rows.blocked[0].environmentID != "env-1" || rows.blocked[0].reason != blocker {
		t.Fatalf("blocked calls = %+v, want one naming env-1 with reason %q", rows.blocked, blocker)
	}
}

func TestEnvLifecycleDeleteMarksBlockedWhenJobCannotBeLaunched(t *testing.T) {
	runner := &stubLifecycleRunner{err: errors.New("create job: connection refused")}
	rows := &stubRowDeleter{}
	lifecycle := NewEnvLifecycle(runner, rows, testLifecycleConfig(), nil, nil, nil, nil)

	err := lifecycle.Delete(context.Background(), EnvLifecycleInput{Tenant: "acme", Environment: "prod", EnvironmentID: "env-1", RunningVersion: "1.2.3"})
	if err == nil {
		t.Fatal("expected an error when the job cannot be launched")
	}
	if len(rows.blocked) != 1 || rows.blocked[0].environmentID != "env-1" {
		t.Fatalf("blocked calls = %+v, want one naming env-1", rows.blocked)
	}
}

func jsonQuote(s string) string {
	encoded, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func TestEnvLifecycleStopRecordsUsageEvent(t *testing.T) {
	runner := &stubLifecycleRunner{stopResult: deployexec.Result{Outcome: deployexec.OutcomeSucceeded}}
	usage := &recordingUsageRecorder{}
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig(), usage, nil, nil, nil)

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
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig(), usage, nil, nil, nil)

	_ = lifecycle.Stop(context.Background(), EnvLifecycleInput{Tenant: "acme", Environment: "prod", EnvironmentID: "env-1", RunningVersion: "1.2.3"})
	if len(usage.events) != 0 {
		t.Fatalf("usage events = %+v, want none on a failed stop", usage.events)
	}
}

func TestEnvLifecycleDeleteRecordsUsageEvent(t *testing.T) {
	runner := &stubLifecycleRunner{deleteResult: deployexec.Result{Outcome: deployexec.OutcomeSucceeded}}
	usage := &recordingUsageRecorder{}
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig(), usage, nil, nil, nil)

	if err := lifecycle.Delete(context.Background(), EnvLifecycleInput{Tenant: "acme", Environment: "prod", EnvironmentID: "env-1", RunningVersion: "1.2.3"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(usage.events) != 1 || usage.events[0].EnvironmentID != "env-1" || usage.events[0].EventType != string(model.UsageEventEnvironmentDeleted) {
		t.Fatalf("usage events = %+v", usage.events)
	}
}

func TestEnvLifecycleStopRefusesWhenPlacementCredentialUnavailable(t *testing.T) {
	runner := &stubLifecycleRunner{stopResult: deployexec.Result{Outcome: deployexec.OutcomeSucceeded}}
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig(), nil, nil, nil, nil)

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

func TestEnvLifecycleStopThreadsThePlacementCredential(t *testing.T) {
	runner := &stubLifecycleRunner{stopResult: deployexec.Result{Outcome: deployexec.OutcomeSucceeded}}
	credentials := stubLifecycleCredentials{token: "live-token"}
	lifecycle := NewEnvLifecycle(runner, &stubRowDeleter{}, testLifecycleConfig(), nil, nil, credentials, nil)

	err := lifecycle.Stop(context.Background(), EnvLifecycleInput{
		TenantID: "tenant-1", Tenant: "acme", Environment: "prod", EnvironmentID: "env-1", RunningVersion: "1.2.3",
		ContextID: "ctx-1", PlacementKubernetesContext: "prod-cluster", PlacementServerURL: "https://203.0.113.10:6443",
	})
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	got := runner.stopCalls[0].Placement
	if got.AdminToken != "live-token" || got.KubernetesContext != "prod-cluster" || got.ServerURL != "https://203.0.113.10:6443" {
		t.Fatalf("stop job placement = %+v", got)
	}
	if got.TenantID != "tenant-1" {
		t.Fatalf("stop job placement TenantID = %q, want tenant-1", got.TenantID)
	}
}

func TestEnvLifecycleDeleteRefusesWhenPlacementCredentialUnavailable(t *testing.T) {
	runner := &stubLifecycleRunner{deleteResult: deployexec.Result{Outcome: deployexec.OutcomeSucceeded}}
	rows := &stubRowDeleter{}
	lifecycle := NewEnvLifecycle(runner, rows, testLifecycleConfig(), nil, nil, nil, nil)

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
	if len(rows.blocked) != 1 || rows.blocked[0].environmentID != "env-1" {
		t.Fatalf("blocked calls = %+v, want one naming env-1", rows.blocked)
	}
}

type stubLifecycleCredentials struct {
	token string
	err   error
}

func (s stubLifecycleCredentials) Get(context.Context, string, string) (string, error) {
	return s.token, s.err
}
