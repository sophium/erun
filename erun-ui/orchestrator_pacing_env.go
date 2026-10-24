package main

import (
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/orchestrator"
)

// An orchestrator waiting on work it dispatched to a linked environment is, to
// plain pacing, indistinguishable from one that has gone dark. This file adds
// the missing signal: whether any environment this orchestrator linked is busy
// on a lease it holds, read from what environment_activity.go's poller already
// gathers.

// orchestratorEnvActivitySignal is what the pacing reconciler could determine
// about an orchestrator's linked environments this tick.
type orchestratorEnvActivitySignal int

const (
	// orchestratorEnvActivityIdle means every linked environment was actually
	// observed and none is busy on a lease this orchestrator holds.
	orchestratorEnvActivityIdle orchestratorEnvActivitySignal = iota
	// orchestratorEnvActivityBusy means at least one linked environment is
	// busy on work this orchestrator itself dispatched.
	orchestratorEnvActivityBusy
	// orchestratorEnvActivityUnknown means at least one linked environment's
	// activity could not be read this tick (stopped, edge unreachable, or the
	// activity poller has not observed it yet) and none of the rest were
	// found busy. It falls back to the env-unaware decision: idle would nudge
	// an orchestrator waiting on work this desktop cannot see, and busy would
	// mask one that has gone dark.
	orchestratorEnvActivityUnknown
)

// orchestratorLinkedEnvBusyState is the env-aware signal plus, when busy, the
// busy environment's own detail (see environmentBusyFromIdleStatus) — its own
// words for what is keeping it up, so a nudge that fires anyway past the
// suppression bound can still say what the orchestrator was waiting on.
type orchestratorLinkedEnvBusyState struct {
	signal orchestratorEnvActivitySignal
	detail string
}

// stuckDetail names the busy environment's detail only when this orchestrator
// is still being credited with a suppression that reconcileOrchestratorPacingOne
// has decided to override past the bound — never for the ordinary idle/unknown
// cases, which have nothing distinctive to say.
func (s orchestratorLinkedEnvBusyState) stuckDetail() string {
	if s.signal != orchestratorEnvActivityBusy {
		return ""
	}
	return s.detail
}

// orchestratorLinkedEnvBusyStateFor reduces one orchestrator's linked
// environments to the single signal the pacing gate needs. Only a lease this
// orchestrator's own ID holds counts as busy: an environment busy on another
// orchestrator's job, or an operator's own session, must not silence this one.
func orchestratorLinkedEnvBusyStateFor(orchestratorID string, envs []config.OrchestratorEnvConfig, snapshot map[string]environmentActivityState) orchestratorLinkedEnvBusyState {
	sawUnknown := false
	for _, env := range envs {
		key := selectionKey(uiSelection{Tenant: env.Tenant, Environment: env.Environment})
		state, ok := snapshot[key]
		if !ok || !state.observed {
			// Never observed at all (the activity poller has not reached it
			// yet) and observed-but-inconclusive (stopped, edge unreachable)
			// are the same case here: neither is evidence either way.
			sawUnknown = true
			continue
		}
		if environmentStateBusyForOrchestrator(state, orchestratorID) {
			return orchestratorLinkedEnvBusyState{signal: orchestratorEnvActivityBusy, detail: state.detail}
		}
	}
	if sawUnknown {
		return orchestratorLinkedEnvBusyState{signal: orchestratorEnvActivityUnknown}
	}
	return orchestratorLinkedEnvBusyState{signal: orchestratorEnvActivityIdle}
}

// environmentStateBusyForOrchestrator reports whether orchestratorID appears
// among the orchestrator IDs holding a lease on this environment.
func environmentStateBusyForOrchestrator(state environmentActivityState, orchestratorID string) bool {
	if state.busyHolderOrchestrators == "" || orchestratorID == "" {
		return false
	}
	for _, id := range strings.Split(state.busyHolderOrchestrators, ",") {
		if id == orchestratorID {
			return true
		}
	}
	return false
}

// orchestratorPacingReasonEnvBusy is the reason logOrchestratorPacingTransition
// records while a linked environment's own activity is suppressing the nudge —
// distinct from orchestratorPacingReasonFresh so the log can tell "quiet
// because nothing is happening" from "quiet because it is waiting on
// dispatched work".
const orchestratorPacingReasonEnvBusy orchestratorPacingReason = "env-busy"

// orchestratorPacingEnvBusyBound is the ceiling on how long a linked
// environment's own activity may excuse this orchestrator's silence, so one
// that dispatched work and then died still surfaces while its lanes keep
// running. It reuses the stale-after/max-nudges budget: a busy lane cannot buy
// more patience than the ordinary cap already grants.
func orchestratorPacingEnvBusyBound(cfg orchestrator.WhipConfig) time.Duration {
	return cfg.StaleAfter * time.Duration(cfg.MaxNudges)
}

// orchestratorPacingSuppressedByLinkedEnv is reconcileOrchestratorPacingOne's
// env-aware gate. A busy linked environment is waiting, not idle, so it must
// not accrue staleness or take a nudge. It only excuses an alive,
// automatic-pass candidate (an explicit whip overrides the schedule), and only
// up to orchestratorPacingEnvBusyBound.
func (a *App) orchestratorPacingSuppressedByLinkedEnv(row orchestratorPacingRow, explicit bool, envBusy orchestratorLinkedEnvBusyState, elapsed time.Duration) bool {
	if !row.alive || explicit || envBusy.signal != orchestratorEnvActivityBusy {
		return false
	}
	return elapsed < orchestratorPacingEnvBusyBound(getOrchestratorWhipConfig())
}
