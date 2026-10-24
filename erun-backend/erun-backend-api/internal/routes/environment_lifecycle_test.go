package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/provision"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type stubEnvironmentLifecycle struct {
	stopped []provision.EnvLifecycleInput
	err     error
}

func (s *stubEnvironmentLifecycle) Stop(_ context.Context, in provision.EnvLifecycleInput) error {
	s.stopped = append(s.stopped, in)
	return s.err
}

type stubEnvironmentDeleter struct {
	started []provision.EnvDeleteInput
	err     error
}

func (s *stubEnvironmentDeleter) Start(in provision.EnvDeleteInput) error {
	s.started = append(s.started, in)
	return s.err
}

func postStopEnvironment(t *testing.T, environments *stubEnvironmentRepository, lifecycle EnvironmentLifecycle) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/environments/env-1/stop", nil)
	req.SetPathValue("environment_id", "env-1")
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   "t1",
		TenantType: string(model.TenantTypeCompany),
		ErunUserID: "u1",
	}))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{
		environments: environments,
		contexts:     &stubContextRepository{},
		quotas:       underCapQuota,
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		lifecycle:    lifecycle,
	}.stopEnvironment(rec, req)
	return rec
}

func deleteEnvironmentRequest(t *testing.T, environments *stubEnvironmentRepository, deleter EnvironmentDeleter) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/v1/environments/env-1", nil)
	req.SetPathValue("environment_id", "env-1")
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   "t1",
		TenantType: string(model.TenantTypeCompany),
		ErunUserID: "u1",
	}))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{
		environments: environments,
		contexts:     &stubContextRepository{},
		quotas:       underCapQuota,
		tenants:      stubConfigTenantRepository{tenant: model.Tenant{Name: "acme"}},
		deleter:      deleter,
	}.deleteEnvironment(rec, req)
	return rec
}

func TestStopEnvironmentRunsLifecycleStop(t *testing.T) {
	lifecycle := &stubEnvironmentLifecycle{}
	rec := postStopEnvironment(t, runtimeEnvironment(), lifecycle)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s), want 200", rec.Code, rec.Body.String())
	}
	if len(lifecycle.stopped) != 1 {
		t.Fatalf("Stop called %d times, want 1", len(lifecycle.stopped))
	}
	got := lifecycle.stopped[0]
	if got.Tenant != "acme" || got.Environment != "prod" || got.EnvironmentID != "env-1" || got.RunningVersion != "1.2.3" {
		t.Fatalf("stop input = %+v", got)
	}
}

func TestStopEnvironmentRejectsNonRuntime(t *testing.T) {
	environments := &stubEnvironmentRepository{environment: model.Environment{
		EnvironmentID: "env-1", Name: "agents", Type: model.EnvironmentTypeRemoteAgent,
	}}
	lifecycle := &stubEnvironmentLifecycle{}
	rec := postStopEnvironment(t, environments, lifecycle)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 Bad Request", rec.Code)
	}
	if len(lifecycle.stopped) != 0 {
		t.Fatal("a non-runtime environment must never reach the stop lifecycle")
	}
}

func TestStopEnvironmentReportsUnconfiguredExecutor(t *testing.T) {
	rec := postStopEnvironment(t, runtimeEnvironment(), nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 Not Implemented", rec.Code)
	}
}

func TestStopEnvironmentSurfacesLifecycleFailure(t *testing.T) {
	lifecycle := &stubEnvironmentLifecycle{err: foreignKeyError{}}
	rec := postStopEnvironment(t, runtimeEnvironment(), lifecycle)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 Bad Gateway", rec.Code)
	}
}

// TestDeleteEnvironmentStartsDeleteAsynchronously pins #1140's bounded-delete
// contract: the handler claims the delete (moving the row to `deleting`) and
// starts the durable workflow, then returns 202 immediately rather than
// waiting on the teardown itself — which could run long or wedge entirely.
func TestDeleteEnvironmentStartsDeleteAsynchronously(t *testing.T) {
	environments := runtimeEnvironment()
	deleter := &stubEnvironmentDeleter{}
	rec := deleteEnvironmentRequest(t, environments, deleter)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d (body %s), want 202 Accepted", rec.Code, rec.Body.String())
	}
	if environments.claimDeleteCalls != 1 {
		t.Fatalf("ClaimDelete called %d times, want 1", environments.claimDeleteCalls)
	}
	if len(deleter.started) != 1 {
		t.Fatalf("Start called %d times, want 1", len(deleter.started))
	}

	var response model.Environment
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != model.EnvironmentStatusDeleting {
		t.Fatalf("response status = %q, want %q — `running` must not survive a delete attempt", response.Status, model.EnvironmentStatusDeleting)
	}
}

