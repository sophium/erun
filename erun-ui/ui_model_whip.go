package main

import (
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// The whip surface's models -- one flat list of per-target outcomes, folded
// from both populations' eruncommon.WhipResult/WhipCandidate into the shape
// the titlebar control renders, so a click's report reads the same whether
// the target was an environment or an orchestrator.

type uiWhipResult struct {
	Kind    string `json:"kind"` // "orchestrator" | "environment"
	ID      string `json:"id"`
	Name    string `json:"name"`
	Outcome string `json:"outcome"` // "pushed" | "capped" | "skipped"
	Reason  string `json:"reason,omitempty"`
	Error   string `json:"error,omitempty"`
}

type uiWhipReport struct {
	Results []uiWhipResult `json:"results"`
}

func whipReportToUI(results []eruncommon.WhipResult) uiWhipReport {
	out := make([]uiWhipResult, 0, len(results))
	for _, result := range results {
		out = append(out, whipResultToUI(result))
	}
	return uiWhipReport{Results: out}
}

func whipResultToUI(result eruncommon.WhipResult) uiWhipResult {
	kind := "environment"
	if result.Candidate.Kind == eruncommon.WhipTargetOrchestrator {
		kind = "orchestrator"
	}
	name := strings.TrimSpace(result.Candidate.Name)
	if name == "" {
		name = result.Candidate.ID
	}
	ui := uiWhipResult{Kind: kind, ID: result.Candidate.ID, Name: name}
	switch result.Decision {
	case eruncommon.WhipDecisionNudge:
		if result.Pushed {
			ui.Outcome = "pushed"
			return ui
		}
		ui.Outcome = "skipped"
		ui.Reason = "push failed"
		ui.Error = result.Error
		return ui
	case eruncommon.WhipDecisionCap:
		ui.Outcome = "capped"
		ui.Reason = "stopped nudging after repeated silence — reply in its pane or restart it"
		return ui
	default:
		ui.Outcome = "skipped"
		ui.Reason = whipSkipReasonText(result.Reason)
		ui.Error = result.Error
		return ui
	}
}

// whipSkipReasonText renders WhipReason as the sentence an operator reads,
// mirroring the wording erun-cli's whipResultValue and the pacing-capped
// notice already use for the same conditions.
func whipSkipReasonText(reason eruncommon.WhipReason) string {
	switch reason {
	case eruncommon.WhipReasonNotAlive:
		return "not alive — no live session to push"
	case eruncommon.WhipReasonUnreachable:
		return "unreachable from this transport"
	case eruncommon.WhipReasonAlreadyCapped:
		return "already capped — reply in its pane or restart it"
	case eruncommon.WhipReasonFresh:
		return "moved recently"
	default:
		return string(reason)
	}
}
