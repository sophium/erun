package orchestrator

import (
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

func TestDecideWhipStaleUncappedNudges(t *testing.T) {
	now := time.Now()
	cfg := ResolveWhipConfig(nil)
	c := model.WhipCandidate{
		Kind:         model.WhipTargetOrchestrator,
		ID:           "eng-1",
		Reachable:    true,
		Alive:        true,
		LastActiveAt: now.Add(-cfg.StaleAfter - time.Minute),
	}
	decision, reason := DecideWhip(c, now, cfg, false)
	if decision != model.WhipDecisionNudge || reason != model.WhipReasonNudge {
		t.Fatalf("got decision=%v reason=%v, want nudge/nudge", decision, reason)
	}
}

func TestDecideWhipAtCapStops(t *testing.T) {
	now := time.Now()
	cfg := ResolveWhipConfig(nil)
	c := model.WhipCandidate{
		Reachable:    true,
		Alive:        true,
		LastActiveAt: now.Add(-cfg.StaleAfter - time.Minute),
		NudgeCount:   cfg.MaxNudges,
	}
	decision, reason := DecideWhip(c, now, cfg, false)
	if decision != model.WhipDecisionCap || reason != model.WhipReasonCapCrossed {
		t.Fatalf("got decision=%v reason=%v, want cap/cap-crossed", decision, reason)
	}
}

func TestDecideWhipFreshDoesNothingWhenNotExplicit(t *testing.T) {
	now := time.Now()
	cfg := ResolveWhipConfig(nil)
	c := model.WhipCandidate{Reachable: true, Alive: true, LastActiveAt: now}
	decision, reason := DecideWhip(c, now, cfg, false)
	if decision != model.WhipDecisionNone || reason != model.WhipReasonFresh {
		t.Fatalf("got decision=%v reason=%v, want none/fresh", decision, reason)
	}
}

func TestDecideWhipExplicitIgnoresFreshnessButNotCap(t *testing.T) {
	now := time.Now()
	cfg := ResolveWhipConfig(nil)

	fresh := model.WhipCandidate{Reachable: true, Alive: true, LastActiveAt: now}
	if decision, reason := DecideWhip(fresh, now, cfg, true); decision != model.WhipDecisionNudge || reason != model.WhipReasonNudge {
		t.Fatalf("explicit fresh: got decision=%v reason=%v, want nudge/nudge", decision, reason)
	}

	capped := model.WhipCandidate{Reachable: true, Alive: true, LastActiveAt: now, Capped: true}
	if decision, reason := DecideWhip(capped, now, cfg, true); decision != model.WhipDecisionNone || reason != model.WhipReasonAlreadyCapped {
		t.Fatalf("explicit capped: got decision=%v reason=%v, want none/already-capped", decision, reason)
	}

	atCap := model.WhipCandidate{Reachable: true, Alive: true, LastActiveAt: now, NudgeCount: cfg.MaxNudges}
	if decision, reason := DecideWhip(atCap, now, cfg, true); decision != model.WhipDecisionCap || reason != model.WhipReasonCapCrossed {
		t.Fatalf("explicit at cap: got decision=%v reason=%v, want cap/cap-crossed", decision, reason)
	}
}

func TestDecideWhipNotAliveNeverNudges(t *testing.T) {
	now := time.Now()
	cfg := ResolveWhipConfig(nil)
	c := model.WhipCandidate{Reachable: true, Alive: false, LastActiveAt: now.Add(-time.Hour)}
	if decision, reason := DecideWhip(c, now, cfg, true); decision != model.WhipDecisionNone || reason != model.WhipReasonNotAlive {
		t.Fatalf("got decision=%v reason=%v, want none/not-alive", decision, reason)
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
	cfg := ResolveWhipConfig(&config.WhipConfigOverride{
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
