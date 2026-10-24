package orchestrator

import (
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

// DefaultWhipMessage restates the pacing contract, plus a clause letting a finished session ignore it.
const DefaultWhipMessage = "Keep pacing yourself, on connection errors wait and resume, do not exit this loop. " +
	"If the assigned task is already complete and verified, say so in one line and stop."

// DefaultWhipStaleAfter is how long a target may go unrenewed before an automatic pass nudges it.
const DefaultWhipStaleAfter = 10 * time.Minute

// DefaultWhipMaxNudges bounds consecutive unanswered nudges before a target is capped.
const DefaultWhipMaxNudges = 6

type WhipConfig struct {
	Message    string
	StaleAfter time.Duration
	MaxNudges  int
	// AutoEnabled gates only the scheduled pass; an explicit whip ignores it.
	AutoEnabled bool
}

// ResolveWhipConfig fills every unset field with its default.
func ResolveWhipConfig(override *config.WhipConfigOverride) WhipConfig {
	cfg := WhipConfig{
		Message:     DefaultWhipMessage,
		StaleAfter:  DefaultWhipStaleAfter,
		MaxNudges:   DefaultWhipMaxNudges,
		AutoEnabled: true,
	}
	if override == nil {
		return cfg
	}
	if override.Message != nil {
		cfg.Message = *override.Message
	}
	if override.StaleAfterSeconds != nil {
		cfg.StaleAfter = time.Duration(*override.StaleAfterSeconds) * time.Second
	}
	if override.MaxNudges != nil {
		cfg.MaxNudges = *override.MaxNudges
	}
	if override.AutoEnabled != nil {
		cfg.AutoEnabled = *override.AutoEnabled
	}
	return cfg
}

// DecideWhip nudges a reachable, alive, stale, uncapped target, and sends the cap notice once
// when it crosses the cap. explicit bypasses only staleness, never the cap.
func DecideWhip(c model.WhipCandidate, now time.Time, cfg WhipConfig, explicit bool) (model.WhipDecision, model.WhipReason) {
	if !c.Reachable {
		return model.WhipDecisionNone, model.WhipReasonUnreachable
	}
	if !c.Alive {
		return model.WhipDecisionNone, model.WhipReasonNotAlive
	}
	if !explicit && now.Sub(c.LastActiveAt) < cfg.StaleAfter {
		return model.WhipDecisionNone, model.WhipReasonFresh
	}
	if c.Capped {
		return model.WhipDecisionNone, model.WhipReasonAlreadyCapped
	}
	if c.NudgeCount >= cfg.MaxNudges {
		return model.WhipDecisionCap, model.WhipReasonCapCrossed
	}
	return model.WhipDecisionNudge, model.WhipReasonNudge
}

// ListWhipOrchestratorCandidates reports every orchestrator as unreachable: CLI and MCP
// cannot reach the desktop-held PTYs.
func ListWhipOrchestratorCandidates(orchestrators []config.OrchestratorConfig) []model.WhipCandidate {
	candidates := make([]model.WhipCandidate, 0, len(orchestrators))
	for _, o := range orchestrators {
		candidates = append(candidates, model.WhipCandidate{
			Kind:      model.WhipTargetOrchestrator,
			ID:        o.ID,
			Name:      o.Name,
			Reachable: false,
		})
	}
	return candidates
}