// TestDeleteEnvironmentResolvesTheDeleteInput checks the resolved
// provision.EnvDeleteInput's fields separately from the response-shape
// assertions above, so each assertion failure names exactly what went wrong.
func TestDeleteEnvironmentResolvesTheDeleteInput(t *testing.T) {
	deleter := &stubEnvironmentDeleter{}
	deleteEnvironmentRequest(t, runtimeEnvironment(), deleter)

	if len(deleter.started) != 1 {
		t.Fatalf("Start called %d times, want 1", len(deleter.started))
	}
	got := deleter.started[0]
	assertEqual(t, "Tenant", got.Tenant, "acme")
	assertEqual(t, "Environment", got.Environment, "prod")
	assertEqual(t, "EnvironmentID", got.EnvironmentID, "env-1")
	assertEqual(t, "RunningVersion", got.RunningVersion, "1.2.3")
	if got.DeleteID == "" {
		t.Fatal("a fresh delete attempt must get its own delete id, or a retry would replay this attempt's cached result")
	}
}

func assertEqual(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %q, want %q", field, got, want)
	}
}

// TestDeleteEnvironmentAllowsNonRuntime: a remote-agent/local-agent row was
// never server-side deployed, so deleting it is a plain row removal — still
// routed through the durable workflow, which skips the Job when
// RunningVersion is empty.
func TestDeleteEnvironmentAllowsNonRuntime(t *testing.T) {
	environments := &stubEnvironmentRepository{environment: model.Environment{
		EnvironmentID: "env-1", Name: "agents", Type: model.EnvironmentTypeRemoteAgent,
	}}
	deleter := &stubEnvironmentDeleter{}
	rec := deleteEnvironmentRequest(t, environments, deleter)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d (body %s), want 202 Accepted", rec.Code, rec.Body.String())
	}
	if len(deleter.started) != 1 {
		t.Fatalf("Start called %d times, want 1", len(deleter.started))
	}
	if deleter.started[0].RunningVersion != "" {
		t.Fatalf("running version = %q, want empty for a never-deployed environment", deleter.started[0].RunningVersion)
	}
}

func TestDeleteEnvironmentReportsUnconfiguredExecutor(t *testing.T) {
	rec := deleteEnvironmentRequest(t, runtimeEnvironment(), nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 Not Implemented", rec.Code)
	}
}

// TestDeleteEnvironmentRejectsConcurrentDelete mirrors
// TestDeployEnvironmentRejectsConcurrentDeploy: a double-submit must not
// start two delete Jobs against the same namespace.
func TestDeleteEnvironmentRejectsConcurrentDelete(t *testing.T) {
	environments := runtimeEnvironment()
	environments.claimDeleteTaken = true
	deleter := &stubEnvironmentDeleter{}
	rec := deleteEnvironmentRequest(t, environments, deleter)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d (body %s), want 409 Conflict", rec.Code, rec.Body.String())
	}
	if len(deleter.started) != 0 {
		t.Fatal("a delete already in progress must not start a second attempt")
	}
}

// The three tests below cover the refusal *messages* for #1163. The guard that
// produces the refusal lives in ClaimDeploy/ClaimDelete's SQL, which this
// package's stub repository cannot exercise and which no Postgres-backed
// harness exists for here — that half is verified against a live control plane
// instead. What these hold is the half that is testable in Go: that a refusal
// caused by an outstanding teardown is reported as such, rather than as the
// misleading "a deploy is already in progress" a caller used to get.

