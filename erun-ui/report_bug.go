package main

import (
	"fmt"
	"strings"
)

// reportBugOutcome is ReportBugFailure's result. Exactly one of two shapes
// holds: Admitted names the orchestrator spawned to draft the report, or a
// refusal is described by Reason/Message — with ExistingID set only when the
// refusal points at an already-admitted draft/investigation for the same
// failure, so the caller can focus that session instead of treating the
// refusal as a dead end.
type reportBugOutcome struct {
	Orchestrator orchestratorInfo `json:"orchestrator"`
	Admitted     bool             `json:"admitted"`
	Reason       string           `json:"reason,omitempty"`
	Message      string           `json:"message,omitempty"`
	ExistingID   string           `json:"existingId,omitempty"`
}

// ReportBugFailure hands a failure report to a transient orchestrator that
// drafts a GitHub issue for it rather than opening the browser form the
// operator would otherwise have to fill in by hand (root AGENTS.md "Smooth,
// Seamless, No Dead Ends"). The desktop holds no GitHub token of its own (see
// DebugPanel.Report.tsx's ReportIssueButton), so filing still needs an
// authenticated agent: the spawned session searches sophium/erun's open
// issues for a duplicate, drafts a title/body (or a comment on the match) in
// its own terminal, and is instructed to wait for the operator's explicit
// go-ahead there before it runs `gh issue create`/`gh issue comment` — the
// same tool-confirmation flow every interactive agent session already uses,
// reused here as the human-in-the-loop gate rather than a bespoke review UI.
//
// Spawning reuses InvestigateFailure's exact bounded-spawn plumbing
// (spawnFailureAgent) and, with it, investigation_bounds.go's population: a
// report-bug draft and an investigation share one cap, one per-failure
// dedupe, and one cooldown, because both spend the same shared agent account
// (root AGENTS.md "Spawning an AI agent is a resource decision, not a UI
// action" — this is the reuse it asks for, not a second policy). A refusal
// that names an already-admitted session for the same failure is reported as
// ExistingID rather than as a Go error, so a repeat click focuses that draft
// instead of erroring; any other refusal (or an empty report) is reported as
// a plain (Reason, Message) pair so the caller falls back to the existing
// prefilled-URL report, naming why.
func (a *App) ReportBugFailure(report, tenant, environment string, cols, rows int) (reportBugOutcome, error) {
	if strings.TrimSpace(report) == "" {
		return reportBugOutcome{}, fmt.Errorf("nothing to report: the failure report is empty")
	}
	name := "Report bug"
	if env := strings.TrimSpace(environment); env != "" {
		name = "Report bug: " + env
	}
	info, err := a.spawnFailureAgent("report-bug", report, tenant, environment, cols, rows, name, reportBugPrompt)
	if err == nil {
		return reportBugOutcome{Orchestrator: info, Admitted: true}, nil
	}
	reason, message, existingID := investigationRefusalDetails(err)
	return reportBugOutcome{Reason: reason, Message: message, ExistingID: existingID}, nil
}

// reportBugPrompt is the seed prompt for a report-bug draft session. It names
// the erun-file-issue skill (already available to a spawned orchestrator —
// InvestigateFailure's own prompt relies on the same skill) rather than
// inventing a second body format, requires a duplicate search first so the
// tracker does not accumulate repeats of one failure, and makes the
// confirm-before-filing requirement explicit rather than leaving it to the
// agent's own judgment.
func reportBugPrompt(reportPath string) string {
	return fmt.Sprintf(`A failure report is saved at %s.

Draft a GitHub issue for it in sophium/erun:

1. Search open issues first (for example "gh issue list --repo sophium/erun --search <keywords>") for one describing the same failure. If you find a clear match, propose commenting on that issue instead of filing a duplicate.
2. Otherwise draft a new issue using the erun-file-issue skill's body format (What happened / What you expected / Reproduction / Environment), carrying the full captured output from the report rather than a trimmed summary.
3. Show the operator the exact title and body (or the issue number and comment) you are about to submit here, and wait for their explicit go-ahead before running "gh issue create" or "gh issue comment". Do not file anything until they confirm.`, reportPath)
}
