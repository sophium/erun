package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

// TestListGateRunsRefusesAnUnknownStatusFilter: the read route normalized any
// `?status=` value and handed it straight to the repository, so an
// unrecognised one matched no row and answered 200 with an empty list -- a
// mistyped filter was indistinguishable from a real "no gate runs", while the
// write route refused the same value as a named field. The refusal must name
// the accepted values, and it must happen before the query, or the caller
// still receives the empty listing this route is being fixed to stop
// producing.
func TestListGateRunsRefusesAnUnknownStatusFilter(t *testing.T) {
	repo := &stubGateRunRepository{}
	routes := GateRunRoutes{gateRuns: repo}
	req := httptest.NewRequest(http.MethodGet, "/v1/gate-runs?status=bogus-not-a-status", nil)
	rec := httptest.NewRecorder()

	routes.listGateRuns(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	var body errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not JSON: %v", err)
	}
	if body.Code != "INVALID_QUERY" {
		t.Fatalf("code = %q, want %q", body.Code, "INVALID_QUERY")
	}
	for _, want := range []string{"RUNNING", "PASSED", "FAILED", "INCONCLUSIVE"} {
		if !strings.Contains(body.Message, want) {
			t.Fatalf("message %q does not name the accepted value %s", body.Message, want)
		}
	}
	if repo.listFilter.Status != "" {
		t.Fatal("the repository was queried anyway; the refusal must precede it")
	}
}

// TestListGateRunsAcceptsRunningAsAStatusFilter: the write side's vocabulary
// is terminal-only (RUNNING is what Start assigns, never what ReportOutcome
// may report), but the read side filters the queue's own current work -- a
// RUNNING filter is the question "what is being gated right now". Validating
// the filter against the write side's three terminal statuses would refuse it.
func TestListGateRunsAcceptsRunningAsAStatusFilter(t *testing.T) {
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

// TestListGateRunsNormalizesLowercaseStatusFilter: startGateRun/reportGateRunOutcome
// already normalize a lowercase status so a caller following `erun exec
// gate-run report`'s own lowercase examples is accepted -- every status this
// API ever stores is therefore uppercase. listGateRuns must normalize its own
// `?status=` filter the same way, or `GET /v1/gate-runs?status=failed`
// silently returns zero rows against real, uppercase-stored data instead of
// matching them. The filter is validated as well as normalized, so the check
// has to be a membership test on the normalized value: an entry point that
// validated the raw `?status=` first would refuse `failed` for not being
// spelled `FAILED` and break the case-insensitivity the CLI's examples rely
// on.
func TestListGateRunsNormalizesLowercaseStatusFilter(t *testing.T) {
	for _, spelling := range []string{"failed", "FAILED", " Failed "} {
		t.Run(spelling, func(t *testing.T) {
			repo := &stubGateRunRepository{}
			routes := GateRunRoutes{gateRuns: repo}
			req := httptest.NewRequest(http.MethodGet, "/v1/gate-runs?status="+url.QueryEscape(spelling), nil)
			rec := httptest.NewRecorder()

			routes.listGateRuns(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			if repo.listFilter.Status != model.GateRunStatusFailed {
				t.Fatalf("repository received status filter = %q, want %q", repo.listFilter.Status, model.GateRunStatusFailed)
			}
		})
	}
}
