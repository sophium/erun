package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// InvalidGateRunInputError refuses a gate run write whose field does not
// satisfy the gate_runs table's own contract, named up front instead of
// surfacing as a generic 400 once the database's CHECK constraint fires.
type InvalidGateRunInputError struct {
	Field  string
	Reason string
}

func (e *InvalidGateRunInputError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

func (e *InvalidGateRunInputError) Unwrap() error { return repository.ErrInvalidInput }

// GateRunAlreadyDecidedError refuses reporting an outcome against a gate run
// that already has one: a verdict is immutable once reached, the same
// discipline that keeps a review's own recorded builds append-only.
type GateRunAlreadyDecidedError struct {
	GateRunID string
	Status    model.GateRunStatus
}

func (e *GateRunAlreadyDecidedError) Error() string {
	return fmt.Sprintf("gate run %s already reached %s; a verdict cannot be re-reported", e.GateRunID, e.Status)
}

func (e *GateRunAlreadyDecidedError) Unwrap() error { return repository.ErrConflict }

type GateRunRepository interface {
	Create(ctx context.Context, run model.GateRun) (model.GateRun, error)
	Get(ctx context.Context, gateRunID string) (model.GateRun, error)
	Update(ctx context.Context, run model.GateRun) (model.GateRun, error)
}

type GateRunService struct {
	gateRuns GateRunRepository
}

func NewGateRunService(gateRuns GateRunRepository) *GateRunService {
	return &GateRunService{gateRuns: gateRuns}
}

// terminalGateRunStatuses are the outcomes ReportOutcome may report; RUNNING
// is only ever the status Start assigns.
var terminalGateRunStatuses = map[model.GateRunStatus]bool{
	model.GateRunStatusPassed:       true,
	model.GateRunStatusFailed:       true,
	model.GateRunStatusInconclusive: true,
}

// Start records the beginning of one gate attempt. A caller with no
// trackable "running" phase (e.g. a squash conflict before any build ever
// starts) may instead call Start already carrying a terminal status —
// mergeCommit empty and failingStep set is exactly that case.
func (s *GateRunService) Start(ctx context.Context, run model.GateRun) (model.GateRun, error) {
	if strings.TrimSpace(run.SourceBranch) == "" {
		return model.GateRun{}, &InvalidGateRunInputError{Field: "sourceBranch", Reason: "is required"}
	}
	if strings.TrimSpace(run.TargetBranch) == "" {
		return model.GateRun{}, &InvalidGateRunInputError{Field: "targetBranch", Reason: "is required"}
	}
	if strings.TrimSpace(run.SourceCommit) == "" {
		return model.GateRun{}, &InvalidGateRunInputError{Field: "sourceCommit", Reason: "is required"}
	}
	if run.Status == "" {
		run.Status = model.GateRunStatusRunning
	}
	if err := validateGateRunOutcome(run.Status, run.MergeCommit, run.FailingStep); err != nil {
		return model.GateRun{}, err
	}
	return s.gateRuns.Create(ctx, run)
}

// ReportOutcome moves an existing gate run from RUNNING to a terminal
// status. Reporting against a gate run that already has one is refused —
// see GateRunAlreadyDecidedError.
func (s *GateRunService) ReportOutcome(ctx context.Context, gateRunID string, status model.GateRunStatus, failingStep, logRef, mergeCommit string) (model.GateRun, error) {
	if !terminalGateRunStatuses[status] {
		return model.GateRun{}, &InvalidGateRunInputError{Field: "status", Reason: "must be PASSED, FAILED, or INCONCLUSIVE"}
	}
	existing, err := s.gateRuns.Get(ctx, gateRunID)
	if err != nil {
		return model.GateRun{}, err
	}
	if existing.Status != model.GateRunStatusRunning {
		return model.GateRun{}, &GateRunAlreadyDecidedError{GateRunID: gateRunID, Status: existing.Status}
	}
	merged := existing.MergeCommit
	if strings.TrimSpace(mergeCommit) != "" {
		merged = mergeCommit
	}
	if err := validateGateRunOutcome(status, merged, failingStep); err != nil {
		return model.GateRun{}, err
	}
	existing.Status = status
	existing.FailingStep = failingStep
	existing.LogRef = logRef
	existing.MergeCommit = merged
	return s.gateRuns.Update(ctx, existing)
}

// validateGateRunOutcome mirrors the gate_runs table's own CHECK constraints
// so a violation is named up front rather than surfaced as a generic 400.
func validateGateRunOutcome(status model.GateRunStatus, mergeCommit, failingStep string) error {
	if status == model.GateRunStatusFailed && strings.TrimSpace(failingStep) == "" {
		return &InvalidGateRunInputError{Field: "failingStep", Reason: "is required when status is FAILED"}
	}
	if status != model.GateRunStatusFailed && status != model.GateRunStatusInconclusive && strings.TrimSpace(mergeCommit) == "" {
		return &InvalidGateRunInputError{Field: "mergeCommit", Reason: "is required unless status is FAILED or INCONCLUSIVE"}
	}
	return nil
}
