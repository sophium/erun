package orchestrator

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
)

// Selection and result rows share the Kind/ID identity.

const (
	whipTargetKindEnvironment  = "environment"
	whipTargetKindOrchestrator = "orchestrator"
)

// WhipTargetRef is one explicitly selected candidate; WhipNow refuses an empty list.
type WhipTargetRef struct {
	Kind string `json:"kind"` // "environment" | "orchestrator"
	ID   string `json:"id"`
}

type WhipEnvironmentTarget struct {
	ID          string `json:"id"`
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
}

type WhipOrchestratorTarget struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type WhipTargetList struct {
	Environments  []WhipEnvironmentTarget  `json:"environments"`
	Orchestrators []WhipOrchestratorTarget `json:"orchestrators"`
}

// WhipTargets lists the current whip candidates; it is a side-effect-free read.
func (s *Service) WhipTargets() (WhipTargetList, error) {
	envTargets, err := s.listWhipEnvironmentTargets()
	if err != nil {
		return WhipTargetList{}, err
	}
	environments := make([]WhipEnvironmentTarget, 0, len(envTargets))
	for _, target := range envTargets {
		environments = append(environments, WhipEnvironmentTarget{
			ID:          target.tenant + "/" + target.environment,
			Tenant:      target.tenant,
			Environment: target.environment,
		})
	}
	orchestratorTargets := s.listWhipOrchestratorTargets()
	orchestrators := make([]WhipOrchestratorTarget, 0, len(orchestratorTargets))
	for _, target := range orchestratorTargets {
		orchestrators = append(orchestrators, WhipOrchestratorTarget{ID: target.id, Name: target.name})
	}
	return WhipTargetList{Environments: environments, Orchestrators: orchestrators}, nil
}

type WhipResult struct {
	Kind    string `json:"kind"` // "orchestrator" | "environment"
	ID      string `json:"id"`
	Name    string `json:"name"`
	Outcome string `json:"outcome"` // "pushed" | "capped" | "skipped" | "failed"
	Reason  string `json:"reason,omitempty"`
	Error   string `json:"error,omitempty"`
}

type WhipReport struct {
	Results []WhipResult `json:"results"`
}

func whipReportToUI(results []model.WhipResult) WhipReport {
	out := make([]WhipResult, 0, len(results))
	for _, result := range results {
		out = append(out, whipResultToUI(result))
	}
	return WhipReport{Results: out}
}

func whipResultToUI(result model.WhipResult) WhipResult {
	kind := whipTargetKindEnvironment
	if result.Candidate.Kind == model.WhipTargetOrchestrator {
		kind = whipTargetKindOrchestrator
	}
	name := strings.TrimSpace(result.Candidate.Name)
	if name == "" {
		name = result.Candidate.ID
	}
	ui := WhipResult{Kind: kind, ID: result.Candidate.ID, Name: name}
	switch result.Decision {
	case model.WhipDecisionNudge:
		if result.Pushed {
			ui.Outcome = "pushed"
			return ui
		}
		// A refused push is a failure, not a benign skip.
		ui.Outcome = "failed"
		ui.Reason = "push failed"
		ui.Error = result.Error
		return ui
	case model.WhipDecisionCap:
		ui.Outcome = "capped"
		ui.Reason = "stopped nudging after repeated silence — reply in its pane or restart it"
		return ui
	case model.WhipDecisionNone:
		fallthrough
	default:
		if result.Reason == model.WhipReasonCallFailed {
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
func whipSkipReasonText(reason model.WhipReason) string {
	switch reason {
	case model.WhipReasonNotAlive:
		return "not alive — no live session to push"
	case model.WhipReasonUnreachable:
		return "unreachable from this transport"
	case model.WhipReasonAlreadyCapped:
		return "already capped — reply in its pane or restart it"
	case model.WhipReasonFresh:
		return "moved recently"
	case model.WhipReasonCapCrossed, model.WhipReasonNudge, model.WhipReasonCallFailed, model.WhipReasonChannelDown:
		fallthrough
	default:
		return string(reason)
	}
}
