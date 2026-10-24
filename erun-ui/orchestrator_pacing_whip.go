package main

import (
	"sort"
	"sync"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/orchestrator"
)

// orchestratorWhipConfig is the pacing pass's resolved, live-reloadable
// configuration (message/stale-threshold/cap), read from the operator's global
// config once per reconciler tick (refreshOrchestratorWhipConfig) so an edit to
// ~/.erun/config.yaml takes effect on the next tick without a rebuild or
// restart. Guarded by a mutex because the manual whip-now entrypoints
// (whipOrchestratorsNow) can read it from a different goroutine than the
// reconciler tick.
var (
	orchestratorWhipConfigMu sync.RWMutex
	orchestratorWhipConfig   = orchestrator.ResolveWhipConfig(nil)
)

func setOrchestratorWhipConfig(cfg orchestrator.WhipConfig) {
	orchestratorWhipConfigMu.Lock()
	orchestratorWhipConfig = cfg
	orchestratorWhipConfigMu.Unlock()
}

func getOrchestratorWhipConfig() orchestrator.WhipConfig {
	orchestratorWhipConfigMu.RLock()
	defer orchestratorWhipConfigMu.RUnlock()
	return orchestratorWhipConfig
}

// refreshOrchestratorWhipConfig re-reads ~/.erun/config.yaml's whip override
// and resolves it against the defaults. Best-effort: a missing or unreadable
// root config resolves to the defaults, as orchestrator.ResolveWhipConfig does
// for every transport.
func refreshOrchestratorWhipConfig() {
	config, _, _ := configpkg.LoadERunConfig()
	setOrchestratorWhipConfig(orchestrator.ResolveWhipConfig(config.Whip))
}

// decideOrchestratorWhip is decideOrchestratorPacing's explicit-aware form: a
// manual, operator-triggered whip (explicit=true) ignores staleness but never
// bypasses the cap or an already-capped session, as DecideWhip requires.
func decideOrchestratorWhip(c orchestratorPacingCandidate, now time.Time, explicit bool) (orchestratorPacingDecision, orchestratorPacingReason) {
	candidate := orchestrator.WhipCandidate{
		Kind: orchestrator.WhipTargetOrchestrator,
		// The desktop holds this orchestrator's own PTY, so it can write a
		// nudge into it — except for a configured orchestrator it holds no
		// session for, which nothing here can reach (see
		// orchestratorPacingUnmanagedRows).
		Reachable:    !c.unmanaged,
		Alive:        c.alive,
		LastActiveAt: c.lastActiveAt,
		NudgeCount:   c.nudgeCount,
		Capped:       c.capped,
	}
	decision, reason := orchestrator.DecideWhip(candidate, now, getOrchestratorWhipConfig(), explicit)
	return orchestratorPacingDecisionFromWhip(decision), orchestratorPacingReasonFromWhip(reason)
}

func orchestratorPacingDecisionFromWhip(decision orchestrator.WhipDecision) orchestratorPacingDecision {
	switch decision {
	case orchestrator.WhipDecisionNudge:
		return orchestratorPacingNudge
	case orchestrator.WhipDecisionCap:
		return orchestratorPacingCap
	case orchestrator.WhipDecisionNone:
		fallthrough
	default:
		return orchestratorPacingNone
	}
}

// orchestratorPacingReasonFromWhip translates every reason DecideWhip can
// return. WhipReasonUnreachable reaches here only for a configured
// orchestrator whose session this desktop does not hold (see
// orchestratorPacingUnmanagedRows) — every other orchestrator row is one the
// desktop holds the PTY of, which is what makes those candidates reachable.
func orchestratorPacingReasonFromWhip(reason orchestrator.WhipReason) orchestratorPacingReason {
	switch reason {
	case orchestrator.WhipReasonNotAlive:
		return orchestratorPacingReasonNotAlive
	case orchestrator.WhipReasonUnreachable:
		return orchestratorPacingReasonUnreachable
	case orchestrator.WhipReasonAlreadyCapped:
		return orchestratorPacingReasonAlreadyCapped
	case orchestrator.WhipReasonCapCrossed:
		return orchestratorPacingReasonCapCrossed
	case orchestrator.WhipReasonNudge:
		return orchestratorPacingReasonNudge
	case orchestrator.WhipReasonFresh, orchestrator.WhipReasonCallFailed, orchestrator.WhipReasonChannelDown:
		fallthrough
	default:
		return orchestratorPacingReasonFresh
	}
}

