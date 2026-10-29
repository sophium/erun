package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/deployexec"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

type recordingUsageRecorder struct {
	events []model.UsageEvent
	err    error
}

func (r *recordingUsageRecorder) Record(_ context.Context, event model.UsageEvent) error {
	r.events = append(r.events, event)
	return r.err
}

type recordingStatusWriter struct {
	transitions []string // "status:error" per call
	updates     []repository.EnvironmentStatusUpdate
	err         error
	failFirst   int
	calls       int
}

func (w *recordingStatusWriter) UpdateProvisioningStatus(_ context.Context, _ string, _ string, update repository.EnvironmentStatusUpdate) error {
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
	failure string
	output  string
	err     error
}

func (r fakeRunner) Run(context.Context, deployexec.DeployJobParams) (deployexec.Result, error) {
	return deployexec.Result{Outcome: r.outcome, Failure: r.failure, Output: r.output}, r.err
}

func provision(t *testing.T, runner DeployRunner, status *recordingStatusWriter) error {
	t.Helper()
	return provisionVersion(t, runner, status, "")
}

func provisionVersion(t *testing.T, runner DeployRunner, status *recordingStatusWriter, version string) error {
	t.Helper()
	provisioner := NewEnvironmentProvisioner(runner, status, nil, nil)
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

func TestProvisionRecordsTheDeploysOwnFailure(t *testing.T) {
	const chartFailure = "runtime chart oci://ghcr.io/acme/charts/erun-devops version 1.2.3 could not be pulled from ghcr.io/acme: " +
		"no chart is published at 1.2.3 at any coordinate the deploy probed — ghcr.io/acme/charts/acme-devops (the tenant's own umbrella), ghcr.io/acme/charts/erun-devops (the shared platform chart)"
	status := &recordingStatusWriter{}
	runner := fakeRunner{outcome: deployexec.OutcomeFailed, failure: chartFailure}
	if err := provisionVersion(t, runner, status, "1.2.3"); err == nil {
		t.Fatal("expected an error when the deploy job fails")
	}
	recorded := status.updates[len(status.updates)-1].ProvisionError
	if !strings.Contains(recorded, chartFailure) {
		t.Fatalf("provisionError = %q, want it to carry the deploy's own failure", recorded)
	}
	if !strings.Contains(recorded, "1.2.3") {
		t.Fatalf("provisionError = %q, want it to name the version", recorded)
	}
}

func TestProvisionSaysWhereToLookWhenTheJobLeftNothing(t *testing.T) {
	status := &recordingStatusWriter{}
	provisioner := NewEnvironmentProvisioner(fakeRunner{outcome: deployexec.OutcomeFailed}, status, nil, nil)
	provisioner.backoff = 0
	params := deployexec.DeployJobParams{Tenant: "acme", Environment: "prod", Version: "1.2.3", Namespace: "acme-platform"}
	if err := provisioner.Provision(context.Background(), "env-1", params); err == nil {
		t.Fatal("expected an error when the deploy job fails")
	}
	recorded := status.updates[len(status.updates)-1].ProvisionError
	for _, want := range []string{"1.2.3", "acme-platform", deployexec.DeployJobName("acme", "prod", "1.2.3", "")} {
		if !strings.Contains(recorded, want) {
			t.Fatalf("provisionError = %q, want it to mention %q", recorded, want)
		}
	}
}

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

func TestProvisionRecordsExposeErrorButStaysRunning(t *testing.T) {
	status := &recordingStatusWriter{}
	output := "audit: erun expose --ip 203.0.113.10 --skip-if-unconfigured acme prod mcp\n" +
		"ERUN_EXPOSE_FAILED: cannot find git project\n"
	runner := fakeRunner{outcome: deployexec.OutcomeSucceeded, output: output}
	if err := provisionVersion(t, runner, status, "1.2.3"); err != nil {
		t.Fatalf("provision: %v", err)
	}
	assertTransitions(t, status.transitions, []string{"provisioning:", "running:"})
	last := status.updates[len(status.updates)-1]
	if last.ExposeError != "cannot find git project" {
		t.Fatalf("exposeError = %q, want the chained expose step's own failure", last.ExposeError)
	}
	if last.DeployedVersion != "1.2.3" {
		t.Fatalf("deployedVersion = %q, want 1.2.3 despite the expose failure", last.DeployedVersion)
	}
}

func TestProvisionLeavesExposeErrorEmptyOnCleanSuccess(t *testing.T) {
	status := &recordingStatusWriter{}
	runner := fakeRunner{outcome: deployexec.OutcomeSucceeded, output: "==> Deployed acme/prod 1.2.3 in 4s\n"}
	if err := provisionVersion(t, runner, status, "1.2.3"); err != nil {
		t.Fatalf("provision: %v", err)
	}
	last := status.updates[len(status.updates)-1]
	if last.ExposeError != "" {
		t.Fatalf("exposeError = %q, want empty on a clean success", last.ExposeError)
	}
}

func TestProvisionRecordsExposedHostnameOnSuccessfulExpose(t *testing.T) {
	status := &recordingStatusWriter{}
	runner := fakeRunner{outcome: deployexec.OutcomeSucceeded, output: "==> Deployed acme/prod 1.2.3 in 4s\n"}
	provisioner := NewEnvironmentProvisioner(runner, status, nil, nil)
	provisioner.backoff = 0
	params := deployexec.DeployJobParams{
		Tenant:                  "acme",
		Environment:             "prod",
		Version:                 "1.2.3",
		ExposeTargetIP:          "203.0.113.10",
		ExposeServicesZone:      "services.example.com",
		ExposePlatformNamespace: "acme-platform",
	}
	if err := provisioner.Provision(context.Background(), "env-1", params); err != nil {
		t.Fatalf("provision: %v", err)
	}
	last := status.updates[len(status.updates)-1]
	if want := "mcp.acme-prod.services.example.com"; last.ExposedHostname != want {
		t.Fatalf("exposedHostname = %q, want %q", last.ExposedHostname, want)
	}
}

func TestProvisionLeavesExposedHostnameEmptyOnExposeFailure(t *testing.T) {
	status := &recordingStatusWriter{}
	output := "audit: erun expose --ip 203.0.113.10 --skip-if-unconfigured acme prod mcp\n" +
		"ERUN_EXPOSE_FAILED: ingress controller unavailable\n"
	runner := fakeRunner{outcome: deployexec.OutcomeSucceeded, output: output}
	provisioner := NewEnvironmentProvisioner(runner, status, nil, nil)
	provisioner.backoff = 0
	params := deployexec.DeployJobParams{
		Tenant:                  "acme",
		Environment:             "prod",
		Version:                 "1.2.3",
		ExposeTargetIP:          "203.0.113.10",
		ExposeServicesZone:      "services.example.com",
		ExposePlatformNamespace: "acme-platform",
	}
	if err := provisioner.Provision(context.Background(), "env-1", params); err != nil {
		t.Fatalf("provision: %v", err)
	}
	last := status.updates[len(status.updates)-1]
	if last.ExposedHostname != "" {
		t.Fatalf("exposedHostname = %q, want empty when the chained expose failed", last.ExposedHostname)
	}
	if last.ExposeError != "ingress controller unavailable" {
		t.Fatalf("exposeError = %q, want the chained expose step's own failure", last.ExposeError)
	}
}

func TestProvisionLeavesExposedHostnameEmptyWhenNeverConfigured(t *testing.T) {
	status := &recordingStatusWriter{}
	runner := fakeRunner{outcome: deployexec.OutcomeSucceeded, output: "==> Deployed acme/prod 1.2.3 in 4s\n"}
	if err := provisionVersion(t, runner, status, "1.2.3"); err != nil {
		t.Fatalf("provision: %v", err)
	}
	last := status.updates[len(status.updates)-1]
	if last.ExposedHostname != "" {
		t.Fatalf("exposedHostname = %q, want empty when exposure was never configured", last.ExposedHostname)
	}
}

func TestProvisionRetriesTransientStatusWrite(t *testing.T) {
	status := &recordingStatusWriter{failFirst: 2}
	if err := provisionVersion(t, fakeRunner{outcome: deployexec.OutcomeSucceeded}, status, "1.2.3"); err != nil {
		t.Fatalf("provision should survive a transient status-write failure: %v", err)
	}
	assertTransitions(t, status.transitions, []string{"provisioning:", "running:"})
}

func TestProvisionFailsWhenStatusWriteNeverSucceeds(t *testing.T) {
	status := &recordingStatusWriter{failFirst: statusWriteAttempts}
	if err := provisionVersion(t, fakeRunner{outcome: deployexec.OutcomeSucceeded}, status, "1.2.3"); err == nil {
		t.Fatal("expected the exhausted status write to surface an error")
	}
	if status.calls != statusWriteAttempts {
		t.Fatalf("status write attempted %d times, want %d", status.calls, statusWriteAttempts)
	}
}

func TestProvisionRecordsUsageEventOnSuccess(t *testing.T) {
	status := &recordingStatusWriter{}
	usage := &recordingUsageRecorder{}
	provisioner := NewEnvironmentProvisioner(fakeRunner{outcome: deployexec.OutcomeSucceeded}, status, usage, nil)
	provisioner.backoff = 0
	params := deployexec.DeployJobParams{Version: "1.2.3", MaxCPUMillicores: 4000, MaxMemoryMB: 9216, MaxStorageGB: 80}
	if err := provisioner.Provision(context.Background(), "env-1", params); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if len(usage.events) != 1 {
		t.Fatalf("usage events recorded = %d, want 1", len(usage.events))
	}
	got := usage.events[0]
	if got.EnvironmentID != "env-1" || got.EventType != string(model.UsageEventEnvironmentProvisioned) ||
		got.CPUMillicores != 4000 || got.MemoryMB != 9216 || got.StorageGB != 80 {
		t.Fatalf("usage event = %+v", got)
	}
}

func TestProvisionRecordsNoUsageEventOnFailure(t *testing.T) {
	status := &recordingStatusWriter{}
	usage := &recordingUsageRecorder{}
	provisioner := NewEnvironmentProvisioner(fakeRunner{outcome: deployexec.OutcomeFailed}, status, usage, nil)
	provisioner.backoff = 0
	if err := provisioner.Provision(context.Background(), "env-1", deployexec.DeployJobParams{Version: "1.2.3"}); err == nil {
		t.Fatal("expected an error when the deploy job fails")
	}
	if len(usage.events) != 0 {
		t.Fatalf("usage events recorded = %d, want 0 on a failed deploy", len(usage.events))
	}
}

func TestProvisionSurvivesUsageRecordFailure(t *testing.T) {
	status := &recordingStatusWriter{}
	usage := &recordingUsageRecorder{err: errors.New("usage store unavailable")}
	provisioner := NewEnvironmentProvisioner(fakeRunner{outcome: deployexec.OutcomeSucceeded}, status, usage, nil)
	provisioner.backoff = 0
	if err := provisioner.Provision(context.Background(), "env-1", deployexec.DeployJobParams{Version: "1.2.3"}); err != nil {
		t.Fatalf("provision should survive a usage-record failure: %v", err)
	}
}

func TestProvisionRefusesWhenPlacementCredentialUnavailable(t *testing.T) {
	status := &recordingStatusWriter{}
	runner := fakeRunner{outcome: deployexec.OutcomeSucceeded}
	provisioner := NewEnvironmentProvisioner(runner, status, nil, nil)
	provisioner.backoff = 0
	params := deployexec.DeployJobParams{Version: "1.2.3", Placement: deployexec.PlacementParams{TenantID: "tenant-1", ContextID: "ctx-1"}}
	err := provisioner.Provision(context.Background(), "env-1", params)
	if err == nil {
		t.Fatal("expected an error when no placement credential resolver is configured")
	}
	if status.transitions[len(status.transitions)-1][:7] != "failed:" {
		t.Fatalf("last transition = %q, want failed:*", status.transitions[len(status.transitions)-1])
	}
}

// The token must come from the resolver on every run so a rotated token reaches the next deploy.
func TestProvisionResolvesThePlacementTokenFreshOnEveryRun(t *testing.T) {
	status := &recordingStatusWriter{}
	var seenToken string
	runner := recordingPlacementRunner{fn: func(p deployexec.DeployJobParams) {
		seenToken = p.Placement.AdminToken
	}}
	credentials := stubPlacementCredentials{token: "live-token"}
	provisioner := NewEnvironmentProvisioner(runner, status, nil, credentials)
	provisioner.backoff = 0
	params := deployexec.DeployJobParams{Version: "1.2.3", Placement: deployexec.PlacementParams{TenantID: "tenant-1", ContextID: "ctx-1"}}
	if err := provisioner.Provision(context.Background(), "env-1", params); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if seenToken != "live-token" {
		t.Fatalf("token seen by the runner = %q, want live-token", seenToken)
	}
}

type stubPlacementCredentials struct {
	token string
	err   error
}

func (s stubPlacementCredentials) Get(context.Context, string, string) (string, error) {
	return s.token, s.err
}

type recordingPlacementRunner struct {
	fn func(deployexec.DeployJobParams)
}

func (r recordingPlacementRunner) Run(_ context.Context, params deployexec.DeployJobParams) (deployexec.Result, error) {
	r.fn(params)
	return deployexec.Result{Outcome: deployexec.OutcomeSucceeded}, nil
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
