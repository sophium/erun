package model

import (
	"time"
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
