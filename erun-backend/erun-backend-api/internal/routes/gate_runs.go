package routes

import (
	"context"
	"errors"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

type GateRunRepository interface {
	Get(ctx context.Context, gateRunID string) (model.GateRun, error)
	List(ctx context.Context, filter apirepository.GateRunFilter) ([]model.GateRun, error)
}

type GateRunService interface {
	Start(ctx context.Context, run model.GateRun) (model.GateRun, error)
	ReportOutcome(ctx context.Context, gateRunID string, status model.GateRunStatus, failingStep, logRef, mergeCommit string) (model.GateRun, error)
}

type GateRunRoutes struct {
	gateRuns GateRunRepository
	service  GateRunService
}

func RegisterGateRunRoutes(register ProtectedRouteRegistrar, gateRuns GateRunRepository, gateRunService GateRunService) {
	routes := GateRunRoutes{gateRuns: gateRuns, service: gateRunService}
	register(http.MethodGet, "/v1/gate-runs", http.HandlerFunc(routes.listGateRuns))
	register(http.MethodPost, "/v1/gate-runs", http.HandlerFunc(routes.startGateRun))
	register(http.MethodGet, "/v1/gate-runs/{gate_run_id}", http.HandlerFunc(routes.getGateRun))
	register(http.MethodPatch, "/v1/gate-runs/{gate_run_id}", http.HandlerFunc(routes.reportGateRunOutcome))
}

func (r GateRunRoutes) listGateRuns(w http.ResponseWriter, req *http.Request) {
	query := req.URL.Query()
	filter := apirepository.GateRunFilter{
		TargetBranch: query.Get("targetBranch"),
		SourceBranch: query.Get("sourceBranch"),
		Status:       model.GateRunStatus(query.Get("status")),
	}
	runs, err := r.gateRuns.List(req.Context(), filter)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (r GateRunRoutes) getGateRun(w http.ResponseWriter, req *http.Request) {
	run, err := r.gateRuns.Get(req.Context(), req.PathValue("gate_run_id"))
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (r GateRunRoutes) startGateRun(w http.ResponseWriter, req *http.Request) {
	var run model.GateRun
	if err := decodeJSON(req, &run); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	run, err := r.service.Start(req.Context(), run)
	if err != nil {
		writeGateRunError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, run)
}

type reportGateRunOutcomeRequest struct {
	Status      model.GateRunStatus `json:"status"`
	FailingStep string              `json:"failingStep,omitempty"`
	LogRef      string              `json:"logRef,omitempty"`
	MergeCommit string              `json:"mergeCommit,omitempty"`
}

func (r GateRunRoutes) reportGateRunOutcome(w http.ResponseWriter, req *http.Request) {
	var input reportGateRunOutcomeRequest
	if err := decodeJSON(req, &input); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	run, err := r.service.ReportOutcome(req.Context(), req.PathValue("gate_run_id"), input.Status, input.FailingStep, input.LogRef, input.MergeCommit)
	if err != nil {
		writeGateRunError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// writeGateRunError gives InvalidGateRunInputError and
// GateRunAlreadyDecidedError their own machine codes; every other failure
// falls through to the generic status-derived code.
func writeGateRunError(w http.ResponseWriter, req *http.Request, err error) {
	var invalidInput *service.InvalidGateRunInputError
	if errors.As(err, &invalidInput) {
		writeErrorDetails(w, http.StatusBadRequest, "INVALID_BODY", invalidInput.Error(), map[string]any{"field": invalidInput.Field})
		return
	}
	var alreadyDecided *service.GateRunAlreadyDecidedError
	if errors.As(err, &alreadyDecided) {
		writeErrorCode(w, http.StatusConflict, "GATE_RUN_ALREADY_DECIDED", alreadyDecided.Error())
		return
	}
	writeRepositoryError(w, req, err)
}
