package service

import (
	"context"
	"errors"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

type fakeGateRunRepo struct {
	runs   map[string]*model.GateRun
	nextID int
}

func newFakeGateRunRepo() *fakeGateRunRepo {
	return &fakeGateRunRepo{runs: map[string]*model.GateRun{}}
}

func (f *fakeGateRunRepo) Create(_ context.Context, run model.GateRun) (model.GateRun, error) {
	f.nextID++
	created := run
	created.GateRunID = "gate-run-" + string(rune('0'+f.nextID))
	f.runs[created.GateRunID] = &created
	return created, nil
}

func (f *fakeGateRunRepo) Get(_ context.Context, gateRunID string) (model.GateRun, error) {
	r, ok := f.runs[gateRunID]
	if !ok {
		return model.GateRun{}, repository.ErrNotFound
	}
	return *r, nil
}

func (f *fakeGateRunRepo) Update(_ context.Context, run model.GateRun) (model.GateRun, error) {
	if _, ok := f.runs[run.GateRunID]; !ok {
		return model.GateRun{}, repository.ErrNotFound
	}
	r := run
	f.runs[run.GateRunID] = &r
	return r, nil
}

func TestGateRunServiceStartRefusesMissingRequiredFields(t *testing.T) {
	svc := NewGateRunService(newFakeGateRunRepo())
	_, err := svc.Start(context.Background(), model.GateRun{TargetBranch: "main", SourceCommit: "abc"})
	var invalid *InvalidGateRunInputError
	if !errors.As(err, &invalid) || invalid.Field != "sourceBranch" {
		t.Fatalf("expected InvalidGateRunInputError on sourceBranch, got %v", err)
	}
}

func TestGateRunServiceStartRefusesRunningWithNoMergeCommit(t *testing.T) {
	svc := NewGateRunService(newFakeGateRunRepo())
	_, err := svc.Start(context.Background(), model.GateRun{SourceBranch: "feature/x", TargetBranch: "main", SourceCommit: "abc"})
	var invalid *InvalidGateRunInputError
	if !errors.As(err, &invalid) || invalid.Field != "mergeCommit" {
		t.Fatalf("expected InvalidGateRunInputError on mergeCommit, got %v", err)
	}
}

// A squash conflict has no trackable "running" phase and no merge commit at
// all -- Start must accept reporting FAILED directly, in one call.
func TestGateRunServiceStartAcceptsImmediateFailureWithNoMergeCommit(t *testing.T) {
	svc := NewGateRunService(newFakeGateRunRepo())
	run, err := svc.Start(context.Background(), model.GateRun{
		SourceBranch: "feature/x", TargetBranch: "main", SourceCommit: "abc",
		Status: model.GateRunStatusFailed, FailingStep: "git merge --squash",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Status != model.GateRunStatusFailed || run.MergeCommit != "" {
		t.Fatalf("expected FAILED with empty mergeCommit, got status=%s mergeCommit=%q", run.Status, run.MergeCommit)
	}
}

func TestGateRunServiceReportOutcomeRefusesFailedWithNoFailingStep(t *testing.T) {
	repo := newFakeGateRunRepo()
	svc := NewGateRunService(repo)
	started, err := svc.Start(context.Background(), model.GateRun{
		SourceBranch: "feature/x", TargetBranch: "main", SourceCommit: "abc", MergeCommit: "def",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_, err = svc.ReportOutcome(context.Background(), started.GateRunID, model.GateRunStatusFailed, "", "", "")
	var invalid *InvalidGateRunInputError
	if !errors.As(err, &invalid) || invalid.Field != "failingStep" {
		t.Fatalf("expected InvalidGateRunInputError on failingStep, got %v", err)
	}
}

// This is the caution the issue names explicitly: a wrapper that never
// reached a real verdict must report INCONCLUSIVE, and INCONCLUSIVE must be
// reportable with no failingStep -- it is not a red verdict.
func TestGateRunServiceReportOutcomeAcceptsInconclusiveWithNoFailingStep(t *testing.T) {
	repo := newFakeGateRunRepo()
	svc := NewGateRunService(repo)
	started, err := svc.Start(context.Background(), model.GateRun{
		SourceBranch: "feature/x", TargetBranch: "main", SourceCommit: "abc", MergeCommit: "def",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	reported, err := svc.ReportOutcome(context.Background(), started.GateRunID, model.GateRunStatusInconclusive, "", "wrapper hit its own timeout cap", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reported.Status != model.GateRunStatusInconclusive {
		t.Fatalf("expected INCONCLUSIVE, got %s", reported.Status)
	}
}

// A verdict is immutable once reached -- re-reporting against an
// already-decided gate run must be refused rather than silently overwritten.
func TestGateRunServiceReportOutcomeRefusesAlreadyDecided(t *testing.T) {
	repo := newFakeGateRunRepo()
	svc := NewGateRunService(repo)
	started, err := svc.Start(context.Background(), model.GateRun{
		SourceBranch: "feature/x", TargetBranch: "main", SourceCommit: "abc", MergeCommit: "def",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := svc.ReportOutcome(context.Background(), started.GateRunID, model.GateRunStatusPassed, "", "", ""); err != nil {
		t.Fatalf("first report: %v", err)
	}
	_, err = svc.ReportOutcome(context.Background(), started.GateRunID, model.GateRunStatusFailed, "erun build", "", "")
	var alreadyDecided *GateRunAlreadyDecidedError
	if !errors.As(err, &alreadyDecided) {
		t.Fatalf("expected GateRunAlreadyDecidedError, got %v", err)
	}
}
