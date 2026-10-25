package main

import (
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/orchestrator"
	"github.com/sophium/erun/erun-ui/internal/runtimeview"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Env-aware pacing: an orchestrator waiting on a linked environment's work is not stalled.

type orchestratorEnvActivitySignal int

const (
	orchestratorEnvActivityIdle orchestratorEnvActivitySignal = iota
	orchestratorEnvActivityBusy
	// orchestratorEnvActivityUnknown falls back to the env-unaware decision: idle
	// would nudge a waiting orchestrator, busy would mask a dark one.
	orchestratorEnvActivityUnknown
)

// orchestratorLinkedEnvBusyState carries the busy env's detail for a nudge that fires past the bound.
type orchestratorLinkedEnvBusyState struct {
	signal orchestratorEnvActivitySignal
	detail string
}

func (s orchestratorLinkedEnvBusyState) stuckDetail() string {
	if s.signal != orchestratorEnvActivityBusy {
		return ""
	}
	return s.detail
}

// orchestratorLinkedEnvBusyStateFor counts only leases held by this orchestrator's
// own ID; other orchestrators' or the operator's work must not silence it.
func orchestratorLinkedEnvBusyStateFor(orchestratorID string, envs []config.OrchestratorEnvConfig, snapshot map[string]runtimeview.ActivityState) orchestratorLinkedEnvBusyState {
	sawUnknown := false
	for _, env := range envs {
		key := selectionKey(uimodel.Selection{Tenant: env.Tenant, Environment: env.Environment})
		state, ok := snapshot[key]
		if !ok || !state.Observed {
			// Unobserved and inconclusive are both no evidence either way.
			sawUnknown = true
			continue
		}
		if environmentStateBusyForOrchestrator(state, orchestratorID) {
			return orchestratorLinkedEnvBusyState{signal: orchestratorEnvActivityBusy, detail: state.Detail}
		}
	}
	if sawUnknown {
		return orchestratorLinkedEnvBusyState{signal: orchestratorEnvActivityUnknown}
	}
	return orchestratorLinkedEnvBusyState{signal: orchestratorEnvActivityIdle}
}

func environmentStateBusyForOrchestrator(state runtimeview.ActivityState, orchestratorID string) bool {
	if state.BusyHolderOrchestrators == "" || orchestratorID == "" {
		return false
	}
	for _, id := range strings.Split(state.BusyHolderOrchestrators, ",") {
		if id == orchestratorID {
			return true
		}
	}
	return false
}

const orchestratorPacingReasonEnvBusy orchestratorPacingReason = "env-busy"

// orchestratorPacingEnvBusyBound caps how long busy lanes excuse silence, so a
// dead orchestrator still surfaces; it never exceeds the ordinary cap budget.
func orchestratorPacingEnvBusyBound(cfg orchestrator.WhipConfig) time.Duration {
	return cfg.StaleAfter * time.Duration(cfg.MaxNudges)
}

// orchestratorPacingSuppressedByLinkedEnv excuses only an alive automatic-pass
// candidate, and only up to orchestratorPacingEnvBusyBound.
func (a *App) orchestratorPacingSuppressedByLinkedEnv(row orchestratorPacingRow, explicit bool, envBusy orchestratorLinkedEnvBusyState, elapsed time.Duration) bool {
	if !row.alive || explicit || envBusy.signal != orchestratorEnvActivityBusy {
		return false
	}
	return elapsed < orchestratorPacingEnvBusyBound(getOrchestratorWhipConfig())
}
