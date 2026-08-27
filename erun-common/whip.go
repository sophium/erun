package eruncommon

import "time"

// The pacing nudge (orchestrator_pacing.go, erun-ui) already re-states the
// pacing contract into a stalled orchestrator's own pane, automatically, on
// the desktop's existing 15s tick. This file generalizes the pure decide/report
// core so the same contract also covers an environment's own agent session and
// so an operator can trigger a pass by hand instead of waiting for the tick —
// "one pass, two populations". It stays transport-agnostic: no
// pty, no dtach, no MCP SDK. The erun-ui reconciler and the in-pod push in
// whip_environment.go are the two concrete pushers that act on what this
// decides.

// WhipTargetKind names which population a candidate belongs to.
type WhipTargetKind string

const (
	WhipTargetOrchestrator WhipTargetKind = "orchestrator"
	WhipTargetEnvironment  WhipTargetKind = "environment"
)

// WhipCandidate is one target as a whip pass decides for it, gathered so the
// decision itself stays a pure function independent of how alive/lastActiveAt
// were observed (a desktop in-memory PTY map for orchestrators, an in-pod
// dtach probe for environments).
type WhipCandidate struct {
	Kind WhipTargetKind
	// ID and Name identify the target for the report: ID is the orchestrator id
	// or "tenant/environment"; Name is what a human-facing report should print.
	ID   string
	Name string
	// Reachable is false when this transport has no channel to push this
	// candidate at all — a CLI/MCP process has no way into a desktop-held
	// orchestrator PTY, whatever that orchestrator's own liveness is. This is a
	// fact about the transport, never about the session, so it gets its own
	// reason rather than collapsing into "not alive".
	Reachable bool
	// Alive reports whether this transport can currently see a live session
	// behind the target (a PTY session for an orchestrator, a dtach master for
	// an environment's AI session).
	Alive        bool
	LastActiveAt time.Time
	// NudgeCount and Capped carry the target's own consecutive-nudge bookkeeping
	// forward from whatever the caller persists it in (in-memory for the
	// desktop's orchestrator sessions, a small on-disk record for an
	// environment's own agent).
	NudgeCount int
	Capped     bool
}

// WhipDecision is what a whip pass does about one candidate.
type WhipDecision int

const (
	WhipDecisionNone WhipDecision = iota
	WhipDecisionNudge
	WhipDecisionCap
)

// WhipReason names why DecideWhip returned what it did, independent of the
// decision: two candidates can both resolve to WhipDecisionNone for different
// reasons, and a report that only carried the decision could not tell a quiet
// target from a suppressed one.
type WhipReason string

const (
	WhipReasonNotAlive      WhipReason = "not-alive"
	WhipReasonUnreachable   WhipReason = "unreachable-from-transport"
	WhipReasonFresh         WhipReason = "fresh"
	WhipReasonAlreadyCapped WhipReason = "already-capped"
	WhipReasonCapCrossed    WhipReason = "cap-crossed"
	WhipReasonNudge         WhipReason = "nudge"
)

// DefaultWhipMessage restates the pacing contract verbatim, plus the one
// clause that makes it a no-op for a session that is genuinely finished: erun
// cannot know completion, but the session can, and asking is cheaper than
// guessing or nudging forever. This is the text an unconfigured install keeps
// getting; ResolveWhipConfig only overrides it when a caller explicitly set one.
const DefaultWhipMessage = "Keep pacing yourself, on connection errors wait and resume, do not exit this loop. " +
	"If the assigned task is already complete and verified, say so in one line and stop."

// DefaultWhipStaleAfter is how long a target may go unrenewed before an
// automatic pass reads it as quiet. Matches orchestrator_pacing.go's existing
// constant so behaviour is unchanged for an install that configures nothing.
const DefaultWhipStaleAfter = 10 * time.Minute

// DefaultWhipMaxNudges bounds consecutive un-answered nudges before a target
// is capped instead of nudged again. Matches orchestrator_pacing.go's existing
// constant.
const DefaultWhipMaxNudges = 6

// WhipConfig is the resolved, always-fully-populated pacing configuration a
// whip pass decides against.
type WhipConfig struct {
	Message    string
	StaleAfter time.Duration
	MaxNudges  int
	// AutoEnabled gates the automatic, schedule-driven pass (the existing
	// desktop tick for orchestrators). An explicit, operator-triggered whip
	// ignores this — it is the operator overriding the schedule, not asking for
	// one.
	AutoEnabled bool
}

// WhipConfigOverride is what a caller may persist in ~/.erun/config.yaml to
// change WhipConfig without a rebuild. Pointer fields so an unset field is
// distinguishable from an explicit zero (a StaleAfterSeconds of 0 or a
// disabled AutoEnabled are meaningful values, not "use the default").
type WhipConfigOverride struct {
	Message           *string `yaml:"message,omitempty" json:"message,omitempty"`
	StaleAfterSeconds *int    `yaml:"staleafterseconds,omitempty" json:"staleAfterSeconds,omitempty"`
	MaxNudges         *int    `yaml:"maxnudges,omitempty" json:"maxNudges,omitempty"`
	AutoEnabled       *bool   `yaml:"autoenabled,omitempty" json:"autoEnabled,omitempty"`
}

// ResolveWhipConfig fills every unset field with today's constant, so an
// override left nil entirely (the common case: nobody has configured
// anything) resolves to exactly what orchestrator_pacing.go hard-coded before
// this file existed.
func ResolveWhipConfig(override *WhipConfigOverride) WhipConfig {
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

// DecideWhip is the whole bound, generalized from orchestrator_pacing.go's
// original decideOrchestratorPacing: a target this transport cannot reach at
// all, one that is not alive, or one already past the cap gets no nudge. A
// candidate that is stale (or whose caller is asserting explicitly, ignoring
// staleness) and not yet capped gets nudged; one that just crossed the cap
// gets the one-time cap notice instead of a nudge. explicit never bypasses the
// cap or the capped state — only the freshness gate.
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

// WhipResult is the visible record for one candidate: what was decided, why,
// and whether the nudge text was actually written (false on --dry-run/preview
// or on a push failure, which is carried in Error).
type WhipResult struct {
	Candidate WhipCandidate `json:"candidate"`
	Decision  WhipDecision  `json:"decision"`
	Reason    WhipReason    `json:"reason"`
	Pushed    bool          `json:"pushed"`
	Error     string        `json:"error,omitempty"`
}

// WhipReport is the whole pass's outcome — the deliverable an operator judges
// the feature by: naming every target and every skip with its reason, not just
// asserting the pass ran.
type WhipReport struct {
	DryRun  bool         `json:"dryRun"`
	Results []WhipResult `json:"results"`
}

// ListWhipOrchestratorCandidates turns the persisted orchestrator definitions
// into whip candidates for a transport that cannot reach any of them: CLI and
// MCP run as separate processes from the desktop that holds an orchestrator's
// live PTY, so every one of them is reported, truthfully, as unreachable from
// here rather than silently omitted.
func ListWhipOrchestratorCandidates(orchestrators []OrchestratorConfig) []WhipCandidate {
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