// TestDeployEnvironmentRefusalNamesAnOutstandingDelete: a deploy refused
// because the environment is mid-teardown must say so. The old message sent
// the caller looking for a concurrent deploy that does not exist.
func TestDeployEnvironmentRefusalNamesAnOutstandingDelete(t *testing.T) {
	for _, tc := range []struct {
		status model.EnvironmentStatus
		want   string
	}{
		{model.EnvironmentStatusDeleting, "being deleted"},
		{model.EnvironmentStatusDeletionBlocked, "delete is blocked"},
	} {
		environments := runtimeEnvironment()
		environments.environment.Status = tc.status
		environments.claimTaken = true
		prov := &stubEnvironmentProvisioner{}
		rec := postDeployEnvironment(t, environments, prov, "")

		if rec.Code != http.StatusConflict {
			t.Fatalf("status %q: code = %d, want 409 Conflict", tc.status, rec.Code)
		}
		if body := rec.Body.String(); !strings.Contains(body, tc.want) {
			t.Fatalf("status %q: body %s does not mention %q", tc.status, body, tc.want)
		}
		if len(prov.deployed) != 0 {
			t.Fatalf("status %q: StartDeploy ran %d times, want 0", tc.status, len(prov.deployed))
		}
	}
}

// TestDeleteEnvironmentRefusalNamesAnInFlightDeploy is the mirror: a delete
// refused because a deploy holds the row should point at the deploy, since
// waiting for it is the actionable step.
func TestDeleteEnvironmentRefusalNamesAnInFlightDeploy(t *testing.T) {
	environments := runtimeEnvironment()
	environments.environment.Status = model.EnvironmentStatusProvisioning
	environments.claimDeleteTaken = true
	deleter := &stubEnvironmentDeleter{}
	rec := deleteEnvironmentRequest(t, environments, deleter)

	if rec.Code != http.StatusConflict {
		t.Fatalf("code = %d, want 409 Conflict", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "a deploy is in progress") {
		t.Fatalf("body %s does not name the in-flight deploy", body)
	}
	if len(deleter.started) != 0 {
		t.Fatalf("Start ran %d times, want 0", len(deleter.started))
	}
}

// TestClaimRefusalMessages pins each mapping directly, including that an
// ordinary status still yields the original concurrent-lifecycle wording — the
// teardown cases must not swallow the common one.
func TestClaimRefusalMessages(t *testing.T) {
	if got := deployClaimRefusal(model.EnvironmentStatusRunning); got != "a deploy is already in progress for this environment" {
		t.Fatalf("running deploy refusal = %q, want the concurrent-deploy message", got)
	}
	if got := deployClaimRefusal(model.EnvironmentStatusDeleting); !strings.Contains(got, "being deleted") {
		t.Fatalf("deleting deploy refusal = %q", got)
	}
	if got := deployClaimRefusal(model.EnvironmentStatusDeletionBlocked); !strings.Contains(got, "delete is blocked") {
		t.Fatalf("deletion-blocked deploy refusal = %q", got)
	}
	if got := deleteClaimRefusal(model.EnvironmentStatusRunning); got != "a delete is already in progress for this environment" {
		t.Fatalf("running delete refusal = %q, want the concurrent-delete message", got)
	}
	if got := deleteClaimRefusal(model.EnvironmentStatusProvisioning); !strings.Contains(got, "a deploy is in progress") {
		t.Fatalf("provisioning delete refusal = %q", got)
	}
}

// TestDeleteEnvironmentStartFailureMarksBlocked mirrors
// TestDeployEnvironmentStartFailureMarksFailed: ClaimDelete already moved the
// row to `deleting` before Start ran, so a failure to even enqueue the
// durable workflow must not strand it there — it moves to deletion-blocked.
func TestDeleteEnvironmentStartFailureMarksBlocked(t *testing.T) {
	environments := runtimeEnvironment()
	deleter := &stubEnvironmentDeleter{err: foreignKeyError{}}
	rec := deleteEnvironmentRequest(t, environments, deleter)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d (body %s), want 500", rec.Code, rec.Body.String())
	}
	if environments.markDeleteBlockedCalls != 1 {
		t.Fatalf("MarkDeleteBlocked called %d times, want 1", environments.markDeleteBlockedCalls)
	}
	if environments.markDeleteBlockedReason == "" {
		t.Fatal("MarkDeleteBlocked must record why the workflow could not be started")
	}
}
