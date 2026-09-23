package routes

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
	eruncommon "github.com/sophium/erun/erun-common"
)

type GateRunRepository interface {
	// Get takes the caller's tenant alongside the gate run id: gate_runs is
	// tenant-owned and erun_operations' RLS policy is unconditional, so an
	// id-only lookup would answer an OPERATIONS caller with a stranger's run.
	Get(ctx context.Context, tenantID, gateRunID string) (model.GateRun, error)
	List(ctx context.Context, filter apirepository.GateRunFilter) ([]model.GateRun, error)
}

type GateRunService interface {
	Start(ctx context.Context, run model.GateRun) (model.GateRun, error)
	ReportOutcome(ctx context.Context, tenantID, gateRunID string, status model.GateRunStatus, failingStep, logRef, mergeCommit string) (model.GateRun, error)
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

// listGateRuns answers GET /v1/gate-runs. The `?status=` filter is normalized
// and then validated before it reaches the repository: an unrecognised value
// matches no row, so passing one through answered `200` with an empty list
// that a caller reads as "no gate runs" -- indistinguishable from a real
// empty result on the merge queue's audit trail. The membership check is the
// shared eruncommon.NormalizeGateRunStatus, the same refusal `erun gate list`
// makes before it ever calls the platform, so both surfaces accept the same
// spellings and name the same accepted values.
//
// The order is deliberate: the filter is resolved to its stored spelling here
// first, so all three status entry points -- list, start, report -- normalize
// their own input and the resolved value is what reaches the repository.
// Handing the raw value to the shared helper and taking its resolved return
// instead would drop the entry-point normalization from this route.
func (r GateRunRoutes) listGateRuns(w http.ResponseWriter, req *http.Request) {
	query := req.URL.Query()
	status := model.GateRunStatus(strings.ToUpper(strings.TrimSpace(query.Get("status"))))
	if _, err := eruncommon.NormalizeGateRunStatus(string(status)); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_QUERY", err.Error())
		return
	}
	filter := apirepository.GateRunFilter{
		TargetBranch: query.Get("targetBranch"),
		SourceBranch: query.Get("sourceBranch"),
		Status:       status,
	}
	runs, err := r.gateRuns.List(req.Context(), filter)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (r GateRunRoutes) getGateRun(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), errors.New("security context not found in request"))
		return
	}
	// A gate run read through this route is always the caller's own, so a
	// cross-tenant id answers 404 exactly as an id that names nothing does.
	run, err := r.gateRuns.Get(req.Context(), securityContext.TenantID, req.PathValue("gate_run_id"))
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
	run.Status = model.GateRunStatus(strings.ToUpper(strings.TrimSpace(string(run.Status))))
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
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), errors.New("security context not found in request"))
		return
	}
	input.Status = model.GateRunStatus(strings.ToUpper(strings.TrimSpace(string(input.Status))))
	// Only the caller's own gate run can be decided: a verdict for a
	// cross-tenant id fails the same not-found lookup the read makes, so it
	// answers 404 rather than confirming the run exists.
	run, err := r.service.ReportOutcome(req.Context(), securityContext.TenantID, req.PathValue("gate_run_id"), input.Status, input.FailingStep, input.LogRef, input.MergeCommit)
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
