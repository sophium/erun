package routes

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

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
