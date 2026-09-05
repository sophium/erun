package main

import (
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// The whip surface's models -- one flat list of per-target outcomes, folded
// from both populations' eruncommon.WhipResult/WhipCandidate into the shape
// the titlebar control renders, so a click's report reads the same whether
// the target was an environment or an orchestrator. uiWhipTargetRef and the
// WhipTargets population below are the selection half of the same surface:
// what the operator can check, and what a click actually asks WhipNow to
// push -- an explicit subset, keyed by the same Kind/ID pair a result reports
// under, so a checked row and its eventual report row are the same identity.

const (
	uiWhipTargetKindEnvironment  = "environment"
	uiWhipTargetKindOrchestrator = "orchestrator"
)

// uiWhipTargetRef identifies one candidate the operator explicitly selected.
// WhipNow takes a list of these instead of a boolean "everything" switch, so
// an empty list is refused rather than silently read as "every target" (see
// WhipNow's own doc comment in whip.go).
type uiWhipTargetRef struct {
	Kind string `json:"kind"` // "environment" | "orchestrator"
	ID   string `json:"id"`
}

// uiWhipEnvironmentTarget/uiWhipOrchestratorTarget are one selectable row
// each -- WhipTargets' population. ID matches the id a uiWhipResult reports
// under ("tenant/environment" for an environment, the orchestrator's own id
// otherwise), so a checked row and its eventual report row are the same
// identity.
type uiWhipEnvironmentTarget struct {
	ID          string `json:"id"`
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
}

type uiWhipOrchestratorTarget struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// uiWhipTargetList is WhipTargets' whole return: the current population the
// selection surface renders as checkable rows, refreshed on demand so a
// "select all X" click always acts on who is eligible right now rather than a
// snapshot from when the surface opened.
type uiWhipTargetList struct {
	Environments  []uiWhipEnvironmentTarget  `json:"environments"`
	Orchestrators []uiWhipOrchestratorTarget `json:"orchestrators"`
}

// WhipTargets lists every environment and orchestrator the whip selection
// surface can offer right now. It is a pure read -- no push, no decision --
// so the desktop can refresh it as often as the selection UI needs (on open,
// and again on every "select all" click, and once more immediately before a
// click actually whips) with no side effect.
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

func whipReportToUI(results []eruncommon.WhipResult) uiWhipReport {
	out := make([]uiWhipResult, 0, len(results))
	for _, result := range results {
		out = append(out, whipResultToUI(result))
	}
	return uiWhipReport{Results: out}
}

func whipResultToUI(result eruncommon.WhipResult) uiWhipResult {
	kind := uiWhipTargetKindEnvironment
	if result.Candidate.Kind == eruncommon.WhipTargetOrchestrator {
		kind = uiWhipTargetKindOrchestrator
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
		// A decided nudge that did not push is a write that was attempted and
		// refused -- InlineAlert territory (erun-ui/AGENTS.md's Design-Language
		// Decision Record), not a benign skip. Give it its own outcome so the
		// badge and tone can't collapse it into the same bucket as "not alive".
		ui.Outcome = "failed"
		ui.Reason = "push failed"
		ui.Error = result.Error
		return ui
	case eruncommon.WhipDecisionCap:
		ui.Outcome = "capped"
		ui.Reason = "stopped nudging after repeated silence — reply in its pane or restart it"
		return ui
	default:
		if result.Reason == eruncommon.WhipReasonCallFailed {
			// A call that was actually attempted and failed is a write that was
			// refused, not a benign skip -- same InlineAlert-territory distinction
			// the decided-but-refused-nudge case above already draws, so it must
			// not collapse into whipSkipReasonText's "not alive" wording.
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
