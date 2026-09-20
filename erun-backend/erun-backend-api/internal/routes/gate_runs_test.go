package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

type stubGateRunRepository struct {
	listFilter apirepository.GateRunFilter
}

func (s *stubGateRunRepository) Get(_ context.Context, gateRunID string) (model.GateRun, error) {
	return model.GateRun{GateRunID: gateRunID}, nil
}

func (s *stubGateRunRepository) List(_ context.Context, filter apirepository.GateRunFilter) ([]model.GateRun, error) {
	s.listFilter = filter
	return nil, nil
}

type stubGateRunService struct {
	startedStatus  model.GateRunStatus
	reportedStatus model.GateRunStatus
}

func (s *stubGateRunService) Start(_ context.Context, run model.GateRun) (model.GateRun, error) {
	s.startedStatus = run.Status
	run.GateRunID = "gate-run-1"
	return run, nil
}

func (s *stubGateRunService) ReportOutcome(_ context.Context, gateRunID string, status model.GateRunStatus, _, _, _ string) (model.GateRun, error) {
	s.reportedStatus = status
	return model.GateRun{GateRunID: gateRunID, Status: status}, nil
}

// TestStartGateRunNormalizesLowercaseStatus: `erun exec gate-run start`'s own
// help documents lowercase ("failed", "inconclusive"); the route must accept
// what it teaches rather than pass the caller's case straight through to the
// service and its FAILED/INCONCLUSIVE checks.
func TestStartGateRunNormalizesLowercaseStatus(t *testing.T) {
	svc := &stubGateRunService{}
	routes := GateRunRoutes{service: svc}
	req := httptest.NewRequest(http.MethodPost, "/v1/gate-runs",
		bytes.NewBufferString(`{"sourceBranch":"feature/x","targetBranch":"main","sourceCommit":"abc","status":"failed"}`))
	rec := httptest.NewRecorder()

	routes.startGateRun(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if svc.startedStatus != model.GateRunStatusFailed {
		t.Fatalf("service received status = %q, want %q", svc.startedStatus, model.GateRunStatusFailed)
	}
}

// TestReportGateRunOutcomeNormalizesLowercaseStatus: `erun exec gate-run
// report`'s own Example lines are lowercase ("passed", "failed",
// "inconclusive"); copying one verbatim must not be refused as INVALID_BODY.
func TestReportGateRunOutcomeNormalizesLowercaseStatus(t *testing.T) {
	svc := &stubGateRunService{}
	routes := GateRunRoutes{service: svc}
	req := httptest.NewRequest(http.MethodPatch, "/v1/gate-runs/gate-run-1",
		bytes.NewBufferString(`{"status":"inconclusive","logRef":"wrapper hit its own 8m cap"}`))
	req.SetPathValue("gate_run_id", "gate-run-1")
	rec := httptest.NewRecorder()

	routes.reportGateRunOutcome(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if svc.reportedStatus != model.GateRunStatusInconclusive {
		t.Fatalf("service received status = %q, want %q", svc.reportedStatus, model.GateRunStatusInconclusive)
	}
}

// TestListGateRunsNormalizesLowercaseStatusFilter: startGateRun/reportGateRunOutcome
// already normalize a lowercase status so a caller following `erun exec
// gate-run report`'s own lowercase examples is accepted -- every status this
// API ever stores is therefore uppercase. listGateRuns must normalize its own
// `?status=` filter the same way, or `GET /v1/gate-runs?status=failed`
// silently returns zero rows against real, uppercase-stored data instead of
// matching them.
func TestListGateRunsNormalizesLowercaseStatusFilter(t *testing.T) {
	repo := &stubGateRunRepository{}
	routes := GateRunRoutes{gateRuns: repo}
	req := httptest.NewRequest(http.MethodGet, "/v1/gate-runs?status=failed", nil)
	rec := httptest.NewRecorder()

	routes.listGateRuns(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if repo.listFilter.Status != model.GateRunStatusFailed {
		t.Fatalf("repository received status filter = %q, want %q", repo.listFilter.Status, model.GateRunStatusFailed)
	}
}

// TestListGateRunsRejectsUnknownStatusFilter: an unknown `?status=` must be
// refused with the write route's own named-field error, not answered with the
// zero rows a legitimately empty result returns -- the two are indistinguishable
// to an operator, the same ambiguity the outcome route already refuses one
// layer over.
func TestListGateRunsRejectsUnknownStatusFilter(t *testing.T) {
	repo := &stubGateRunRepository{}
	routes := GateRunRoutes{gateRuns: repo}
	req := httptest.NewRequest(http.MethodGet, "/v1/gate-runs?status=bogus-status", nil)
	rec := httptest.NewRecorder()

	routes.listGateRuns(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details struct {
			Field string `json:"field"`
		} `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v: %s", err, rec.Body.String())
	}
	if body.Code != "INVALID_BODY" {
		t.Fatalf("code = %q, want INVALID_BODY: %s", body.Code, rec.Body.String())
	}
	if body.Details.Field != "status" {
		t.Fatalf("details.field = %q, want %q: %s", body.Details.Field, "status", rec.Body.String())
	}
	if repo.listFilter != (apirepository.GateRunFilter{}) {
		t.Fatalf("repository was queried with %+v despite the bad filter", repo.listFilter)
	}
}

// TestListGateRunsAcceptsRunningStatusFilter: RUNNING is a valid gate_runs
// status, so narrowing the list to runs still in flight must keep working even
// though the write route -- which only ever reports outcomes -- refuses it.
// This is why the filter validates against the status domain rather than the
// write path's terminal-only subset.
func TestListGateRunsAcceptsRunningStatusFilter(t *testing.T) {
	repo := &stubGateRunRepository{}
	routes := GateRunRoutes{gateRuns: repo}
	req := httptest.NewRequest(http.MethodGet, "/v1/gate-runs?status=running", nil)
	rec := httptest.NewRecorder()

	routes.listGateRuns(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if repo.listFilter.Status != model.GateRunStatusRunning {
		t.Fatalf("repository received status filter = %q, want %q", repo.listFilter.Status, model.GateRunStatusRunning)
	}
}
