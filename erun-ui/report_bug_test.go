package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-ui/internal/investigate"
	"github.com/sophium/erun/erun-ui/internal/terminal"
)

// A refusal is reported as a value, carrying the existing session's id when
// one already covers the failure.

func TestReportBugFailureWithNoDiagnosticContentIsRefused(t *testing.T) {
	harness := newInvestigationHarness(t)
	outcome, err := harness.app.ReportBugFailure(investigateThinDeployReport, "frs", "dev", 80, 24)
	if err != nil {
		t.Fatalf("a refusal must be reported as a value, not a Go error: %v", err)
	}
	if outcome.Admitted {
		t.Fatalf("expected the thin report refused, got %+v", outcome)
	}
	if outcome.Reason != "thin-report" {
		t.Fatalf("expected a thin-report refusal, got %q (%+v)", outcome.Reason, outcome)
	}
	if !strings.Contains(outcome.Message, "no diagnostic content") {
		t.Fatalf("the refusal must name the missing evidence, got %q", outcome.Message)
	}
	if outcome.ExistingID != "" {
		t.Fatalf("a hard refusal must not name an existing draft, got %+v", outcome)
	}
	assertNoSessions(t, harness.app, "thin report")
}

func TestReportBugFailureWithDiagnosticContentSpawnsADraftAgent(t *testing.T) {
	harness := newInvestigationHarness(t)
	harness.app.deps.startTerminal = func(terminal.StartParams) (terminal.PTY, error) {
		session := newStubTerminalSession()
		session.pid = os.Getpid()
		return session, nil
	}
	outcome, err := harness.app.ReportBugFailure(investigateHelmTimeoutReport, "frs", "dev", 80, 24)
	if err != nil {
		t.Fatalf("a report with a command, an error, and captured output must be admitted: %v", err)
	}
	if !outcome.Admitted || !outcome.Orchestrator.Transient {
		t.Fatalf("expected an admitted, transient draft agent, got %+v", outcome)
	}
	job := soleEnvironmentJob(t, "frs", "dev")
	if !strings.Contains(job.Name, "Report bug") {
		t.Fatalf("the job must name what it is, got %q", job.Name)
	}
}

func TestReportBugFailureRefusalNamesTheExistingDraftToFocus(t *testing.T) {
	harness := newInvestigationHarness(t)
	report := investigateReport("frs/dev", "UPGRADE FAILED: timed out")
	first, err := harness.app.ReportBugFailure(report, "frs", "dev", 80, 24)
	if err != nil || !first.Admitted {
		t.Fatalf("first report must be admitted: %v, %+v", err, first)
	}
	harness.advance(investigate.EventWindow + time.Minute)
	second, err := harness.app.ReportBugFailure(report, "frs", "dev", 80, 24)
	if err != nil {
		t.Fatalf("a refusal must be reported as a value, not a Go error: %v", err)
	}
	if second.Admitted {
		t.Fatalf("expected the repeat refused, got %+v", second)
	}
	if second.ExistingID != first.Orchestrator.ID {
		t.Fatalf("expected the refusal to name the first draft %q, got %+v", first.Orchestrator.ID, second)
	}
	if listed := harness.app.ListOrchestrators(); len(listed) != 1 {
		t.Fatalf("expected the population to stay at one, got %+v", listed)
	}
}

// Investigate and report-bug share one population, not a cap each.
func TestReportBugAndInvestigateShareTheSameBoundedPopulation(t *testing.T) {
	harness := newInvestigationHarness(t)
	report := investigateReport("frs/dev", "UPGRADE FAILED: timed out")
	investigation, err := harness.app.InvestigateFailure(report, "frs", "dev", 80, 24)
	if err != nil {
		t.Fatalf("investigation must be admitted: %v", err)
	}
	harness.advance(investigate.EventWindow + time.Minute)
	outcome, err := harness.app.ReportBugFailure(report, "frs", "dev", 80, 24)
	if err != nil {
		t.Fatalf("a refusal must be reported as a value, not a Go error: %v", err)
	}
	if outcome.Admitted {
		t.Fatalf("expected the report-bug draft refused in favor of the running investigation, got %+v", outcome)
	}
	if outcome.ExistingID != investigation.ID {
		t.Fatalf("expected the refusal to name the running investigation %q, got %+v", investigation.ID, outcome)
	}
	if listed := harness.app.ListOrchestrators(); len(listed) != 1 {
		t.Fatalf("investigate and report-bug must share one population, got %+v", listed)
	}
}

func TestReportBugFailureWithEmptyReportErrors(t *testing.T) {
	harness := newInvestigationHarness(t)
	if _, err := harness.app.ReportBugFailure("   ", "frs", "dev", 80, 24); err == nil {
		t.Fatal("expected an error for an empty report")
	}
	assertNoSessions(t, harness.app, "empty report")
}
