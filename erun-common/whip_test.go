package eruncommon

import (
	"testing"
	"time"
)

func TestDecideWhipStaleUncappedNudges(t *testing.T) {
	now := time.Now()
	cfg := ResolveWhipConfig(nil)
	c := WhipCandidate{
		Kind:         WhipTargetOrchestrator,
		ID:           "eng-1",
		Reachable:    true,
		Alive:        true,
		LastActiveAt: now.Add(-cfg.StaleAfter - time.Minute),
	}
	decision, reason := DecideWhip(c, now, cfg, false)
	if decision != WhipDecisionNudge || reason != WhipReasonNudge {
		t.Fatalf("got decision=%v reason=%v, want nudge/nudge", decision, reason)
	}
}

func TestDecideWhipAtCapStops(t *testing.T) {
	now := time.Now()
	cfg := ResolveWhipConfig(nil)
	c := WhipCandidate{
		Reachable:    true,
		Alive:        true,
		LastActiveAt: now.Add(-cfg.StaleAfter - time.Minute),
		NudgeCount:   cfg.MaxNudges,
	}
	decision, reason := DecideWhip(c, now, cfg, false)
	if decision != WhipDecisionCap || reason != WhipReasonCapCrossed {
		t.Fatalf("got decision=%v reason=%v, want cap/cap-crossed", decision, reason)
	}
}

func TestDecideWhipFreshDoesNothingWhenNotExplicit(t *testing.T) {
	now := time.Now()
	cfg := ResolveWhipConfig(nil)
	c := WhipCandidate{Reachable: true, Alive: true, LastActiveAt: now}
	decision, reason := DecideWhip(c, now, cfg, false)
	if decision != WhipDecisionNone || reason != WhipReasonFresh {
		t.Fatalf("got decision=%v reason=%v, want none/fresh", decision, reason)
	}
}

// TestDecideWhipExplicitIgnoresFreshnessButNotCap is the manual-trigger
// contract: clicking whip on a session that moved a second ago
// still pushes it, but a capped session stays capped and an already-exhausted
// one crosses to the cap notice rather than a 7th nudge.
func TestDecideWhipExplicitIgnoresFreshnessButNotCap(t *testing.T) {
	now := time.Now()
	cfg := ResolveWhipConfig(nil)

	fresh := WhipCandidate{Reachable: true, Alive: true, LastActiveAt: now}
	if decision, reason := DecideWhip(fresh, now, cfg, true); decision != WhipDecisionNudge || reason != WhipReasonNudge {
		t.Fatalf("explicit fresh: got decision=%v reason=%v, want nudge/nudge", decision, reason)
	}

	capped := WhipCandidate{Reachable: true, Alive: true, LastActiveAt: now, Capped: true}
	if decision, reason := DecideWhip(capped, now, cfg, true); decision != WhipDecisionNone || reason != WhipReasonAlreadyCapped {
		t.Fatalf("explicit capped: got decision=%v reason=%v, want none/already-capped", decision, reason)
	}

	atCap := WhipCandidate{Reachable: true, Alive: true, LastActiveAt: now, NudgeCount: cfg.MaxNudges}
	if decision, reason := DecideWhip(atCap, now, cfg, true); decision != WhipDecisionCap || reason != WhipReasonCapCrossed {
		t.Fatalf("explicit at cap: got decision=%v reason=%v, want cap/cap-crossed", decision, reason)
	}
}

func TestDecideWhipNotAliveNeverNudges(t *testing.T) {
	now := time.Now()
	cfg := ResolveWhipConfig(nil)
	c := WhipCandidate{Reachable: true, Alive: false, LastActiveAt: now.Add(-time.Hour)}
	if decision, reason := DecideWhip(c, now, cfg, true); decision != WhipDecisionNone || reason != WhipReasonNotAlive {
		t.Fatalf("got decision=%v reason=%v, want none/not-alive", decision, reason)
	}
}

// TestDecideWhipUnreachableIsItsOwnReason is what a CLI/MCP-driven whip
// reports for a persisted orchestrator: those transports have no channel into
// a desktop-held PTY, so the skip must name that structural fact rather than
// silently reporting "not alive" (a claim about the session, not the transport).
func TestDecideWhipUnreachableIsItsOwnReason(t *testing.T) {
	now := time.Now()
	cfg := ResolveWhipConfig(nil)
	c := WhipCandidate{Reachable: false, Alive: true, LastActiveAt: now.Add(-time.Hour)}
	if decision, reason := DecideWhip(c, now, cfg, true); decision != WhipDecisionNone || reason != WhipReasonUnreachable {
		t.Fatalf("got decision=%v reason=%v, want none/unreachable-from-transport", decision, reason)
	}
}

func TestResolveWhipConfigDefaultsPreserveExistingBehaviour(t *testing.T) {
	cfg := ResolveWhipConfig(nil)
	if cfg.Message != DefaultWhipMessage {
		t.Fatalf("unconfigured install must keep today's text, got %q", cfg.Message)
	}
	if cfg.StaleAfter != DefaultWhipStaleAfter {
		t.Fatalf("got StaleAfter=%v, want default %v", cfg.StaleAfter, DefaultWhipStaleAfter)
	}
	if cfg.MaxNudges != DefaultWhipMaxNudges {
		t.Fatalf("got MaxNudges=%d, want default %d", cfg.MaxNudges, DefaultWhipMaxNudges)
	}
}

func TestResolveWhipConfigOverrideWins(t *testing.T) {
	message := "Custom whip text specific to this install."
	staleSeconds := 120
	maxNudges := 2
	cfg := ResolveWhipConfig(&WhipConfigOverride{
		Message:           &message,
		StaleAfterSeconds: &staleSeconds,
		MaxNudges:         &maxNudges,
	})
	if cfg.Message != message {
		t.Fatalf("got Message=%q, want override %q", cfg.Message, message)
	}
	if cfg.StaleAfter != 120*time.Second {
		t.Fatalf("got StaleAfter=%v, want 120s", cfg.StaleAfter)
	}
	if cfg.MaxNudges != 2 {
		t.Fatalf("got MaxNudges=%d, want 2", cfg.MaxNudges)
	}
}

func TestListWhipOrchestratorCandidatesAreUnreachable(t *testing.T) {
	candidates := ListWhipOrchestratorCandidates([]OrchestratorConfig{{ID: "eng-1", Name: "Eng One"}})
	if len(candidates) != 1 {
		t.Fatalf("got %d candidates, want 1", len(candidates))
	}
	if candidates[0].Reachable {
		t.Fatalf("an orchestrator candidate listed for a CLI/MCP transport must be marked unreachable")
	}
	if candidates[0].Kind != WhipTargetOrchestrator {
		t.Fatalf("got Kind=%v, want WhipTargetOrchestrator", candidates[0].Kind)
	}
}
