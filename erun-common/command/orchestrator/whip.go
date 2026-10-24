package orchestrator

import (
	"time"

	"github.com/sophium/erun/erun-common/base/config"
)

// Whip is the transport-agnostic decide/report core of the pacing nudge, shared by
// orchestrators and environment agents; the desktop and in-pod pushers act on it.

type WhipTargetKind string

const (
	WhipTargetOrchestrator WhipTargetKind = "orchestrator"
	WhipTargetEnvironment  WhipTargetKind = "environment"
)

// WhipCandidate is one target, observed by the caller so DecideWhip stays pure.
type WhipCandidate struct {
	Kind WhipTargetKind
	// ID is the orchestrator id or "tenant/environment".
	ID   string
	Name string
	// Reachable is false when this transport has no channel to the candidate at all — a fact
	// about the transport, not the session.
	Reachable bool
	Alive     bool
	// LastActiveAt is omitzero so a never-recorded activity must not serialize as Go's zero instant.
	LastActiveAt time.Time `json:"LastActiveAt,omitzero"`
	// NudgeCount and Capped are the caller-persisted consecutive-nudge bookkeeping.
	NudgeCount int
	Capped     bool
}

type WhipDecision int

const (
	WhipDecisionNone WhipDecision = iota
	WhipDecisionNudge
	WhipDecisionCap
)

// WhipReason distinguishes candidates that share a decision, e.g. quiet vs suppressed.
type WhipReason string

const (
	WhipReasonNotAlive      WhipReason = "not-alive"
	WhipReasonUnreachable   WhipReason = "unreachable-from-transport"
	WhipReasonFresh         WhipReason = "fresh"
	WhipReasonAlreadyCapped WhipReason = "already-capped"
	WhipReasonCapCrossed    WhipReason = "cap-crossed"
	WhipReasonNudge         WhipReason = "nudge"
	// WhipReasonCallFailed is an attempted call that failed; a dead session is WhipReasonNotAlive instead.
	WhipReasonCallFailed WhipReason = "call-failed"
	// WhipReasonChannelDown names a target whose MCP edge is not answering, reported as a skip
	// with a remedy.
	WhipReasonChannelDown WhipReason = "channel-down"
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
func DecideWhip(c WhipCandidate, now time.Time, cfg WhipConfig, explicit bool) (WhipDecision, WhipReason) {
	if !c.Reachable {
		return WhipDecisionNone, WhipReasonUnreachable
	}
	if !c.Alive {
		return WhipDecisionNone, WhipReasonNotAlive
	}
	if !explicit && now.Sub(c.LastActiveAt) < cfg.StaleAfter {
		return WhipDecisionNone, WhipReasonFresh
	}
	if c.Capped {
		return WhipDecisionNone, WhipReasonAlreadyCapped
	}
	if c.NudgeCount >= cfg.MaxNudges {
		return WhipDecisionCap, WhipReasonCapCrossed
	}
	return WhipDecisionNudge, WhipReasonNudge
}

// WhipResult is one candidate's outcome; Pushed is false on dry-run or a push failure.
type WhipResult struct {
	Candidate WhipCandidate `json:"candidate"`
	Decision  WhipDecision  `json:"decision"`
	Reason    WhipReason    `json:"reason"`
	Pushed    bool          `json:"pushed"`
	Error     string        `json:"error,omitempty"`
}

// WhipReport names every target and every skip with its reason.
type WhipReport struct {
	DryRun  bool         `json:"dryRun"`
	Results []WhipResult `json:"results"`
}

// ListWhipOrchestratorCandidates reports every orchestrator as unreachable: CLI and MCP
// cannot reach the desktop-held PTYs.
func ListWhipOrchestratorCandidates(orchestrators []config.OrchestratorConfig) []WhipCandidate {
	candidates := make([]WhipCandidate, 0, len(orchestrators))
	for _, o := range orchestrators {
		candidates = append(candidates, WhipCandidate{
			Kind:      WhipTargetOrchestrator,
			ID:        o.ID,
			Name:      o.Name,
			Reachable: false,
		})
	}
	return candidates
}