// whipDecisionFromOrchestratorPacing is orchestratorPacingDecisionFromWhip's
// inverse, used by WhipNow (whip.go) to fold an orchestrator outcome into the
// same orchestrator.WhipResult shape the environment side reports in.
func whipDecisionFromOrchestratorPacing(decision orchestratorPacingDecision) orchestrator.WhipDecision {
	switch decision {
	case orchestratorPacingNudge:
		return orchestrator.WhipDecisionNudge
	case orchestratorPacingCap:
		return orchestrator.WhipDecisionCap
	case orchestratorPacingNone:
		fallthrough
	default:
		return orchestrator.WhipDecisionNone
	}
}

// whipReasonFromOrchestratorPacing is orchestratorPacingReasonFromWhip's
// inverse; see whipDecisionFromOrchestratorPacing.
func whipReasonFromOrchestratorPacing(reason orchestratorPacingReason) orchestrator.WhipReason {
	switch reason {
	case orchestratorPacingReasonNotAlive:
		return orchestrator.WhipReasonNotAlive
	case orchestratorPacingReasonAlreadyCapped:
		return orchestrator.WhipReasonAlreadyCapped
	case orchestratorPacingReasonCapCrossed:
		return orchestrator.WhipReasonCapCrossed
	case orchestratorPacingReasonNudge:
		return orchestrator.WhipReasonNudge
	case orchestratorPacingReasonFresh, orchestratorPacingReasonUnreachable, orchestratorPacingReasonEnvBusy:
		fallthrough
	default:
		return orchestrator.WhipReasonFresh
	}
}

// orchestratorWhipOutcome is one orchestrator's result from an explicit
// whip-everything pass — the visible record an operator judges the feature
// by: which orchestrator, what was decided, and why.
type orchestratorWhipOutcome struct {
	id       string
	name     string
	decision orchestratorPacingDecision
	reason   orchestratorPacingReason
}

// orchestratorWhipTarget is one orchestrator the whip selection surface can
// offer -- id plus the name a human-facing row renders, gathered without
// deciding or pushing anything.
type orchestratorWhipTarget struct{ id, name string }

// listWhipOrchestratorTargets enumerates every orchestrator eligible to be
// whipped -- every live session plus every persisted config with no session
// yet, deduplicated by id -- the same union whipOrchestratorsNow decides
// against, so "select all orchestrators" and "push the selected
// orchestrators" can never disagree about who exists.
func (a *App) listWhipOrchestratorTargets() []orchestratorWhipTarget {
	rows := a.orchestratorPacingRows()
	seen := make(map[string]struct{}, len(rows))
	targets := make([]orchestratorWhipTarget, 0, len(rows))
	for _, row := range rows {
		targets = append(targets, orchestratorWhipTarget{id: row.id, name: row.name})
		seen[row.id] = struct{}{}
	}
	if configs, err := a.loadOrchestratorConfigs(); err == nil {
		for _, config := range configs {
			if _, ok := seen[config.ID]; ok {
				continue
			}
			targets = append(targets, orchestratorWhipTarget{id: config.ID, name: config.Name})
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].name != targets[j].name {
			return targets[i].name < targets[j].name
		}
		return targets[i].id < targets[j].id
	})
	return targets
}

// whipOrchestratorsNow pushes only the requested orchestrators (by id), each
// still bound by its own cap and pushed now regardless of staleness. Named
// per orchestrator in the return so a caller can report exactly who was
// pushed and who was skipped, and why skipped.
func (a *App) whipOrchestratorsNow(want map[string]struct{}) []orchestratorWhipOutcome {
	refreshOrchestratorWhipConfig()
	now := time.Now()
	rows := a.orchestratorPacingRows()
	outcomes := make([]orchestratorWhipOutcome, 0, len(want))
	whipped := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if _, ok := want[row.id]; !ok {
			continue
		}
		decision, reason := a.reconcileOrchestratorPacingOne(row, now, true)
		outcomes = append(outcomes, orchestratorWhipOutcome{id: row.id, name: row.name, decision: decision, reason: reason})
		whipped[row.id] = struct{}{}
	}
	// orchestratorPacingRows only covers sessions this desktop holds; a
	// requested orchestrator that was never opened must still be reported with
	// a skip reason rather than silently omitted.
	configs, err := a.loadOrchestratorConfigs()
	if err != nil {
		// The sessions above are still a truthful partial answer.
		return outcomes
	}
	for _, config := range configs {
		if _, ok := want[config.ID]; !ok {
			continue
		}
		if _, ok := whipped[config.ID]; ok {
			continue
		}
		outcomes = append(outcomes, orchestratorWhipOutcome{
			id:       config.ID,
			name:     config.Name,
			decision: orchestratorPacingNone,
			reason:   orchestratorPacingReasonNotAlive,
		})
	}
	return outcomes
}
