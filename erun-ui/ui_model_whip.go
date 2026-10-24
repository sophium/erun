package main

import (
	"strings"

	"github.com/sophium/erun/erun-common/command/orchestrator"
)

// Selection and result rows share the Kind/ID identity.

const (
	uiWhipTargetKindEnvironment  = "environment"
	uiWhipTargetKindOrchestrator = "orchestrator"
)

// uiWhipTargetRef is one explicitly selected candidate; WhipNow refuses an empty list.
type uiWhipTargetRef struct {
	Kind string `json:"kind"` // "environment" | "orchestrator"
	ID   string `json:"id"`
}

type uiWhipEnvironmentTarget struct {
	ID          string `json:"id"`
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
}

type uiWhipOrchestratorTarget struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type uiWhipTargetList struct {
	Environments  []uiWhipEnvironmentTarget  `json:"environments"`
	Orchestrators []uiWhipOrchestratorTarget `json:"orchestrators"`
}

// WhipTargets lists the current whip candidates; it is a side-effect-free read.
func (a *App) WhipTargets() (uiWhipTargetList, error) {
	envTargets, err := a.listWhipEnvironmentTargets()
	if err != nil {
		return uiWhipTargetList{}, err
	}
	environments := make([]uiWhipEnvironmentTarget, 0, len(envTargets))
	for _, target := range envTargets {
		environments = append(environments, uiWhipEnvironmentTarget{
			ID:          target.tenant + "/" + target.environment,
			Tenant:      target.tenant,
			Environment: target.environment,
		})
	}
	orchestratorTargets := a.listWhipOrchestratorTargets()
	orchestrators := make([]uiWhipOrchestratorTarget, 0, len(orchestratorTargets))
	for _, target := range orchestratorTargets {
		orchestrators = append(orchestrators, uiWhipOrchestratorTarget{ID: target.id, Name: target.name})
	}
	return uiWhipTargetList{Environments: environments, Orchestrators: orchestrators}, nil
}

type uiWhipResult struct {
	Kind    string `json:"kind"` // "orchestrator" | "environment"
	ID      string `json:"id"`
	Name    string `json:"name"`
	Outcome string `json:"outcome"` // "pushed" | "capped" | "skipped" | "failed"
	Reason  string `json:"reason,omitempty"`
	Error   string `json:"error,omitempty"`
}

type uiWhipReport struct {
	Results []uiWhipResult `json:"results"`
}

func whipReportToUI(results []orchestrator.WhipResult) uiWhipReport {
	out := make([]uiWhipResult, 0, len(results))
	for _, result := range results {
		out = append(out, whipResultToUI(result))
	}
	return uiWhipReport{Results: out}
}

func whipResultToUI(result orchestrator.WhipResult) uiWhipResult {
	kind := uiWhipTargetKindEnvironment
	if result.Candidate.Kind == orchestrator.WhipTargetOrchestrator {
		kind = uiWhipTargetKindOrchestrator
	}
	name := strings.TrimSpace(result.Candidate.Name)
	if name == "" {
		name = result.Candidate.ID
	}
	ui := uiWhipResult{Kind: kind, ID: result.Candidate.ID, Name: name}
	switch result.Decision {
	case orchestrator.WhipDecisionNudge:
		if result.Pushed {
			ui.Outcome = "pushed"
			return ui
		}
		// A refused push is a failure, not a benign skip.
		ui.Outcome = "failed"
		ui.Reason = "push failed"
		ui.Error = result.Error
		return ui
	case orchestrator.WhipDecisionCap:
		ui.Outcome = "capped"
		ui.Reason = "stopped nudging after repeated silence — reply in its pane or restart it"
		return ui
	case orchestrator.WhipDecisionNone:
		fallthrough
	default:
		if result.Reason == orchestrator.WhipReasonCallFailed {
			ui.Outcome = "failed"
			ui.Reason = "call failed"
			ui.Error = result.Error
			return ui
		}
		ui.Outcome = "skipped"
		ui.Reason = whipSkipReasonText(result.Reason)
		ui.Error = result.Error
		return ui
	}
}

// whipSkipReasonText matches erun-cli's whipResultValue wording.
func whipSkipReasonText(reason orchestrator.WhipReason) string {
	switch reason {
	case orchestrator.WhipReasonNotAlive:
		return "not alive — no live session to push"
	case orchestrator.WhipReasonUnreachable:
		return "unreachable from this transport"
	case orchestrator.WhipReasonAlreadyCapped:
		return "already capped — reply in its pane or restart it"
	case orchestrator.WhipReasonFresh:
		return "moved recently"
	case orchestrator.WhipReasonCapCrossed, orchestrator.WhipReasonNudge, orchestrator.WhipReasonCallFailed, orchestrator.WhipReasonChannelDown:
		fallthrough
	default:
		return string(reason)
	}
}
