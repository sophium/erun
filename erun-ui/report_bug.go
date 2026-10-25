package main

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-ui/internal/investigate"
	orchestratorsvc "github.com/sophium/erun/erun-ui/internal/orchestrator"
)

// reportBugOutcome is ReportBugFailure's result: either Admitted, or a refusal
// with ExistingID set when an earlier session for the same failure exists.
type reportBugOutcome struct {
	Orchestrator orchestratorsvc.Info `json:"orchestrator"`
	Admitted     bool                 `json:"admitted"`
	Reason       string               `json:"reason,omitempty"`
	Message      string               `json:"message,omitempty"`
	ExistingID   string               `json:"existingId,omitempty"`
}

// ReportBugFailure spawns an orchestrator that drafts a GitHub issue and waits
// for the operator's go-ahead before filing; the desktop holds no GitHub token.
// It shares investigations' cap, dedupe, and cooldown via spawnFailureAgent.
func (a *App) ReportBugFailure(report, tenant, environment string, cols, rows int) (reportBugOutcome, error) {
	if strings.TrimSpace(report) == "" {
		return reportBugOutcome{}, fmt.Errorf("nothing to report: the failure report is empty")
	}
	name := "Report bug"
	if env := strings.TrimSpace(environment); env != "" {
		name = "Report bug: " + env
	}
	info, err := a.spawnFailureAgent(investigate.Request{
		IDPrefix:    "report-bug",
		Report:      report,
		Tenant:      tenant,
		Environment: environment,
		Name:        name,
		Cols:        cols,
		Rows:        rows,
		Prompt:      reportBugPrompt,
	})
	if err == nil {
		return reportBugOutcome{Orchestrator: info, Admitted: true}, nil
	}
	reason, message, existingID := investigate.RefusalDetails(err)
	return reportBugOutcome{Reason: reason, Message: message, ExistingID: existingID}, nil
}

// reportBugPrompt is the seed prompt for a report-bug draft session.
func reportBugPrompt(reportPath string) string {
	return fmt.Sprintf(`A failure report is saved at %s.

Draft a GitHub issue for it in sophium/erun:

1. Search open issues first (for example "gh issue list --repo sophium/erun --search <keywords>") for one describing the same failure. If you find a clear match, propose commenting on that issue instead of filing a duplicate.
2. Otherwise draft a new issue using the erun-file-issue skill's body format (What happened / What you expected / Reproduction / Environment), carrying the full captured output from the report rather than a trimmed summary.
3. Show the operator the exact title and body (or the issue number and comment) you are about to submit here, and wait for their explicit go-ahead before running "gh issue create" or "gh issue comment". Do not file anything until they confirm.`, reportPath)
}
