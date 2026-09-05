package main

import (
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// erun#1699: an orchestrator that dispatched work to a linked environment and
// is waiting on it is, to plain pacing, indistinguishable from one that has
// gone dark — both simply show no fresh activity report. This file adds the
// missing signal: whether any environment this orchestrator itself linked
// (config-scoped, never "whatever happens to be running") is busy on a lease
// this orchestrator holds. The observation is not new plumbing — it is
// exactly what environment_activity.go's poller already gathers for the
// sidebar hover card; this only reads it a second time, filtered to leases
// this orchestrator's own ID holds.

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
	// found busy. It must resolve to neither of the other two: collapsing it
	// into idle would nudge an orchestrator that is genuinely waiting on work
	// this desktop simply cannot see right now, and collapsing it into busy
	// would mask one that has actually gone dark. Falling back to the base,
	// env-unaware decision is the explicit choice for this case — exactly
	// today's behaviour, applied deliberately rather than by an unnoticed
	// default.
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
// orchestrator's job, or an operator's own session, must not silence this one
// — the lease's holder is the discriminator, exactly as the issue asks for.
func orchestratorLinkedEnvBusyStateFor(orchestratorID string, envs []eruncommon.OrchestratorEnvConfig, snapshot map[string]environmentActivityState) orchestratorLinkedEnvBusyState {
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
// dispatched work" (erun#1699).
const orchestratorPacingReasonEnvBusy orchestratorPacingReason = "env-busy"

// orchestratorPacingEnvBusyBound is the ceiling on how long a linked
// environment's own activity may excuse this orchestrator's silence. Without
// one, an orchestrator that dispatched work and then genuinely died would
// never be surfaced as long as its lanes kept running — the exact regression
// this file exists to avoid introducing. The bound reuses the pass's own
// stale-after/max-nudges budget (its default, ten minutes times six, is the
// same "about an hour" already named in the cap notice) rather than a new
// knob: a busy lane cannot buy more patience than the ordinary cap already
// grants an orchestrator with no linked work at all.
func orchestratorPacingEnvBusyBound(cfg eruncommon.WhipConfig) time.Duration {
	return cfg.StaleAfter * time.Duration(cfg.MaxNudges)
}

// orchestratorPacingSuppressedByLinkedEnv is reconcileOrchestratorPacingOne's
// env-aware gate, split out to keep that function under this module's
// complexity budget. A busy linked environment is waiting, not idle: it must
// not accrue staleness or take a nudge (erun#1699). This only ever excuses an
// alive, automatic-pass candidate — a session the desktop cannot see gets
// "not-alive" regardless, and an explicit operator-triggered whip is the
// operator overriding the schedule, exactly like it already overrides plain
// staleness. The excuse itself is bounded (orchestratorPacingEnvBusyBound):
// past that ceiling a dispatched-then-abandoned orchestrator must still
// surface rather than hide behind lanes that outlived it.
func (a *App) orchestratorPacingSuppressedByLinkedEnv(row orchestratorPacingRow, explicit bool, envBusy orchestratorLinkedEnvBusyState, elapsed time.Duration) bool {
	if !row.alive || explicit || envBusy.signal != orchestratorEnvActivityBusy {
		return false
	}
	return elapsed < orchestratorPacingEnvBusyBound(getOrchestratorWhipConfig())
}
